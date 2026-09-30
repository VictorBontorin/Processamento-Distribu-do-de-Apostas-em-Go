package domain

import (
	"testing"
	"time"
)

func testMoney(t *testing.T, amount string) Money {
	t.Helper()

	money, err := ParseMoney(amount, "BRL")
	if err != nil {
		t.Fatalf("failed to create money: %v", err)
	}

	return money
}

func newTestTransaction(t *testing.T) *WagerTransaction {
	t.Helper()

	tx, err := NewExternalTransaction(
		NewID(),
		"provider-a",
		"transaction-123",
		"idem-123",
		"hash-123",
		NewID(),
		NewID(),
		"round-1",
		"game-1",
		TransactionBet,
		testMoney(t, "100.00"),
		"",
		time.Now(),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return tx
}

func TestWagerTransaction_InitialState(t *testing.T) {
	tx := newTestTransaction(t)

	if tx.State() != StatePending {
		t.Fatalf("expected state %s, got %s", StatePending, tx.State())
	}
}

func TestWagerTransaction_Process(t *testing.T) {
	tx := newTestTransaction(t)

	if err := tx.Process(time.Now()); err != nil {
		t.Fatalf("unexpected error processing transaction: %v", err)
	}

	if tx.State() != StateProcessed {
		t.Fatalf("expected state %s, got %s", StateProcessed, tx.State())
	}
}

func TestWagerTransaction_TerminalStateCannotChange(t *testing.T) {
	tx := newTestTransaction(t)

	if err := tx.Process(time.Now()); err != nil {
		t.Fatalf("unexpected error processing transaction: %v", err)
	}

	if err := tx.Reject(FailureCode("TEST"), time.Now()); err == nil {
		t.Fatal("expected error when changing terminal transaction")
	}
}

func TestWagerTransaction_Reject(t *testing.T) {
	tx := newTestTransaction(t)

	err := tx.Reject(CodeInsufficientFunds, time.Now())
	if err != nil {
		t.Fatalf("unexpected error rejecting transaction: %v", err)
	}

	if tx.State() != StateRejected {
		t.Fatalf("expected state %s, got %s", StateRejected, tx.State())
	}

	if tx.FailureCode() != CodeInsufficientFunds {
		t.Fatalf(
			"expected failure code %s, got %s",
			CodeInsufficientFunds,
			tx.FailureCode(),
		)
	}
}

func TestWagerTransaction_ProcessAfterRejectFails(t *testing.T) {
	tx := newTestTransaction(t)

	if err := tx.Reject(CodeInsufficientFunds, time.Now()); err != nil {
		t.Fatalf("unexpected error rejecting transaction: %v", err)
	}

	if err := tx.Process(time.Now()); err == nil {
		t.Fatal("expected error when processing rejected transaction")
	}
}

func TestWagerTransaction_RejectAfterProcessFails(t *testing.T) {
	tx := newTestTransaction(t)

	if err := tx.Process(time.Now()); err != nil {
		t.Fatalf("unexpected error processing transaction: %v", err)
	}

	if err := tx.Reject(CodeInsufficientFunds, time.Now()); err == nil {
		t.Fatal("expected error when rejecting processed transaction")
	}
}
func TestWagerTransaction_Kinds(t *testing.T) {
	kinds := []TransactionKind{
		TransactionOpening,
		TransactionBet,
		TransactionWin,
		TransactionLoss,
		TransactionRefund,
		TransactionRollback,
	}

	for _, kind := range kinds {
		if kind == "" {
			t.Fatalf("transaction kind cannot be empty")
		}
	}
}
func TestWagerTransaction_ZeroAmountRules(t *testing.T) {
	tests := []struct {
		name string
		kind TransactionKind
	}{
		{
			name: "BET",
			kind: TransactionBet,
		},
		{
			name: "WIN",
			kind: TransactionWin,
		},
		{
			name: "REFUND",
			kind: TransactionRefund,
		},
		{
			name: "ROLLBACK",
			kind: TransactionRollback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewExternalTransaction(
				NewID(),
				"provider-a",
				"transaction-123",
				"idem-123",
				"hash-123",
				NewID(),
				NewID(),
				"round-1",
				"game-1",
				tt.kind,
				testMoney(t, "0.00"),
				"",
				time.Now(),
			)

			if err == nil {
				t.Fatalf("expected zero amount to be rejected for %s", tt.kind)
			}
		})
	}
}
func TestWagerTransaction_LossAcceptsZero(t *testing.T) {
	_, err := NewExternalTransaction(
		NewID(),
		"provider-a",
		"transaction-loss",
		"idem-loss",
		"hash-loss",
		NewID(),
		NewID(),
		"round-1",
		"game-1",
		TransactionLoss,
		testMoney(t, "0.00"),
		"",
		time.Now(),
	)

	if err != nil {
		t.Fatalf("expected LOSS with zero amount to be accepted, got: %v", err)
	}
}
func TestWagerTransaction_ReversalRequiresReference(t *testing.T) {
	tests := []TransactionKind{
		TransactionRefund,
		TransactionRollback,
	}

	for _, kind := range tests {
		t.Run(string(kind), func(t *testing.T) {
			_, err := NewExternalTransaction(
				NewID(),
				"provider-a",
				"transaction-reversal",
				"idem-reversal",
				"hash-reversal",
				NewID(),
				NewID(),
				"round-1",
				"game-1",
				kind,
				testMoney(t, "50.00"),
				"",
				time.Now(),
			)

			if err == nil {
				t.Fatalf("expected %s without reference to be rejected", kind)
			}
		})
	}
}
func TestWagerTransaction_OpeningIsRejectedAsExternal(t *testing.T) {
	_, err := NewExternalTransaction(
		NewID(),
		"provider-a",
		"transaction-opening",
		"idem-opening",
		"hash-opening",
		NewID(),
		NewID(),
		"round-1",
		"game-1",
		TransactionOpening,
		testMoney(t, "100.00"),
		"",
		time.Now(),
	)

	if err == nil {
		t.Fatal("expected OPENING to be rejected as external transaction")
	}
}
func TestWagerTransaction_Rehydrate(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	id := NewID()
	walletID := NewID()
	playerID := NewID()

	tx, err := RehydrateWagerTransaction(
		id,
		"external-123",
		"provider-a",
		"idem-123",
		"hash-123",
		walletID,
		playerID,
		"round-1",
		"game-1",
		TransactionBet,
		testMoney(t, "100.00"),
		"",
		"",
		StateProcessed,
		"",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tx.ID() != id {
		t.Errorf("id: got %s, want %s", tx.ID(), id)
	}

	if tx.ExternalTransactionID() != "external-123" {
		t.Errorf("external id: got %s", tx.ExternalTransactionID())
	}

	if tx.ProviderID() != "provider-a" {
		t.Errorf("provider: got %s", tx.ProviderID())
	}

	if tx.IdempotencyKey() != "idem-123" {
		t.Errorf("idempotency key: got %s", tx.IdempotencyKey())
	}

	if tx.PayloadHash() != "hash-123" {
		t.Errorf("payload hash: got %s", tx.PayloadHash())
	}

	if tx.State() != StateProcessed {
		t.Errorf("state: got %s, want %s", tx.State(), StateProcessed)
	}

	if tx.Money().Amount() != "100.00" {
		t.Errorf("amount: got %s, want 100.00", tx.Money())
	}

	if !tx.CreatedAt().Equal(now) {
		t.Errorf("created at: got %v, want %v", tx.CreatedAt(), now)
	}
}

func TestWagerTransaction_SetReferenceTransactionID(t *testing.T) {
	tx := newTestTransaction(t)

	referenceID := NewID()

	if err := tx.SetReferenceTransactionID(referenceID); err == nil {
		t.Fatal("expected error when setting internal reference without external reference")
	}

	reversal, err := NewExternalTransaction(
		NewID(),
		"refund-123",
		"provider-a",
		"idem-refund",
		"hash-refund",
		NewID(),
		NewID(),
		"round-1",
		"game-1",
		TransactionRefund,
		testMoney(t, "100.00"),
		"bet-123",
		time.Now(),
	)
	if err != nil {
		t.Fatalf("unexpected error creating reversal: %v", err)
	}

	if err := reversal.SetReferenceTransactionID(referenceID); err != nil {
		t.Fatalf("unexpected error setting reference: %v", err)
	}

	if reversal.ReferenceTransactionID() != referenceID {
		t.Errorf(
			"reference transaction id: got %s, want %s",
			reversal.ReferenceTransactionID(),
			referenceID,
		)
	}
}

func TestWagerTransaction_RehydrateOpening(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	tx, err := RehydrateWagerTransaction(
		NewID(),
		"",
		"",
		"",
		"",
		NewID(),
		NewID(),
		"",
		"",
		TransactionOpening,
		testMoney(t, "100.00"),
		"",
		"",
		StateProcessed,
		"",
		now,
		now,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tx.Kind() != TransactionOpening {
		t.Errorf("kind: got %s, want %s", tx.Kind(), TransactionOpening)
	}

	if tx.ExternalTransactionID() != "" {
		t.Error("opening should not have external transaction id")
	}

	if tx.ProviderID() != "" {
		t.Error("opening should not have provider id")
	}

	if tx.IdempotencyKey() != "" {
		t.Error("opening should not have idempotency key")
	}
}

func TestWagerTransaction_RehydrateRejectsInvalidState(t *testing.T) {
	now := time.Now().UTC()

	_, err := RehydrateWagerTransaction(
		NewID(),
		"external-123",
		"provider-a",
		"idem-123",
		"hash-123",
		NewID(),
		NewID(),
		"round-1",
		"game-1",
		TransactionBet,
		testMoney(t, "100.00"),
		"",
		"",
		TransactionState("INVALID"),
		"",
		now,
		now,
	)

	if err == nil {
		t.Fatal("expected invalid state to be rejected")
	}
}
