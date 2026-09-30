package http

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wager/internal/infrastructure/sqs"
)

type HealthHandler struct {
	pool *pgxpool.Pool
	sqs  *sqs.Client
}

func NewHealthHandler(pool *pgxpool.Pool, sqsClient *sqs.Client) *HealthHandler {
	return &HealthHandler{pool: pool, sqs: sqsClient}
}

// Live: o processo está de pé.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ready: PostgreSQL e SQS respondem.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "sqs": "ok"}
	ready := true

	if err := h.pool.Ping(ctx); err != nil {
		checks["postgres"] = "unavailable"
		ready = false
	}

	if err := h.sqs.Ping(ctx); err != nil {
		checks["sqs"] = "unavailable"
		ready = false
	}

	status, label := http.StatusOK, "ready"
	if !ready {
		status, label = http.StatusServiceUnavailable, "not_ready"
	}

	writeJSON(w, status, map[string]any{"status": label, "checks": checks})
}
