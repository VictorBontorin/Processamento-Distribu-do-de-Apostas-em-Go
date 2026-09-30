// Package metrics reúne as métricas Prometheus da aplicação.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Resultados por status (operações novas, sem replays).
	TransactionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_transactions_total",
		Help: "Operações financeiras concluídas por origem, tipo e status.",
	}, []string{"source", "kind", "status"})

	RejectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_rejections_total",
		Help: "Rejeições de negócio por failureCode.",
	}, []string{"failure_code"})

	// Duplicatas: replay idempotente, conflito de conteúdo, mensagem repetida.
	DuplicatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_duplicates_total",
		Help: "Recebimentos repetidos por origem e tipo (replay, conflict, duplicate_message).",
	}, []string{"source", "type"})

	ProcessingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "wager_processing_duration_seconds",
		Help:    "Latência do processamento de uma operação (transação SQL inclusa).",
		Buckets: prometheus.DefBuckets,
	}, []string{"source"})

	ConcurrencyConflicts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_concurrency_conflicts_total",
		Help: "Conflitos de concorrência no banco (serialization_failure, deadlock, unique_violation).",
	}, []string{"type"})

	SQSMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_sqs_messages_total",
		Help: "Mensagens SQS por resultado (processed, retry, dlq, released).",
	}, []string{"result"})

	SQSRetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wager_sqs_retries_total",
		Help: "Mensagens SQS reagendadas para nova tentativa.",
	})

	SQSDLQTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_sqs_dlq_total",
		Help: "Mensagens enviadas à DLQ (permanent) ou na última tentativa antes do redrive (redrive_expected).",
	}, []string{"cause"})

	OutboxPublishedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wager_outbox_published_total",
		Help: "Eventos da outbox publicados.",
	})

	OutboxFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wager_outbox_publish_failures_total",
		Help: "Falhas de publicação da outbox (com retry agendado).",
	})

	OutboxLagSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "wager_outbox_lag_seconds",
		Help: "Idade do evento pendente mais antigo da outbox (0 se vazia).",
	})

	PendingReferenceTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_pending_reference_total",
		Help: "Desfecho de operações que esperavam uma referência.",
	}, []string{"outcome"})

	ReconciliationRuns = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wager_reconciliation_runs_total",
		Help: "Reconciliações de carteira executadas.",
	})

	ReconciliationDivergences = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wager_reconciliation_divergences_total",
		Help: "Reconciliações com divergência entre saldo e ledger.",
	})

	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wager_http_requests_total",
		Help: "Requisições HTTP por método, rota e status.",
	}, []string{"method", "route", "status"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "wager_http_request_duration_seconds",
		Help:    "Latência das requisições HTTP.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// Handler expõe as métricas no formato Prometheus.
func Handler() http.Handler {
	return promhttp.Handler()
}
