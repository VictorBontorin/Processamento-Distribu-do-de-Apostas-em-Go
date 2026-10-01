//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestIdempotencyConflicts(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	first := newTx(walletID, playerID, "BET", "10.00")

	if r := postA(t, shared[0], first); r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.raw)
	}

	// Mesma chave, conteúdo diferente.
	changed := first
	changed.amount = "11.00"

	if r := postA(t, shared[1], changed); r.status != http.StatusConflict || r.str("code") != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("same key/different body: %d %s", r.status, r.raw)
	}

	// Mesma operação externa com outra chave.
	otherKey := first
	otherKey.key = providerA + ":another-key"

	if r := postA(t, shared[2], otherKey); r.status != http.StatusConflict {
		t.Fatalf("same external id/other key: %d %s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "90.00" {
		t.Fatalf("balance=%s, want 90.00", got)
	}

	if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
		t.Fatalf("debits=%d, want 1", n)
	}
}

func TestIdempotencyKeyIsRequired(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	req := newTx(walletID, playerID, "BET", "10.00")

	r := do(t, "POST", shared[0].url+"/wagering/transactions", tokProviderA(t), nil, req.body())
	if r.status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}
}

// O replay devolve o saldo observado no processamento original, mesmo com
// outras movimentações depois.
func TestReplayReturnsOriginalBalance(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	bet1 := newTx(walletID, playerID, "BET", "10.00")
	bet2 := newTx(walletID, playerID, "BET", "20.00")

	if r := postA(t, shared[0], bet1); r.str("balance", "amount") != "90.00" {
		t.Fatalf("bet1: %s", r.raw)
	}

	if r := postA(t, shared[1], bet2); r.str("balance", "amount") != "70.00" {
		t.Fatalf("bet2: %s", r.raw)
	}

	again := postA(t, shared[2], bet1)
	if again.status != http.StatusOK || !again.boolean("idempotentReplay") || again.str("balance", "amount") != "90.00" {
		t.Fatalf("replay: %d %s", again.status, again.raw)
	}
}

func TestInvalidMoneyIsRejectedWithoutEffect(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	for _, amount := range []string{"10.001", "-5.00", "1e3", "abc", "", "NaN", "Infinity"} {
		req := newTx(walletID, playerID, "BET", amount)

		r := postA(t, shared[0], req)
		if r.status != http.StatusBadRequest {
			t.Fatalf("amount %q: status=%d body=%s", amount, r.status, r.raw)
		}
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}

	if n := ledgerCount(t, walletID, ""); n != 1 { // só a abertura
		t.Fatalf("ledger entries=%d, want 1", n)
	}
}

func TestOpeningKindIsRejectedOnHTTP(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	r := postA(t, shared[0], newTx(walletID, playerID, "OPENING", "10.00"))
	if r.status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", r.status, r.raw)
	}
}

// LOSS: valor zero, sem lançamento e sem alterar a versão da carteira.
func TestLossHasNoMovement(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	r := postA(t, shared[0], newTx(walletID, playerID, "LOSS", "0.00"))
	if r.status != http.StatusCreated || r.str("status") != "PROCESSED" {
		t.Fatalf("loss: %d %s", r.status, r.raw)
	}

	if n := ledgerCount(t, walletID, ""); n != 1 {
		t.Fatalf("ledger entries=%d, want 1", n)
	}

	w := do(t, "GET", shared[0].url+"/wallets/"+walletID, tokInternal(t), nil, nil)
	if v, _ := w.body["version"].(float64); v != 1 {
		t.Fatalf("version=%v, want 1", w.body["version"])
	}
}

func TestZeroInitialBalanceCreatesNoOpeningOrLedger(t *testing.T) {
	walletID, _ := createWallet(t, "0.00")

	if n := ledgerCount(t, walletID, ""); n != 0 {
		t.Fatalf("ledger entries=%d, want 0", n)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1`, walletID); n != 0 {
		t.Fatalf("transactions=%d, want 0", n)
	}
}

func TestDuplicateWalletForPlayerAndCurrencyConflicts(t *testing.T) {
	_, playerID := createWallet(t, "10.00")

	r := do(t, "POST", shared[1].url+"/wallets", tokInternal(t), nil, map[string]any{
		"playerId":       playerID,
		"initialBalance": money("10.00"),
	})
	if r.status != http.StatusConflict {
		t.Fatalf("status=%d body=%s", r.status, r.raw)
	}
}

func TestUnknownWalletReturnsNotFoundWithoutEffect(t *testing.T) {
	req := newTx(newUUID(), newUUID(), "BET", "10.00")

	r := postA(t, shared[0], req)
	if r.status != http.StatusNotFound || r.str("code") != "WALLET_NOT_FOUND" {
		t.Fatalf("status=%d body=%s", r.status, r.raw)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, req.ext); n != 0 {
		t.Fatalf("transaction created for an unknown wallet")
	}
}
