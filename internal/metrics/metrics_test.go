package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesApplicationMetrics(t *testing.T) {
	TransactionsTotal.WithLabelValues("http", "BET", "PROCESSED").Inc()
	ReconciliationDivergences.Inc()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()

	for _, name := range []string{
		"wager_transactions_total",
		"wager_reconciliation_divergences_total",
		"wager_outbox_lag_seconds",
		"wager_duplicates_total",
	} {
		if !strings.Contains(body, name) {
			t.Fatalf("metric %s missing from exposition", name)
		}
	}
}
