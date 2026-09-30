//go:build integration

package integration

import (
	"net/http"
	"sync"
	"testing"
)

func reversal(walletID, playerID, kind, amount string, ref txReq) txReq {
	r := newTx(walletID, playerID, kind, amount)
	r.ref = ref.ext
	r.round = ref.round

	return r
}

// REFUND chega antes da BET: fica pendente, sobrevive e é resolvido depois.
func TestRefundBeforeBetIsResolvedLater(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	bet := newTx(walletID, playerID, "BET", "30.00")
	refund := reversal(walletID, playerID, "REFUND", "30.00", bet)

	r := postA(t, shared[0], refund)
	if r.status != http.StatusAccepted || r.str("status") != "PENDING_REFERENCE" {
		t.Fatalf("refund before bet: %d %s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance while pending=%s", got)
	}

	if r := postA(t, shared[1], bet); r.status != http.StatusCreated {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	waitTxStatus(t, shared[2], providerA, refund.ext, "PROCESSED")

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance after refund=%s, want 100.00", got)
	}

	// abertura + débito + crédito
	if n := ledgerCount(t, walletID, ""); n != 3 {
		t.Fatalf("ledger entries=%d, want 3", n)
	}

	// Replay depois de resolvido devolve o resultado persistido.
	again := postA(t, shared[2], refund)
	if again.status != http.StatusOK || !again.boolean("idempotentReplay") || again.str("status") != "PROCESSED" {
		t.Fatalf("replay: %d %s", again.status, again.raw)
	}

	assertReconciled(t, walletID)
}

func TestSecondReversalOfSameBetIsRejected(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	bet := newTx(walletID, playerID, "BET", "30.00")
	if r := postA(t, shared[0], bet); r.status != http.StatusCreated {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	if r := postA(t, shared[1], reversal(walletID, playerID, "REFUND", "30.00", bet)); r.status != http.StatusCreated {
		t.Fatalf("refund: %d %s", r.status, r.raw)
	}

	for _, kind := range []string{"REFUND", "ROLLBACK"} {
		r := postA(t, shared[2], reversal(walletID, playerID, kind, "30.00", bet))
		if r.status != http.StatusUnprocessableEntity || r.str("failureCode") != "DUPLICATE_REVERSAL" {
			t.Fatalf("second %s: %d %s", kind, r.status, r.raw)
		}
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s, want 100.00", got)
	}

	if n := ledgerCount(t, walletID, "CREDIT"); n != 2 { // abertura + um estorno
		t.Fatalf("credits=%d, want 2", n)
	}

	assertReconciled(t, walletID)
}

func TestConcurrentReversalsOfSameBet(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	tok := tokProviderA(t)

	bet := newTx(walletID, playerID, "BET", "30.00")
	if r := post(t, shared[0], tok, bet); r.status != http.StatusCreated {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	const attempts = 8

	results := make([]resp, attempts)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < attempts; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start

			kind := "REFUND"
			if i%2 == 1 {
				kind = "ROLLBACK"
			}

			results[i] = post(t, shared[i%len(shared)], tok, reversal(walletID, playerID, kind, "30.00", bet))
		}(i)
	}

	close(start)
	wg.Wait()

	processed := 0

	for _, r := range results {
		switch {
		case r.status == http.StatusCreated:
			processed++
		case r.status == http.StatusUnprocessableEntity && r.str("failureCode") == "DUPLICATE_REVERSAL":
		default:
			t.Fatalf("unexpected: %d %s", r.status, r.raw)
		}
	}

	if processed != 1 {
		t.Fatalf("processed reversals=%d, want 1", processed)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s, want 100.00", got)
	}

	assertReconciled(t, walletID)
}

func TestRollbackOfWinAndOfRefund(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	bet := newTx(walletID, playerID, "BET", "40.00") // 60
	win := newTx(walletID, playerID, "WIN", "50.00") // 110

	for _, r := range []txReq{bet, win} {
		if res := postA(t, shared[0], r); res.status != http.StatusCreated {
			t.Fatalf("%s: %d %s", r.kind, res.status, res.raw)
		}
	}

	if r := postA(t, shared[1], reversal(walletID, playerID, "ROLLBACK", "50.00", win)); r.status != http.StatusCreated || r.str("balance", "amount") != "60.00" {
		t.Fatalf("rollback of win: %d %s", r.status, r.raw)
	}

	bet2 := newTx(walletID, playerID, "BET", "20.00") // 40
	refund := reversal(walletID, playerID, "REFUND", "20.00", bet2)

	if r := postA(t, shared[0], bet2); r.status != http.StatusCreated {
		t.Fatalf("bet2: %d %s", r.status, r.raw)
	}

	if r := postA(t, shared[1], refund); r.status != http.StatusCreated || r.str("balance", "amount") != "60.00" {
		t.Fatalf("refund: %d %s", r.status, r.raw)
	}

	if r := postA(t, shared[2], reversal(walletID, playerID, "ROLLBACK", "20.00", refund)); r.status != http.StatusCreated || r.str("balance", "amount") != "40.00" {
		t.Fatalf("rollback of refund: %d %s", r.status, r.raw)
	}

	assertReconciled(t, walletID)
}

func TestReversalWithoutBalanceUsesDistinctFailureCode(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	steps := []struct {
		req  txReq
		want string
	}{
		{newTx(walletID, playerID, "BET", "100.00"), "0.00"},
		{newTx(walletID, playerID, "WIN", "50.00"), "50.00"},
	}

	for _, s := range steps {
		if r := postA(t, shared[0], s.req); r.status != http.StatusCreated || r.str("balance", "amount") != s.want {
			t.Fatalf("%s: %d %s", s.req.kind, r.status, r.raw)
		}
	}

	win := steps[1].req

	if r := postA(t, shared[0], newTx(walletID, playerID, "BET", "50.00")); r.status != http.StatusCreated {
		t.Fatalf("drain balance: %d %s", r.status, r.raw)
	}

	rollback := postA(t, shared[1], reversal(walletID, playerID, "ROLLBACK", "50.00", win))
	if rollback.status != http.StatusUnprocessableEntity || rollback.str("failureCode") != "REVERSAL_INSUFFICIENT_FUNDS" {
		t.Fatalf("rollback: %d %s", rollback.status, rollback.raw)
	}

	bet := postA(t, shared[1], newTx(walletID, playerID, "BET", "10.00"))
	if bet.status != http.StatusUnprocessableEntity || bet.str("failureCode") != "INSUFFICIENT_FUNDS" {
		t.Fatalf("bet: %d %s", bet.status, bet.raw)
	}

	if got := walletBalance(t, walletID); got != "0.00" {
		t.Fatalf("balance=%s", got)
	}

	// A rejeição é auditável: fica registrada na transação.
	if n := count(t, env.pool, `SELECT count(*) FROM wager_transactions WHERE wallet_id = $1 AND state = 'REJECTED'`, walletID); n != 2 {
		t.Fatalf("rejected transactions=%d, want 2", n)
	}

	assertReconciled(t, walletID)
}

func TestReversalMustMatchReferencedAmount(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	bet := newTx(walletID, playerID, "BET", "30.00")
	if r := postA(t, shared[0], bet); r.status != http.StatusCreated {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	r := postA(t, shared[1], reversal(walletID, playerID, "REFUND", "20.00", bet))
	if r.status != http.StatusUnprocessableEntity || r.str("failureCode") != "INVALID_REVERSAL" {
		t.Fatalf("partial refund: %d %s", r.status, r.raw)
	}

	if got := walletBalance(t, walletID); got != "70.00" {
		t.Fatalf("balance=%s", got)
	}
}

// Referência que nunca chega: rejeição por expiração (TTL curto).
func TestPendingReferenceExpires(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	inst := startTest(t, instanceOpts{
		name:   "short-ttl",
		db:     env.dbName,
		queues: env.queues,
		env:    map[string]string{"PENDING_REFERENCE_TTL": "3s"},
	})

	ghost := newTx(walletID, playerID, "BET", "10.00") // nunca enviada
	refund := reversal(walletID, playerID, "REFUND", "10.00", ghost)

	r := postA(t, inst, refund)
	if r.status != http.StatusAccepted {
		t.Fatalf("refund: %d %s", r.status, r.raw)
	}

	final := waitTxStatus(t, inst, providerA, refund.ext, "REJECTED")
	if final.str("failureCode") != "REFERENCE_NOT_FOUND" {
		t.Fatalf("failureCode=%q body=%s", final.str("failureCode"), final.raw)
	}

	if n := count(t, env.pool,
		`SELECT count(*) FROM outbox_events WHERE event_type = 'WagerTransactionRejected' AND aggregate_id = $1`,
		r.str("transactionId")); n != 1 {
		t.Fatalf("rejection events=%d, want 1", n)
	}

	if got := walletBalance(t, walletID); got != "100.00" {
		t.Fatalf("balance=%s", got)
	}
}
