// api — HTTP-сервис магазина shop.
//
//	GET  /health   отдаёт ok; при HEALTH_FAIL=true отвечает 503
//	POST /order    создаёт заказ в postgres
//	GET  /orders   отдаёт список заказов
//	GET  /metrics  метрики Prometheus
//
// Готовность намеренно не зависит от базы: иначе недоступность postgres
// вывела бы из балансировки все поды разом, а выкатка повисла бы навсегда.
package main

import (
	"context"
	"encoding/json"
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
	reqTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Общее число обработанных HTTP-запросов.",
	}, []string{"method", "path", "status"})

	reqDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Время обработки запроса в секундах.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"method", "path"})

	ordersCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shop_orders_created_total",
		Help: "Сколько заказов создано через POST /order.",
	})

	dbErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shop_db_errors_total",
		Help: "Ошибки обращения к базе данных.",
	}, []string{"operation"})

	dbUp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_db_up",
		Help: "Доступна ли база: 1 доступна, 0 нет.",
	})
)

var st *store.Store

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }

func instrument(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r)
		reqTotal.WithLabelValues(r.Method, path, strconv.Itoa(sw.status)).Inc()
		reqDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleHealth отвечает 503 при HEALTH_FAIL=true. Это нужно в Части 2,
// чтобы выкатить заведомо сломанную версию и показать, как rollout
// зависает, не убивая исправные поды.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("HEALTH_FAIL") == "true" {
		slog.Error("проба здоровья провалена намеренно", "reason", "HEALTH_FAIL=true")
		http.Error(w, "health check disabled\n", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "нужен POST\n", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Item string `json:"item"`
		Qty  int    `json:"qty"`
	}
	// Пустое или битое тело допускаем: удобно создавать заказы одним curl
	// без данных, недостающие поля заполним значениями по умолчанию ниже.
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Item == "" {
		req.Item = "товар-" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	}
	if req.Qty <= 0 {
		req.Qty = 1
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	o, err := st.CreateOrder(ctx, req.Item, req.Qty)
	if err != nil {
		dbErrors.WithLabelValues("create_order").Inc()
		slog.Error("не удалось создать заказ", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	ordersCreated.Inc()
	slog.Info("заказ создан", "id", o.ID, "item", o.Item, "qty", o.Qty)
	writeJSON(w, http.StatusCreated, o)
}

func handleListOrders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	orders, err := st.ListOrders(ctx, limit)
	if err != nil {
		dbErrors.WithLabelValues("list_orders").Inc()
		slog.Error("не удалось прочитать заказы", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// watchDB держит метрику доступности базы в актуальном состоянии
// и повторяет миграцию, пока она не пройдёт: база может появиться позже
// самого сервиса, как и происходит по порядку частей этой лабы.
func watchDB(ctx context.Context) {
	migrated := false
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := st.Ping(c)
		cancel()

		if err != nil {
			dbUp.Set(0)
		} else {
			dbUp.Set(1)
			if !migrated {
				c2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
				if e := st.Migrate(c2); e != nil {
					slog.Error("миграция не прошла", "error", e.Error())
				} else {
					migrated = true
					slog.Info("схема базы готова")
				}
				cancel2()
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st = store.New()
	go watchDB(ctx)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", instrument("/health", handleHealth))
	mux.HandleFunc("/order", instrument("/order", handleCreateOrder))
	mux.HandleFunc("/orders", instrument("/orders", handleListOrders))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api\n\n  GET  /health\n  POST /order\n  GET  /orders\n  GET  /metrics\n"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		slog.Info("api запущен", "addr", addr, "version", os.Getenv("APP_VERSION"), "pod", os.Getenv("POD_NAME"))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("сервер остановился", "error", err.Error())
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	// Задержка перед остановкой. Kubernetes шлёт SIGTERM и убирает под из
	// endpoints одновременно, но правила маршрутизации на нодах обновляются
	// асинхронно. Если закрыть сокет сразу, часть трафика прилетит в уже
	// мёртвый под и клиент получит отказ соединения. Обычно для этого
	// используют хук preStop со sleep, но образ собран из scratch,
	// где нет ни шелла, ни sleep, поэтому задержка реализована здесь.
	delay := 5 * time.Second
	if v := os.Getenv("SHUTDOWN_DELAY_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			delay = time.Duration(n) * time.Second
		}
	}
	slog.Info("получен сигнал, продолжаю обслуживать до истечения задержки",
		"delay_seconds", int(delay.Seconds()))
	time.Sleep(delay)

	slog.Info("завершаюсь")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}
