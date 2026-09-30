//go:build integration

package integration

import (
	"net/http"
	"sort"
	"sync"
	"testing"
)

// Duas apostas distintas de 80.00 sobre saldo 100.00, disparadas ao mesmo
// tempo contra instâncias diferentes: uma processada, uma rejeitada.
func TestTwoBetsOf80OnBalance100(t *testing.T) {
	tok := tokProviderA(t)

	for round := 0; round < 10; round++ {
		walletID, playerID := createWallet(t, "100.00")

		reqs := []txReq{
			newTx(walletID, playerID, "BET", "80.00"),
			newTx(walletID, playerID, "BET", "80.00"),
		}

		results := make([]resp, len(reqs))
		start := make(chan struct{})

		var wg sync.WaitGroup

		for i := range reqs {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()
				<-start

				results[i] = post(t, shared[(round+i)%len(shared)], tok, reqs[i])
			}(i)
		}

		close(start)
		wg.Wait()

		statuses := []int{results[0].status, results[1].status}
		sort.Ints(statuses)

		if statuses[0] != http.StatusCreated || statuses[1] != http.StatusUnprocessableEntity {
			t.Fatalf("round %d: statuses=%v bodies=%s | %s", round, statuses, results[0].raw, results[1].raw)
		}

		for _, r := range results {
			if r.status == http.StatusUnprocessableEntity && r.str("failureCode") != "INSUFFICIENT_FUNDS" {
				t.Fatalf("round %d: failureCode=%q", round, r.str("failureCode"))
			}
		}

		if got := walletBalance(t, walletID); got != "20.00" {
			t.Fatalf("round %d: balance=%s, want 20.00", round, got)
		}

		if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
			t.Fatalf("round %d: debit entries=%d, want 1", round, n)
		}

		// Reenvios não alteram o resultado.
		for i, r := range reqs {
			again := post(t, shared[(round+i+1)%len(shared)], tok, r)

			if again.status != results[i].status && !(results[i].status == http.StatusCreated && again.status == http.StatusOK) {
				t.Fatalf("round %d: replay status=%d, original=%d", round, again.status, results[i].status)
			}

			if !again.boolean("idempotentReplay") {
				t.Fatalf("round %d: replay not flagged: %s", round, again.raw)
			}
		}

		if got := walletBalance(t, walletID); got != "20.00" {
			t.Fatalf("round %d: balance after replay=%s", round, got)
		}

		if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
			t.Fatalf("round %d: debit entries after replay=%d", round, n)
		}

		assertReconciled(t, walletID)
	}
}

// A mesma aposta enviada 50 vezes em paralelo produz um único débito.
func TestSameBetFiftyTimesInParallel(t *testing.T) {
	walletID, playerID := createWallet(t, "1000.00")
	tok := tokProviderA(t)
	req := newTx(walletID, playerID, "BET", "25.00")

	const total = 50

	results := make([]resp, total)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < total; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start

			results[i] = post(t, shared[i%len(shared)], tok, req)
		}(i)
	}

	close(start)
	wg.Wait()

	created := 0
	transactionID := ""

	for i, r := range results {
		switch r.status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			if !r.boolean("idempotentReplay") {
				t.Fatalf("response %d: 200 without idempotentReplay: %s", i, r.raw)
			}
		default:
			t.Fatalf("response %d: status=%d body=%s", i, r.status, r.raw)
		}

		if transactionID == "" {
			transactionID = r.str("transactionId")
		} else if r.str("transactionId") != transactionID {
			t.Fatalf("response %d: different transactionId %s", i, r.str("transactionId"))
		}

		if got := r.str("balance", "amount"); got != "975.00" {
			t.Fatalf("response %d: balance=%s, want 975.00", i, got)
		}
	}

	if created != 1 {
		t.Fatalf("created=%d, want exactly 1", created)
	}

	if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
		t.Fatalf("debit entries=%d, want 1", n)
	}

	if got := walletBalance(t, walletID); got != "975.00" {
		t.Fatalf("balance=%s, want 975.00", got)
	}

	assertReconciled(t, walletID)
}

// Carteiras independentes avançam em paralelo, sem interferência.
func TestDistinctWalletsProcessedSimultaneously(t *testing.T) {
	const wallets = 30

	tok := tokProviderA(t)

	type job struct {
		walletID string
		req      txReq
	}

	jobs := make([]job, wallets)

	for i := range jobs {
		walletID, playerID := createWallet(t, "100.00")
		jobs[i] = job{walletID, newTx(walletID, playerID, "BET", "10.00")}
	}

	results := make([]resp, wallets)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := range jobs {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start

			results[i] = post(t, shared[i%len(shared)], tok, jobs[i].req)
		}(i)
	}

	close(start)
	wg.Wait()

	for i, j := range jobs {
		if results[i].status != http.StatusCreated {
			t.Fatalf("wallet %d: status=%d body=%s", i, results[i].status, results[i].raw)
		}

		if got := walletBalance(t, j.walletID); got != "90.00" {
			t.Fatalf("wallet %d: balance=%s, want 90.00", i, got)
		}

		assertReconciled(t, j.walletID)
	}
}

// Várias apostas concorrentes na mesma carteira nunca deixam o saldo negativo
// nem perdem atualizações.
func TestManyBetsSameWalletNoLostUpdates(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	tok := tokProviderA(t)

	const attempts = 40 // cada aposta de 5.00: cabem exatamente 20

	results := make([]resp, attempts)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := 0; i < attempts; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			<-start

			results[i] = post(t, shared[i%len(shared)], tok, newTx(walletID, playerID, "BET", "5.00"))
		}(i)
	}

	close(start)
	wg.Wait()

	processed, rejected := 0, 0

	for _, r := range results {
		switch r.status {
		case http.StatusCreated:
			processed++
		case http.StatusUnprocessableEntity:
			rejected++
		default:
			t.Fatalf("unexpected response: %d %s", r.status, r.raw)
		}
	}

	if processed != 20 || rejected != 20 {
		t.Fatalf("processed=%d rejected=%d, want 20/20", processed, rejected)
	}

	if got := walletBalance(t, walletID); got != "0.00" {
		t.Fatalf("balance=%s, want 0.00", got)
	}

	if n := ledgerCount(t, walletID, "DEBIT"); n != 20 {
		t.Fatalf("debits=%d, want 20", n)
	}

	assertReconciled(t, walletID)
}
