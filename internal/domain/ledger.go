package domain

import (
	"fmt"
	"time"
)

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

// LedgerEntry é um lançamento IMUTÁVEL (sem setters).
type LedgerEntry struct {
	id            ID
	walletID      ID
	transactionID ID
	direction     Direction
	amount        Money
	balanceBefore Money
	balanceAfter  Money
	createdAt     time.Time
}

// NewLedgerEntry cria um lançamento validando balanceAfter = balanceBefore ± amount.
func NewLedgerEntry(id, walletID, txID ID, dir Direction, amount, before, after Money, now time.Time) (LedgerEntry, error) {
	if id.IsZero() || walletID.IsZero() || txID.IsZero() || now.IsZero() {
		return LedgerEntry{}, fmt.Errorf("%w: missing identity or timestamp", ErrInvalidLedger)
	}
	if !amount.IsPositive() {
		return LedgerEntry{}, fmt.Errorf("%w: %v", ErrNonPositiveAmount, amount)
	}
	if before.IsNegative() || after.IsNegative() {
		return LedgerEntry{}, fmt.Errorf("%w: negative balance", ErrInvalidLedger)
	}
	var expected Money
	var err error
	switch dir {
	case DirectionCredit:
		expected, err = before.Add(amount)
	case DirectionDebit:
		expected, err = before.Sub(amount)
	default:
		return LedgerEntry{}, fmt.Errorf("%w: direction %q", ErrInvalidLedger, dir)
	}
	if err != nil {
		return LedgerEntry{}, err
	}
	cmp, err := expected.Cmp(after)
	if err != nil {
		return LedgerEntry{}, err
	}
	if cmp != 0 {
		return LedgerEntry{}, fmt.Errorf("%w: balanceAfter %s != expected %s", ErrInvalidLedger, after, expected)
	}
	return LedgerEntry{id, walletID, txID, dir, amount, before, after, now.UTC()}, nil
}

// RehydrateLedgerEntry reconstrói um lançamento lido do banco. Não gera nada novo;
// reaproveita a validação só como checagem de integridade dos dados lidos.
func RehydrateLedgerEntry(id, walletID, txID ID, dir Direction, amount, before, after Money, createdAt time.Time) (LedgerEntry, error) {
	return NewLedgerEntry(id, walletID, txID, dir, amount, before, after, createdAt)
}

func (e LedgerEntry) ID() ID               { return e.id }
func (e LedgerEntry) WalletID() ID         { return e.walletID }
func (e LedgerEntry) TransactionID() ID    { return e.transactionID }
func (e LedgerEntry) Direction() Direction { return e.direction }
func (e LedgerEntry) Amount() Money        { return e.amount }
func (e LedgerEntry) BalanceBefore() Money { return e.balanceBefore }
func (e LedgerEntry) BalanceAfter() Money  { return e.balanceAfter }
func (e LedgerEntry) CreatedAt() time.Time { return e.createdAt }
