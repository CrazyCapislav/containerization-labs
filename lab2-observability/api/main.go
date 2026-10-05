// api — подопытный сервис для лабы 2.
//
// Эндпоинты, которыми провоцируются ситуации для наблюдения:
//   GET /health     — отдаёт ok
//   GET /fail       — отвечает 500 и помечает спан как ошибочный
//   GET /slow       — спит 1..3 секунды внутри вложенного спана slow-op
//   GET /load?n=N   — делает N запросов к себе, чтобы подскочил RPS
//   GET /metrics    — метрики Prometheus (RED)
//
// Логи структурированные (JSON) и содержат trace_id текущего запроса,
// поэтому от строки лога в Grafana можно перейти к трейсу в Jaeger.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// ---------- метрики RED ----------
//
// Rate    — http_requests_total
// Errors  — http_request_errors_total
// Duration— http_request_duration_seconds

var (
	reqTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Общее число обработанных HTTP-запросов.",
	}, []string{"method", "path", "status"})

	errTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "Число запросов, завершившихся кодом 5xx.",
	}, []string{"method", "path"})

	reqDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "Время обработки запроса в секундах.",
		// Границы подобраны под профиль сервиса: быстрые ручки в миллисекундах,
		// /slow намеренно уходит в диапазон 1..3 с.
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 1.5, 2, 3, 5, 10},
	}, []string{"method", "path"})
)

const serviceName = "api"

var tracer = otel.Tracer(serviceName)

// ---------- логирование ----------

// logger возвращает логгер, в который уже подставлены trace_id и span_id
// текущего запроса. Именно эта связка позволяет из лога попасть в трейс.
func logger(ctx context.Context) *slog.Logger {
	l := slog.Default()
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		l = l.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return l
}

// ---------- учёт метрик ----------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// instrument считает метрики и пишет строку лога по каждому запросу.
// Метка path берётся из имени маршрута, а не из URL, иначе кардинальность
// метрик росла бы с каждым новым значением query-параметров.
func instrument(path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(sw, r)

		dur := time.Since(start)
		status := strconv.Itoa(sw.status)

		reqTotal.WithLabelValues(r.Method, path, status).Inc()
		reqDuration.WithLabelValues(r.Method, path).Observe(dur.Seconds())
		if sw.status >= 500 {
			errTotal.WithLabelValues(r.Method, path).Inc()
		}

		lvl := slog.LevelInfo
		if sw.status >= 500 {
			lvl = slog.LevelError
		}
		logger(r.Context()).Log(r.Context(), lvl, "обработан запрос",
			"method", r.Method,
			"path", path,
			"status", sw.status,
			"duration_ms", dur.Milliseconds(),
		)
	})
}

// ---------- обработчики ----------

func handleHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}

// handleFail отвечает ошибкой и помечает спан как неуспешный,
// чтобы Jaeger подсветил его красным.
func handleFail(w http.ResponseWriter, r *http.Request) {
	span := trace.SpanFromContext(r.Context())
	err := errors.New("обработчик /fail сломался намеренно")

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.SetAttributes(attribute.Bool("fail.intentional", true))

	logger(r.Context()).Error("намеренная ошибка", "error", err.Error())
	http.Error(w, "internal server error\n", http.StatusInternalServerError)
}

// handleSlow спит 1..3 секунды внутри отдельного вложенного спана,
// чтобы в водопаде Jaeger было видно, на каком шаге ушло время.
func handleSlow(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "slow-op")
	defer span.End()

	delay := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
	span.SetAttributes(attribute.Int64("slow.delay_ms", delay.Milliseconds()))

	logger(ctx).Info("начинаю медленную операцию", "delay_ms", delay.Milliseconds())
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		span.SetStatus(codes.Error, "запрос отменён клиентом")
		return
	}

	fmt.Fprintf(w, "спал %d мс\n", delay.Milliseconds())
}

// handleLoad делает пачку запросов к самому себе, чтобы поднять RPS.
// Контекст пробрасывается, поэтому порождённые запросы попадают в тот же трейс.
func handleLoad(w http.ResponseWriter, r *http.Request) {
	n := 50
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 500 {
			n = parsed
		}
	}

	ctx, span := tracer.Start(r.Context(), "load-generator")
	defer span.End()
	span.SetAttributes(attribute.Int("load.requests", n))

	self := "http://127.0.0.1" + listenAddr()
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, self+"/health", nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	logger(ctx).Info("нагрузка создана", "requests", n)
	fmt.Fprintf(w, "сделано %d запросов к /health\n", n)
}

// ---------- инициализация трейсинга ----------

// initTracing поднимает экспортёр OTLP. Адрес берётся из стандартной
// переменной OTEL_EXPORTER_OTLP_ENDPOINT, её же понимают все остальные
// инструменты экосистемы OpenTelemetry.
func initTracing(ctx context.Context) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("создание экспортёра OTLP: %w", err)
	}

	// NewSchemaless, а не NewWithAttributes: версия семантических соглашений
	// в SDK и в импортированном semconv различается, и слияние ресурсов
	// с разными Schema URL завершается ошибкой.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion("1.0.0"),
	))
	if err != nil {
		return nil, fmt.Errorf("сборка ресурса: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

func listenAddr() string {
	if a := os.Getenv("ADDR"); a != "" {
		return a
	}
	return ":8080"
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := initTracing(ctx)
	if err != nil {
		// Без трейсинга сервис всё равно должен работать: метрики и логи важнее.
		slog.Warn("трейсинг не поднялся, работаю без него", "error", err.Error())
		shutdownTracing = func(context.Context) error { return nil }
	}

	mux := http.NewServeMux()
	register := func(path string, h http.HandlerFunc) {
		mux.Handle(path, instrument(path, h))
	}

	register("/health", handleHealth)
	register("/fail", handleFail)
	register("/slow", handleSlow)
	register("/load", handleLoad)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "api\n\n  GET /health\n  GET /fail\n  GET /slow\n  GET /load?n=N\n  GET /metrics\n")
	})

	// /metrics намеренно вне трейсинга: иначе Prometheus своим опросом
	// каждые несколько секунд забивал бы Jaeger бессмысленными спанами.
	root := http.NewServeMux()
	root.Handle("/metrics", promhttp.Handler())
	root.Handle("/", otelhttp.NewHandler(mux, "http.server",
		// Имя спана по маршруту, иначе все запросы сольются в один "http.server".
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	))

	srv := &http.Server{
		Addr:              listenAddr(),
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("сервис запущен",
			"addr", listenAddr(),
			"otlp_endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
			"pid", os.Getpid(),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("сервер остановился с ошибкой", "error", err.Error())
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("получен сигнал, завершаюсь")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("ошибка остановки сервера", "error", err.Error())
	}
	if err := shutdownTracing(shutdownCtx); err != nil {
		slog.Error("ошибка остановки трейсинга", "error", err.Error())
	}
}
