//go:build integration

package integration

import (
	"context"
	"testing"
	"time"
)

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func TestOutboxEventsFollowTheContract(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	req := newTx(walletID, playerID, "BET", "25.00")

	created := postA(t, shared[0], req)
	if created.status != 201 {
		t.Fatalf("bet: %d %s", created.status, created.raw)
	}

	txID := created.str("transactionId")

	var processed, balanceChanged map[string]any

	drain(t, env.queues.Events, 60*time.Second, func(seen []map[string]any) bool {
		for _, ev := range seen {
			switch ev["eventType"] {
			case "WagerTransactionProcessed":
				if ev["aggregateId"] == txID {
					processed = ev
				}
			case "WalletBalanceChanged":
				if ev["causationId"] == txID {
					balanceChanged = ev
				}
			}
		}

		return processed != nil && balanceChanged != nil
	})

	if processed == nil || balanceChanged == nil {
		t.Fatalf("events not published: processed=%v balanceChanged=%v", processed != nil, balanceChanged != nil)
	}

	for name, ev := range map[string]map[string]any{"processed": processed, "balanceChanged": balanceChanged} {
		for _, field := range []string{"eventId", "eventType", "aggregateId", "correlationId", "occurredAt", "version", "data"} {
			if v, ok := ev[field]; !ok || v == "" || v == nil {
				t.Fatalf("%s: missing envelope field %q: %v", name, field, ev)
			}
		}
	}

	data := asMap(balanceChanged["data"])

	if data["direction"] != "DEBIT" || data["walletId"] != walletID || data["transactionId"] != txID {
		t.Fatalf("WalletBalanceChanged data: %v", data)
	}

	if asMap(data["balanceBefore"])["amount"] != "100.00" || asMap(data["balanceAfter"])["amount"] != "75.00" {
		t.Fatalf("balances in event: %v", data)
	}

	if asMap(data["money"])["amount"] != "25.00" {
		t.Fatalf("money in event: %v", data["money"])
	}

	if v, _ := data["walletVersion"].(float64); v < 2 {
		t.Fatalf("walletVersion=%v", data["walletVersion"])
	}
}

// Três publishers disputando a mesma outbox: nenhum evento confirmado no
// banco se perde e todos acabam marcados como publicados.
func TestOutboxWithCompetingPublishersLosesNothing(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)

	tok := tokProviderA(t)

	for i := 0; i < 12; i++ {
		walletID, playerID := createWalletOn(t, shared[i%len(shared)], "100.00")

		if r := post(t, shared[(i+1)%len(shared)], tok, newTx(walletID, playerID, "BET", "10.00")); r.status != 201 {
			t.Fatalf("bet: %d %s", r.status, r.raw)
		}
	}

	eventually(t, 60*time.Second, "outbox fully published", func() bool {
		return count(t, env.pool, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})

	want := map[string]bool{}

	rows, err := env.pool.Query(context.Background(),
		`SELECT id::text FROM outbox_events WHERE created_at >= $1`, started)
	if err != nil {
		t.Fatal(err)
	}

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}

		want[id] = true
	}

	rows.Close()

	if len(want) < 12*3 { // abertura(2) + aposta(2) por carteira
		t.Fatalf("outbox rows=%d, expected at least %d", len(want), 12*3)
	}

	got := map[string]bool{}

	drain(t, env.queues.Events, 90*time.Second, func(seen []map[string]any) bool {
		for _, ev := range seen {
			if id, _ := ev["eventId"].(string); id != "" {
				got[id] = true
			}
		}

		for id := range want {
			if !got[id] {
				return false
			}
		}

		return true
	})

	for id := range want {
		if !got[id] {
			t.Fatalf("event %s committed in the database was never published", id)
		}
	}
}

// Interrupção entre a publicação e a confirmação na outbox: outra instância
// assume o evento abandonado e o eventId é preservado.
func TestOutboxCrashBetweenPublishAndMarkIsRecovered(t *testing.T) {
	if testing.Short() {
		t.Skip("depende do lease da outbox (30s)")
	}

	db, pool := newDatabase(t)
	queues := newQueues(env.prefix + "-outbox-" + randHex(2))

	t.Cleanup(func() { deleteQueues(queues) })

	faulty := startTest(t, instanceOpts{
		name:   "faulty-publisher",
		db:     db,
		queues: queues,
		env:    map[string]string{"FAULT_EXIT_AT": "outbox_after_publish"},
	})

	createWalletOn(t, faulty, "100.00")

	if !faulty.waitExit(60 * time.Second) {
		t.Fatal("faulty publisher did not exit after publishing")
	}	

	if n := count(t, pool, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`); n == 0 {
		t.Fatal("expected unpublished events left by the crashed publisher")
	}

	startTest(t, instanceOpts{name: "recovery-publisher", db: db, queues: queues})

	eventually(t, 120*time.Second, "abandoned events taken over", func() bool {
		return count(t, pool, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})

	want := map[string]bool{}

	rows, err := pool.Query(context.Background(), `SELECT id::text FROM outbox_events`)
	if err != nil {
		t.Fatal(err)
	}

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}

		want[id] = true
	}

	rows.Close()

	got := map[string]bool{}

	drain(t, queues.Events, 60*time.Second, func(seen []map[string]any) bool {
		for _, ev := range seen {
			if id, _ := ev["eventId"].(string); id != "" {
				got[id] = true
			}
		}

		return len(got) >= len(want)
	})

	for id := range want {
		if !got[id] {
			t.Fatalf("event %s missing after recovery", id)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("published %d distinct events, outbox has %d", len(got), len(want))
	}
}
