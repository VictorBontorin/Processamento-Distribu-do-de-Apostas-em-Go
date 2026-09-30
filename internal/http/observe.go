package http

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"wager/internal/metrics"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// observe registra métricas e o log de acesso de cada requisição. Não
// registra corpo, cabeçalhos nem parâmetros: apenas método, rota, status e
// duração (o correlationId entra pelo contexto).
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}

		elapsed := time.Since(start)

		metrics.HTTPRequestsTotal.
			WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.
			WithLabelValues(r.Method, route).Observe(elapsed.Seconds())

		level := slog.LevelInfo
		if r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" || r.URL.Path == "/metrics" {
			level = slog.LevelDebug
		}

		slog.Log(
			r.Context(),
			level,
			"http request",
			"method", r.Method,
			"route", route,
			"status", rec.status,
			"durationMs", elapsed.Milliseconds(),
		)
	})
}
