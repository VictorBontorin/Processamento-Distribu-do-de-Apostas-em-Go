package domain

import (
	"errors"
	"testing"
	"time"
)

func TestOpeningTransaction_InternalOriginAndLifecycle(t *testing.T) {
	money, err := ParseMoney("1000.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()

	opening, err := NewOpeningTransaction(NewID(), NewID(), NewID(), money, now)
	if err != nil {
		t.Fatal(err)
	}

	if opening.Kind() != TransactionOpening || opening.State() != StatePending {
		t.Fatalf("kind=%s state=%s", opening.Kind(), opening.State())
	}

	// Metadados externos não se aplicam à abertura interna.
	if opening.ProviderID() != "" || opening.ExternalTransactionID() != "" ||
		opening.IdempotencyKey() != "" || opening.PayloadHash() != "" ||
		opening.RoundID() != "" || opening.GameID() != "" ||
		opening.ReferenceExternalID() != "" {
		t.Fatal("OPENING must not carry external metadata")
	}

	if err := opening.Process(now); err != nil {
		t.Fatal(err)
	}

	if opening.State() != StateProcessed {
		t.Fatalf("state=%s", opening.State())
	}

	// Estado terminal: nenhuma nova transição.
	if err := opening.Reject("X", now); err == nil {
		t.Fatal("terminal transaction must not change")
	}
}

func TestOpeningTransaction_RequiresIdentityAndRejectsNegative(t *testing.T) {
	money, _ := ParseMoney("10.00", "BRL")
	now := time.Now()

	if _, err := NewOpeningTransaction(ID(""), NewID(), NewID(), money, now); err == nil {
		t.Fatal("zero id must be rejected")
	}

	if _, err := NewOpeningTransaction(NewID(), NewID(), NewID(), Money{}, now); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("uninitialized money: %v", err)
	}

	negative, err := money.Negate()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewOpeningTransaction(NewID(), NewID(), NewID(), negative, now); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative money: %v", err)
	}
}
