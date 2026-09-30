package domain

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func newTestWallet(t *testing.T, initial string) *Wallet {
	t.Helper()
	w, err := NewWallet(NewID(), NewID(), mustMoney(t, initial, "BRL"), t0)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestNewWallet(t *testing.T) {
	w := newTestWallet(t, "1000.00")
	if w.Version() != 1 || w.Balance().Amount() != "1000.00" {
		t.Errorf("v=%d bal=%s", w.Version(), w.Balance())
	}
	if _, err := NewWallet("", NewID(), mustMoney(t, "1.00", "BRL"), t0); !errors.Is(err, ErrInvalidWallet) {
		t.Errorf("empty id: %v", err)
	}
	if _, err := NewWallet(NewID(), NewID(), Money{}, t0); !errors.Is(err, ErrUninitialized) {
		t.Errorf("zero money: %v", err)
	}
	neg, _ := FromUnits(-1, "BRL")
	if _, err := NewWallet(NewID(), NewID(), neg, t0); !errors.Is(err, ErrInvalidWallet) {
		t.Errorf("negative: %v", err)
	}
	zero := newTestWallet(t, "0.00") // saldo inicial zero é permitido
	if zero.Version() != 1 {
		t.Errorf("v=%d", zero.Version())
	}
}

func TestWallet_DebitCredit(t *testing.T) {
	w := newTestWallet(t, "100.00")
	tx := NewID()
	e, err := w.Debit(tx, mustMoney(t, "80.00", "BRL"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().Amount() != "20.00" || w.Version() != 2 {
		t.Errorf("bal=%s v=%d", w.Balance(), w.Version())
	}
	if e.Direction() != DirectionDebit || e.BalanceBefore().Amount() != "100.00" || e.BalanceAfter().Amount() != "20.00" || e.TransactionID() != tx {
		t.Errorf("bad entry %+v", e)
	}
	e2, err := w.Credit(NewID(), mustMoney(t, "5.50", "BRL"), t0)
	if err != nil || w.Balance().Amount() != "25.50" || w.Version() != 3 || e2.Direction() != DirectionCredit {
		t.Errorf("credit: %v bal=%s v=%d", err, w.Balance(), w.Version())
	}
}

func TestWallet_InsufficientFundsKeepsState(t *testing.T) {
	w := newTestWallet(t, "100.00")
	_, err := w.Debit(NewID(), mustMoney(t, "100.01", "BRL"), t0)
	var rej *RejectionError
	if !errors.As(err, &rej) || rej.Code != CodeInsufficientFunds || !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("got %v", err)
	}
	if w.Balance().Amount() != "100.00" || w.Version() != 1 {
		t.Errorf("state changed: %s v=%d", w.Balance(), w.Version())
	}
	// debitar exatamente o saldo é permitido (saldo fica 0.00)
	if _, err := w.Debit(NewID(), mustMoney(t, "100.00", "BRL"), t0); err != nil || !w.Balance().IsZero() {
		t.Errorf("exact debit: %v %s", err, w.Balance())
	}
}

func TestWallet_InvalidMovements(t *testing.T) {
	w := newTestWallet(t, "100.00")
	zero := mustMoney(t, "0.00", "BRL")
	if _, err := w.Debit(NewID(), zero, t0); !errors.Is(err, ErrNonPositiveAmount) {
		t.Errorf("zero debit: %v", err)
	}
	if _, err := w.Credit(NewID(), mustMoney(t, "1.00", "USD"), t0); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("currency: %v", err)
	}
	if _, err := w.Credit(NewID(), Money{}, t0); !errors.Is(err, ErrUninitialized) {
		t.Errorf("uninit: %v", err)
	}
	if _, err := w.Credit("", mustMoney(t, "1.00", "BRL"), t0); !errors.Is(err, ErrInvalidLedger) {
		t.Errorf("empty tx id: %v", err)
	}
	if w.Version() != 1 || w.Balance().Amount() != "100.00" {
		t.Error("failed operations must not change state")
	}
}

func TestRehydrateWallet_NoSideEffects(t *testing.T) {
	w, err := RehydrateWallet(NewID(), NewID(), mustMoney(t, "20.00", "BRL"), 7, t0, t0.Add(time.Hour))
	if err != nil || w.Version() != 7 || w.Balance().Amount() != "20.00" {
		t.Fatalf("%v", err)
	}
	if _, err := RehydrateWallet(NewID(), NewID(), mustMoney(t, "1.00", "BRL"), 0, t0, t0); !errors.Is(err, ErrInvalidWallet) {
		t.Errorf("version 0: %v", err)
	}
}

func TestNewLedgerEntry_ValidatesArithmetic(t *testing.T) {
	m := func(s string) Money { return mustMoney(t, s, "BRL") }
	ok, err := NewLedgerEntry(NewID(), NewID(), NewID(), DirectionCredit, m("10.00"), m("5.00"), m("15.00"), t0)
	if err != nil || ok.BalanceAfter().Amount() != "15.00" {
		t.Fatal(err)
	}
	if _, err := NewLedgerEntry(NewID(), NewID(), NewID(), DirectionCredit, m("10.00"), m("5.00"), m("16.00"), t0); !errors.Is(err, ErrInvalidLedger) {
		t.Errorf("wrong credit: %v", err)
	}
	if _, err := NewLedgerEntry(NewID(), NewID(), NewID(), DirectionDebit, m("10.00"), m("5.00"), m("0.00"), t0); err == nil {
		t.Error("debit below zero must fail")
	}
	if _, err := NewLedgerEntry(NewID(), NewID(), NewID(), "SIDEWAYS", m("1.00"), m("5.00"), m("6.00"), t0); !errors.Is(err, ErrInvalidLedger) {
		t.Errorf("direction: %v", err)
	}
}

func TestIDs(t *testing.T) {
	id := NewID()
	back, err := ParseID(id.String())
	if err != nil || back != id {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{"", "abc", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a", "zzzzzzzz-5dc0-7d58-bdb2-814ad6a0f4a1"} {
		if _, err := ParseID(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if id.String()[14] != '7' {
		t.Errorf("not v7: %s", id)
	}
}
