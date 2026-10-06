// worker — фоновый обработчик заказов.
//
// Раз в несколько секунд забирает пачку необработанных заказов и помечает
// их обработанными. Несколько реплик работают параллельно и не мешают друг
// другу: выборка идёт с FOR UPDATE SKIP LOCKED, поэтому одну и ту же строку
// два worker'а не возьмут.
//
// HTTP-порт поднимается только ради /health и /metrics: пробы Kubernetes
// и сбор метрик иначе работать не будут.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"lab/shop/internal/store"
)

var (
	processed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_orders_processed_total",
		Help: "Сколько заказов обработано worker'ом.",
	})

	cycles = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_worker_cycles_total",
		Help: "Сколько циклов обработки выполнено.",
	})

	workerErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_worker_errors_total",
		Help: "Ошибки при обработке пачки заказов.",
	})

	pending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_orders_pending",
		Help: "Сколько заказов ждёт обработки.",
	})

	dbUp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_db_up",
		Help: "Доступна ли база: 1 доступна, 0 нет.",
	})
)

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st := store.New()

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8081"
	}
	interval := time.Duration(envInt("INTERVAL_SECONDS", 5)) * time.Second
	batch := envInt("BATCH_SIZE", 10)

	mux := http.NewServeMux()
	// Готовность worker'а тоже не зависит от базы: иначе падение postgres
	// привело бы к перезапуску всех реплик вместо простого простоя.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("worker запущен", "addr", addr, "interval", interval.String(),
			"batch", batch, "pod", os.Getenv("POD_NAME"))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("сервер остановился", "error", err.Error())
			os.Exit(1)
		}
	}()

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("получен сигнал, завершаюсь")
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = srv.Shutdown(sctx)
			cancel()
			return
		case <-t.C:
		}

		cycles.Inc()

		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := st.Ping(c); err != nil {
			dbUp.Set(0)
			cancel()
			slog.Warn("база недоступна, пропускаю цикл", "error", err.Error())
			continue
		}
		dbUp.Set(1)

		ids, err := st.ProcessBatch(c, batch)
		if err != nil {
			workerErrors.Inc()
			slog.Error("не удалось обработать пачку", "error", err.Error())
			cancel()
			continue
		}

		if n, err := st.PendingCount(c); err == nil {
			pending.Set(float64(n))
		}
		cancel()

		if len(ids) > 0 {
			processed.Add(float64(len(ids)))
			slog.Info("заказы обработаны", "count", len(ids), "ids", ids)
		}
	}
}
