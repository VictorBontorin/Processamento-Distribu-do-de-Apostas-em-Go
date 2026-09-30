//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// Reinício de todos os processos: idempotência, pendências e consistência
// financeira sobrevivem (estado só no PostgreSQL).
func TestRestartPreservesIdempotencyAndPendingReferences(t *testing.T) {
	db, pool := newDatabase(t)
	queues := newQueues(env.prefix + "-restart-" + randHex(2))

	t.Cleanup(func() { deleteQueues(queues) })

	opts := instanceOpts{
		name:   "before-restart",
		db:     db,
		queues: queues,
		env:    map[string]string{"PENDING_REFERENCE_TTL": "10m"},
	}

	first := startTest(t, opts)

	walletID, playerID := createWalletOn(t, first, "100.00")

	bet := newTx(walletID, playerID, "BET", "30.00")
	if r := postA(t, first, bet); r.status != http.StatusCreated || r.str("balance", "amount") != "70.00" {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	// REFUND antes da BET correspondente: fica pendente e durável.
	later := newTx(walletID, playerID, "BET", "20.00")
	refund := reversal(walletID, playerID, "REFUND", "20.00", later)

	if r := postA(t, first, refund); r.status != http.StatusAccepted {
		t.Fatalf("pending refund: %d %s", r.status, r.raw)
	}

	// kill -9: sem shutdown.
	first.kill()

	opts.name = "after-restart"
	second := startTest(t, opts)

	// Idempotência persistente: o replay devolve o resultado original.
	replay := postA(t, second, bet)
	if replay.status != http.StatusOK || !replay.boolean("idempotentReplay") || replay.str("balance", "amount") != "70.00" {
		t.Fatalf("replay after restart: %d %s", replay.status, replay.raw)
	}

	if n := ledgerCountIn(t, pool, walletID, "DEBIT"); n != 1 {
		t.Fatalf("debits=%d, want 1", n)
	}

	// A pendência sobreviveu ao reinício.
	pending := providerTx(t, second, tokInternal(t), providerA, refund.ext)
	if pending.str("status") != "PENDING_REFERENCE" {
		t.Fatalf("pending after restart: %s", pending.raw)
	}

	// A referência chega: outra instância conclui a reversão.
	if r := postA(t, second, later); r.status != http.StatusCreated {
		t.Fatalf("later bet: %d %s", r.status, r.raw)
	}

	eventually(t, secondsDuration*60, "refund resolved after restart", func() bool {
		r := providerTx(t, second, tokInternal(t), providerA, refund.ext)
		return r.str("status") == "PROCESSED"
	})

	if got := walletBalanceOn(t, second, walletID); got != "70.00" {
		t.Fatalf("balance=%s, want 70.00", got)
	}

	assertReconciledIn(t, pool, second, walletID)
}
