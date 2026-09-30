//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
)

func mustFail(t *testing.T, name, wantFragment, query string, args ...any) {
	t.Helper()

	_, err := env.pool.Exec(context.Background(), query, args...)
	if err == nil {
		t.Fatalf("%s: statement succeeded, expected the database to refuse it", name)
	}

	if wantFragment != "" && !strings.Contains(err.Error(), wantFragment) {
		t.Fatalf("%s: unexpected error: %v", name, err)
	}
}

// O ledger é append-only e as invariantes valem no próprio banco,
// independentemente da aplicação.
func TestDatabaseConstraintsProtectInvariants(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	mustFail(t, "update ledger", "append-only",
		`UPDATE ledger_entries SET amount = amount + 1 WHERE wallet_id = $1`, walletID)

	mustFail(t, "delete ledger", "append-only",
		`DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)

	mustFail(t, "truncate ledger", "append-only", `TRUNCATE ledger_entries`)

	mustFail(t, "negative balance", "wallets_balance_non_negative",
		`UPDATE wallets SET balance = -1 WHERE id = $1`, walletID)

	mustFail(t, "duplicate ledger entry per (wallet, transaction)", "ledger_transaction_unique", `
		INSERT INTO ledger_entries
			(id, wallet_id, transaction_id, direction, amount, balance_before, balance_after, created_at)
		SELECT gen_random_uuid(), wallet_id, transaction_id, direction, amount, balance_before, balance_after, now()
		FROM ledger_entries WHERE wallet_id = $1`, walletID)

	mustFail(t, "second OPENING for the wallet", "wager_transactions_single_opening_uq", `
		INSERT INTO wager_transactions
			(id, wallet_id, player_id, kind, amount, currency, state, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'OPENING', 100, 'BRL', 'PROCESSED', now(), now())`,
		walletID, playerID)

	// OPENING não carrega metadados externos.
	zeroWallet, zeroPlayer := createWallet(t, "0.00")

	mustFail(t, "OPENING with provider metadata", "wager_transactions_origin_check", `
		INSERT INTO wager_transactions
			(id, wallet_id, player_id, provider_id, kind, amount, currency, state, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'provider-a', 'OPENING', 100, 'BRL', 'PROCESSED', now(), now())`,
		zeroWallet, zeroPlayer)

	// Operação externa sem identificadores externos.
	mustFail(t, "external operation without provider", "wager_transactions_origin_check", `
		INSERT INTO wager_transactions
			(id, wallet_id, player_id, kind, amount, currency, state, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'BET', 100, 'BRL', 'PENDING', now(), now())`,
		zeroWallet, zeroPlayer)

	// Uma referência não recebe duas reversões bem-sucedidas.
	bet := newTx(walletID, playerID, "BET", "10.00")
	betResp := postA(t, shared[0], bet)

	if betResp.status != 201 {
		t.Fatalf("bet: %d %s", betResp.status, betResp.raw)
	}

	refund := reversal(walletID, playerID, "REFUND", "10.00", bet)
	if r := postA(t, shared[0], refund); r.status != 201 {
		t.Fatalf("refund: %d %s", r.status, r.raw)
	}

	mustFail(t, "second processed reversal for the same reference", "wager_transactions_single_reversal_uq", `
		INSERT INTO wager_transactions
			(id, provider_id, external_transaction_id, idempotency_key, wallet_id, player_id,
			 round_id, game_id, kind, amount, currency, reference_transaction_id, state, created_at, updated_at)
		VALUES (gen_random_uuid(), 'provider-a', $3, $3, $1, $2,
			 'round-1', 'game', 'ROLLBACK', 1000, 'BRL', $4, 'PROCESSED', now(), now())`,
		walletID, playerID, "manual-"+randHex(4), betResp.str("transactionId"))

	assertReconciled(t, walletID)
}
