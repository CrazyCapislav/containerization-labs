// Команда batch — заглушка «ночной аналитики». Намеренно жжёт процессор и
// держит память, не делая ничего полезного. Нужна, чтобы создать на кластере
// управляемую нехватку ресурсов и посмотреть, кого Kubernetes принесёт
// в жертву первым.
//
// Вся нагрузка задаётся переменными окружения, чтобы менять её из values.yaml
// через helm upgrade, не пересобирая образ:
//
//	BATCH_CPU_WORKERS            сколько горутин крутят процессор (по умолчанию 1)
//	BATCH_CPU_DUTY_PERCENT       доля времени под нагрузкой, 1..100 (по умолчанию 80)
//	BATCH_MEM_MB                 сколько памяти занять на старте (по умолчанию 64)
//	BATCH_MEM_GROWTH_MB_PER_SEC  на сколько наращивать каждую секунду (0 — не расти)
//	BATCH_MEM_MAX_MB             потолок, выше которого не расти
//	BATCH_ADDR                   адрес HTTP-сервера для проб и метрик (:8080)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Размер страницы памяти. Записываем по байту в каждую страницу, иначе ядро
// не выделит физическую память и RSS не вырастет.
const pageSize = 4096

var (
	heldBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "batch_memory_held_bytes",
		Help: "Сколько памяти процесс держит специально.",
	})
	iterations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "batch_iterations_total",
		Help: "Витков счётного цикла с момента старта.",
	})
	gomaxprocs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "batch_gomaxprocs",
		Help: "Сколько ядер видит рантайм Go. С Go 1.25 учитывает лимит cgroup.",
	})

	ready atomic.Bool
)

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("не разобрал переменную окружения, беру значение по умолчанию",
			"name", name, "value", v, "default", def)
		return def
	}
	return n
}

// burnCPU держит процессор занятым. duty задаёт долю времени под нагрузкой:
// при 100 это бесконечный цикл, при 50 — половину периода работа, половину сон.
// Скважность нужна не для красоты: если выесть весь выделенный квант, HTTP-обработчик
// не успеет ответить на пробу за её timeout, kubelet сочтёт контейнер мёртвым и
// перезапустит его. В эксперименте это выглядело бы как вытеснение, хотя причина
// совсем другая.
func burnCPU(ctx context.Context, duty int) {
	const period = 100 * time.Millisecond
	busy := period * time.Duration(duty) / 100

	for ctx.Err() == nil {
		start := time.Now()
		var x float64
		for time.Since(start) < busy {
			for i := 0; i < 10000; i++ {
				x += float64(i) * 1.000001
			}
			iterations.Inc()
		}
		runtime.KeepAlive(x)

		if sleep := period - busy; sleep > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(sleep):
			}
		}
	}
}

// holdMemory занимает память блоками по мегабайту и держит ссылки на них,
// чтобы сборщик мусора не вернул их обратно.
func holdMemory(ctx context.Context, startMB, growthMBPerSec, maxMB int) {
	blocks := make([][]byte, 0, maxMB)

	grab := func(mb int) {
		for i := 0; i < mb && len(blocks) < maxMB; i++ {
			b := make([]byte, 1<<20)
			for p := 0; p < len(b); p += pageSize {
				b[p] = 1
			}
			blocks = append(blocks, b)
		}
		heldBytes.Set(float64(len(blocks)) * (1 << 20))
	}

	grab(startMB)
	slog.Info("занял стартовую память", "held_mb", len(blocks))

	if growthMBPerSec <= 0 {
		<-ctx.Done()
		runtime.KeepAlive(blocks)
		return
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			runtime.KeepAlive(blocks)
			return
		case <-tick.C:
			before := len(blocks)
			grab(growthMBPerSec)
			switch {
			case len(blocks) != before:
				slog.Info("нарастил память", "held_mb", len(blocks))
			case len(blocks) >= maxMB:
				slog.Info("достиг потолка памяти, больше не расту", "held_mb", len(blocks))
				<-ctx.Done()
				runtime.KeepAlive(blocks)
				return
			}
		}
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	workers := envInt("BATCH_CPU_WORKERS", 1)
	duty := envInt("BATCH_CPU_DUTY_PERCENT", 80)
	memStart := envInt("BATCH_MEM_MB", 64)
	memGrowth := envInt("BATCH_MEM_GROWTH_MB_PER_SEC", 0)
	memMax := envInt("BATCH_MEM_MAX_MB", memStart)

	addr := os.Getenv("BATCH_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if workers < 1 {
		workers = 1
	}
	if duty < 1 {
		duty = 1
	}
	if duty > 100 {
		duty = 100
	}
	if memMax < memStart {
		memMax = memStart
	}

	gomaxprocs.Set(float64(runtime.GOMAXPROCS(0)))
	slog.Info("запускаю batch",
		"cpu_workers", workers,
		"cpu_duty_percent", duty,
		"mem_start_mb", memStart,
		"mem_growth_mb_per_sec", memGrowth,
		"mem_max_mb", memMax,
		"gomaxprocs", runtime.GOMAXPROCS(0),
		"numcpu", runtime.NumCPU(),
		"addr", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	for i := 0; i < workers; i++ {
		go burnCPU(ctx, duty)
	}
	go holdMemory(ctx, memStart, memGrowth, memMax)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP-сервер упал", "err", err)
			os.Exit(1)
		}
	}()
	ready.Store(true)

	<-ctx.Done()
	slog.Info("получен сигнал, останавливаюсь")
	ready.Store(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("не успел закрыть сервер", "err", err)
	}
}
