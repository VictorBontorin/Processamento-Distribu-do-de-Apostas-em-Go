//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"
)

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	base := shared[0].url

	req := newTx(walletID, playerID, "BET", "10.00")

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"post transaction", "POST", "/wagering/transactions", req.body()},
		{"create wallet", "POST", "/wallets", map[string]any{"playerId": newUUID(), "initialBalance": money("1.00")}},
		{"get wallet", "GET", "/wallets/" + walletID, nil},
		{"ledger", "GET", "/wallets/" + walletID + "/ledger", nil},
		{"reconciliation", "POST", "/wallets/" + walletID + "/reconciliation", nil},
		{"get transaction", "GET", "/wagering/transactions/" + newUUID(), nil},
		{"provider transaction", "GET", "/providers/provider-a/wagering/transactions/x", nil},
	}

	for _, tc := range cases {
		for name, tok := range map[string]string{"no token": "", "invalid token": "not-a-jwt"} {
			r := do(t, tc.method, base+tc.path, tok, map[string]string{"Idempotency-Key": req.key}, tc.body)
			if r.status != http.StatusUnauthorized {
				t.Fatalf("%s / %s: status=%d body=%s", tc.name, name, r.status, r.raw)
			}

			if _, leaked := r.body["balance"]; leaked {
				t.Fatalf("%s: unauthenticated response exposes data: %s", tc.name, r.raw)
			}
		}
	}

	// Nenhum efeito financeiro.
	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1 AND kind = 'BET'`, walletID); n != 0 {
		t.Fatalf("transactions created without authentication: %d", n)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	tok := fetchToken(t, "provider-expiring", "provider-expiring-secret")

	req := newTx(walletID, playerID, "BET", "10.00")

	// Enquanto válido, o token funciona.
	if r := post(t, shared[0], tok, newTx(walletID, playerID, "BET", "1.00")); r.status != http.StatusCreated {
		t.Fatalf("fresh token: %d %s", r.status, r.raw)
	}

	time.Sleep(5 * time.Second) // lifespan do cliente: 3s

	if r := post(t, shared[0], tok, req); r.status != http.StatusUnauthorized {
		t.Fatalf("expired token: %d %s", r.status, r.raw)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, req.ext); n != 0 {
		t.Fatalf("expired token produced a transaction")
	}
}

func TestRolesAreEnforced(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	base := shared[1].url
	provider := tokProviderA(t)
	internal := tokInternal(t)

	// Provedor não acessa operações de carteira.
	newPlayer := newUUID()

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/wallets", map[string]any{"playerId": newPlayer, "initialBalance": money("50.00")}},
		{"GET", "/wallets/" + walletID, nil},
		{"GET", "/wallets/" + walletID + "/ledger", nil},
		{"POST", "/wallets/" + walletID + "/reconciliation", nil},
	} {
		r := do(t, tc.method, base+tc.path, provider, nil, tc.body)
		if r.status != http.StatusForbidden {
			t.Fatalf("provider on %s %s: %d %s", tc.method, tc.path, r.status, r.raw)
		}
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wallets WHERE player_id = $1`, newPlayer); n != 0 {
		t.Fatalf("provider created a wallet")
	}

	// Serviço interno não envia operações de aposta.
	req := newTx(walletID, playerID, "BET", "10.00")

	if r := post(t, shared[1], internal, req); r.status != http.StatusForbidden {
		t.Fatalf("internal on POST transaction: %d %s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE external_transaction_id = $1`, req.ext); n != 0 {
		t.Fatalf("forbidden request produced a transaction")
	}
}

func TestProviderCannotActAsAnotherProvider(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	// provider-b tentando operar em nome de provider-a.
	req := newTx(walletID, playerID, "BET", "10.00") // providerId = provider-a

	r := post(t, shared[0], tokProviderB(t), req)
	if r.status != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}

	if n := ledgerCount(t, walletID, "DEBIT"); n != 0 {
		t.Fatalf("debits=%d", n)
	}
}

// Provedores enxergam só as próprias transações, inclusive em consultas e
// replays; o espaço de idempotência é por provedor.
func TestProviderIsolationOnQueriesAndReplays(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	req := newTx(walletID, playerID, "BET", "10.00")

	created := postA(t, shared[0], req)
	if created.status != http.StatusCreated {
		t.Fatalf("create: %d %s", created.status, created.raw)
	}

	txID := created.str("transactionId")
	a, b, internal := tokProviderA(t), tokProviderB(t), tokInternal(t)
	base := shared[2].url

	if r := do(t, "GET", base+"/wagering/transactions/"+txID, a, nil, nil); r.status != http.StatusOK {
		t.Fatalf("owner by id: %d %s", r.status, r.raw)
	}

	if r := do(t, "GET", base+"/wagering/transactions/"+txID, internal, nil, nil); r.status != http.StatusOK {
		t.Fatalf("internal by id: %d %s", r.status, r.raw)
	}

	// Outro provedor: 404 (não revela a existência) e sem dados.
	r := do(t, "GET", base+"/wagering/transactions/"+txID, b, nil, nil)
	if r.status != http.StatusNotFound || r.str("balance", "amount") != "" {
		t.Fatalf("other provider by id: %d %s", r.status, r.raw)
	}

	// Consulta pelo caminho de outro provedor: 403.
	if r := providerTx(t, shared[2], b, providerA, req.ext); r.status != http.StatusForbidden {
		t.Fatalf("other provider by path: %d %s", r.status, r.raw)
	}

	if r := providerTx(t, shared[2], a, providerA, req.ext); r.status != http.StatusOK {
		t.Fatalf("owner by path: %d %s", r.status, r.raw)
	}

	// provider-b enviando a mesma chave/id externo no próprio espaço cria
	// outra transação: não é replay da de provider-a e não vaza seu resultado.
	mine := req
	mine.provider = providerB
	mine.key = providerA + ":" + req.ext // mesma chave literal

	other := post(t, shared[0], b, mine)
	if other.status != http.StatusCreated || other.boolean("idempotentReplay") || other.str("transactionId") == txID {
		t.Fatalf("provider-b request collided with provider-a: %d %s", other.status, other.raw)
	}
}
