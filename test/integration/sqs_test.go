//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"
)

func inboxCompleted(t *testing.T, messageID string) int {
	t.Helper()

	return count(t, env.pool,
		`SELECT count(*) FROM inbox_messages WHERE message_id = $1 AND completed_at IS NOT NULL`,
		messageID)
}

func TestSQSProcessesBet(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	req := newTx(walletID, playerID, "BET", "25.00")
	msgID := "msg-" + randHex(6)

	sendSQS(t, env.queues.Tx, sqsMessage(msgID, req), walletID, msgID)

	eventually(t, 60*time.Second, "bet processed through SQS", func() bool {
		return walletBalance(t, walletID) == "75.00"
	})

	waitTxStatus(t, shared[0], providerA, req.ext, "PROCESSED")

	if inboxCompleted(t, msgID) != 1 {
		t.Fatalf("inbox not completed for %s", msgID)
	}

	assertReconciled(t, walletID)
}

// A mesma mensagem (mesmo messageId) recebida várias vezes: a deduplicação é
// feita pela aplicação (inbox), pois cada envio usa um MessageDeduplicationId
// diferente e o SQS entrega todas as cópias.
func TestSQSRedeliveredMessageIsDeduplicatedByInbox(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")
	req := newTx(walletID, playerID, "BET", "25.00")
	msgID := "msg-" + randHex(6)

	for i := 0; i < 4; i++ {
		sendSQS(t, env.queues.Tx, sqsMessage(msgID, req), walletID, msgID+"-copy-"+randHex(3))
	}

	eventually(t, 60*time.Second, "message processed", func() bool {
		return walletBalance(t, walletID) == "75.00"
	})

	// Dá tempo de as demais cópias serem recebidas e descartadas.
	time.Sleep(8 * time.Second)

	if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
		t.Fatalf("debits=%d, want 1", n)
	}

	if n := count(t, env.pool, `SELECT count(*) FROM inbox_messages WHERE message_id = $1`, msgID); n != 1 {
		t.Fatalf("inbox rows=%d, want 1", n)
	}

	assertReconciled(t, walletID)
}

// A mesma operação por HTTP e por SQS (mensagens distintas, mesma chave de
// idempotência) gera um único movimento, em qualquer ordem.
func TestSameOperationOverHTTPAndSQS(t *testing.T) {
	t.Run("HTTP then SQS", func(t *testing.T) {
		walletID, playerID := createWallet(t, "100.00")
		req := newTx(walletID, playerID, "BET", "25.00")

		if r := postA(t, shared[0], req); r.status != http.StatusCreated {
			t.Fatalf("http: %d %s", r.status, r.raw)
		}

		msgID := "msg-" + randHex(6)
		sendSQS(t, env.queues.Tx, sqsMessage(msgID, req), walletID, msgID)

		eventually(t, 60*time.Second, "SQS message consumed", func() bool {
			return inboxCompleted(t, msgID) == 1
		})

		if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
			t.Fatalf("debits=%d, want 1", n)
		}

		if got := walletBalance(t, walletID); got != "75.00" {
			t.Fatalf("balance=%s", got)
		}

		assertReconciled(t, walletID)
	})

	t.Run("SQS then HTTP", func(t *testing.T) {
		walletID, playerID := createWallet(t, "100.00")
		req := newTx(walletID, playerID, "BET", "25.00")
		msgID := "msg-" + randHex(6)

		sendSQS(t, env.queues.Tx, sqsMessage(msgID, req), walletID, msgID)
		waitTxStatus(t, shared[0], providerA, req.ext, "PROCESSED")

		r := postA(t, shared[1], req)
		if r.status != http.StatusOK || !r.boolean("idempotentReplay") || r.str("balance", "amount") != "75.00" {
			t.Fatalf("http replay after SQS: %d %s", r.status, r.raw)
		}

		if n := ledgerCount(t, walletID, "DEBIT"); n != 1 {
			t.Fatalf("debits=%d, want 1", n)
		}

		assertReconciled(t, walletID)
	})
}

// Rejeição de negócio confirmada é terminal: a mensagem é removida da fila.
func TestSQSBusinessRejectionIsTerminal(t *testing.T) {
	walletID, playerID := createWallet(t, "10.00")
	req := newTx(walletID, playerID, "BET", "50.00")
	msgID := "msg-" + randHex(6)

	sendSQS(t, env.queues.Tx, sqsMessage(msgID, req), walletID, msgID)

	final := waitTxStatus(t, shared[0], providerA, req.ext, "REJECTED")
	if final.str("failureCode") != "INSUFFICIENT_FUNDS" {
		t.Fatalf("failureCode=%q", final.str("failureCode"))
	}

	eventually(t, 30*time.Second, "inbox completed", func() bool {
		return inboxCompleted(t, msgID) == 1
	})

	if got := walletBalance(t, walletID); got != "10.00" {
		t.Fatalf("balance=%s", got)
	}

	if n := ledgerCount(t, walletID, "DEBIT"); n != 0 {
		t.Fatalf("debits=%d", n)
	}
}

// Mensagens inválidas e conflitos de conteúdo vão direto para a DLQ.
func TestSQSPermanentFailuresGoToDLQ(t *testing.T) {
	walletID, playerID := createWallet(t, "100.00")

	garbage := "garbage-" + randHex(6)
	sendRaw(t, env.queues.Tx, `{"not":"an envelope","tag":"`+garbage+`"}`, "invalid-group", garbage)

	// Mesma chave de idempotência, conteúdo diferente: conflito permanente.
	ok := newTx(walletID, playerID, "BET", "10.00")

	if r := postA(t, shared[0], ok); r.status != http.StatusCreated {
		t.Fatalf("bet: %d %s", r.status, r.raw)
	}

	conflict := ok
	conflict.amount = "11.00"
	conflictMsg := "msg-conflict-" + randHex(6)

	sendSQS(t, env.queues.Tx, sqsMessage(conflictMsg, conflict), walletID, conflictMsg)

	foundGarbage, foundConflict := false, false

	drain(t, env.queues.DLQ, 60*time.Second, func(seen []map[string]any) bool {
		for _, body := range seen {
			if tag, _ := body["tag"].(string); tag == garbage {
				foundGarbage = true
			}

			if id, _ := body["messageId"].(string); id == conflictMsg {
				foundConflict = true
			}
		}

		return foundGarbage && foundConflict
	})

	if !foundGarbage || !foundConflict {
		t.Fatalf("DLQ garbage=%v conflict=%v", foundGarbage, foundConflict)
	}

	// O conflito não teve efeito financeiro.
	if got := walletBalance(t, walletID); got != "90.00" {
		t.Fatalf("balance=%s, want 90.00", got)
	}
}

// Encerramento abrupto depois do commit e antes de remover a mensagem: outra
// instância recebe a reentrega, a inbox descarta a duplicata e a fila esvazia.
func TestSQSCrashAfterCommitIsRecovered(t *testing.T) {
	if testing.Short() {
		t.Skip("depende do visibility timeout (30s)")
	}

	db, pool := newDatabase(t)
	queues := newQueues(env.prefix + "-crash-" + randHex(2))

	t.Cleanup(func() { deleteQueues(queues) })

	faulty := startTest(t, instanceOpts{
		name:   "faulty-consumer",
		db:     db,
		queues: queues,
		env:    map[string]string{"FAULT_EXIT_AT": "sqs_after_commit"},
	})

	walletID, playerID := createWalletOn(t, faulty, "100.00")
	req := newTx(walletID, playerID, "BET", "25.00")
	msgID := "msg-" + randHex(6)

	sendSQS(t, queues.Tx, sqsMessage(msgID, req), walletID, msgID)

	if !faulty.waitExit(60 * time.Second) {
		t.Fatal("faulty consumer did not exit after commit")
	}

	// O commit aconteceu: débito único e inbox concluída.
	if n := count(t, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND direction = 'DEBIT'`, walletID); n != 1 {
		t.Fatalf("debits after crash=%d, want 1", n)
	}

	if n := count(t, pool, `SELECT count(*) FROM inbox_messages WHERE message_id = $1 AND completed_at IS NOT NULL`, msgID); n != 1 {
		t.Fatalf("inbox rows after crash=%d, want 1", n)
	}

	recovery := startTest(t, instanceOpts{name: "recovery-consumer", db: db, queues: queues})

	// A mensagem volta após o visibility timeout e é removida sem nova aplicação.
	eventually(t, 120*time.Second, "queue drained after redelivery", func() bool {
		return queueDepth(t, queues.Tx) == 0
	})

	if n := count(t, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND direction = 'DEBIT'`, walletID); n != 1 {
		t.Fatalf("debits after redelivery=%d, want 1", n)
	}

	if got := walletBalanceOn(t, recovery, walletID); got != "75.00" {
		t.Fatalf("balance=%s, want 75.00", got)
	}

	assertReconciledIn(t, pool, recovery, walletID)
}
