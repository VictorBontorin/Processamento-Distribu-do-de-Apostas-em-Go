//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func walletBalanceOn(t *testing.T, inst *instance, walletID string) string {
	t.Helper()

	r := do(t, "GET", inst.url+"/wallets/"+walletID, tokInternal(t), nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("get wallet: status=%d body=%s", r.status, r.raw)
	}

	return r.str("balance", "amount")
}

// assertReconciledIn confere saldo x ledger em um banco específico.
func assertReconciledIn(t *testing.T, pool *pgxpool.Pool, inst *instance, walletID string) {
	t.Helper()

	var stored, calculated int64

	err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT balance FROM wallets WHERE id = $1),
			COALESCE((SELECT SUM(CASE direction WHEN 'CREDIT' THEN amount ELSE -amount END)
			          FROM ledger_entries WHERE wallet_id = $1), 0)
	`, walletID).Scan(&stored, &calculated)
	if err != nil {
		t.Fatalf("reconcile query: %v", err)
	}

	if stored != calculated {
		t.Fatalf("wallet %s: stored=%d ledger=%d", walletID, stored, calculated)
	}

	r := do(t, "POST", inst.url+"/wallets/"+walletID+"/reconciliation", tokInternal(t), nil, nil)
	if r.status != http.StatusOK || !r.boolean("consistent") {
		t.Fatalf("reconciliation endpoint: status=%d body=%s", r.status, r.raw)
	}
}

// providerTx consulta uma transação pelo par (provedor, id externo).
func providerTx(t *testing.T, inst *instance, tok, provider, ext string) resp {
	t.Helper()

	return do(t, "GET", inst.url+"/providers/"+provider+"/wagering/transactions/"+ext, tok, nil, nil)
}

func waitTxStatus(t *testing.T, inst *instance, provider, ext, want string) resp {
	t.Helper()

	var last resp

	eventually(t, 60*secondsDuration, "transaction "+ext+" to reach "+want, func() bool {
		last = providerTx(t, inst, tokInternal(t), provider, ext)
		return last.status == http.StatusOK && last.str("status") == want
	})

	return last
}

func ledgerCountIn(t *testing.T, pool *pgxpool.Pool, walletID, direction string) int {
	t.Helper()

	return count(t, pool,
		`SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND direction = $2`,
		walletID, direction)
}
