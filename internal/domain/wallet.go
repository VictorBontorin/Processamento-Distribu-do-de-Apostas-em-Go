package domain

import (
	"fmt"
	"time"
)

// Wallet é a raiz do agregado financeiro. Estado privado: só muda por Credit/Debit.
type Wallet struct {
	id        ID
	playerID  ID
	currency  Currency
	balance   Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// NewWallet CRIA uma carteira nova (versão 1). O saldo inicial pode ser 0.00.
// O lançamento de abertura (OPENING) é responsabilidade do caso de uso.
func NewWallet(id, playerID ID, initial Money, now time.Time) (*Wallet, error) {
	if id.IsZero() || playerID.IsZero() || now.IsZero() {
		return nil, fmt.Errorf("%w: missing identity or timestamp", ErrInvalidWallet)
	}
	if !initial.IsValid() {
		return nil, ErrUninitialized
	}
	if initial.IsNegative() {
		return nil, fmt.Errorf("%w: negative initial balance", ErrInvalidWallet)
	}
	now = now.UTC()
	return &Wallet{id, playerID, initial.Currency(), initial, 1, now, now}, nil
}

// RehydrateWallet reconstrói do banco. NÃO aplica movimentações nem emite eventos;
// só confere se os dados lidos respeitam as invariantes.
func RehydrateWallet(id, playerID ID, balance Money, version int64, createdAt, updatedAt time.Time) (*Wallet, error) {
	if id.IsZero() || playerID.IsZero() || createdAt.IsZero() || updatedAt.IsZero() {
		return nil, fmt.Errorf("%w: missing identity or timestamp", ErrInvalidWallet)
	}
	if !balance.IsValid() {
		return nil, ErrUninitialized
	}
	if balance.IsNegative() || version < 1 {
		return nil, fmt.Errorf("%w: balance=%s version=%d", ErrInvalidWallet, balance, version)
	}
	return &Wallet{id, playerID, balance.Currency(), balance, version, createdAt.UTC(), updatedAt.UTC()}, nil
}

func (w *Wallet) ID() ID               { return w.id }
func (w *Wallet) PlayerID() ID         { return w.playerID }
func (w *Wallet) Currency() Currency   { return w.currency }
func (w *Wallet) Balance() Money       { return w.balance }
func (w *Wallet) Version() int64       { return w.version }
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

func (w *Wallet) validateMovement(m Money) error {
	if !m.IsValid() {
		return ErrUninitialized
	}
	if m.Currency() != w.currency {
		return fmt.Errorf("%w: wallet %s, movement %s", ErrCurrencyMismatch, w.currency, m.Currency())
	}
	if !m.IsPositive() {
		return ErrNonPositiveAmount
	}
	return nil
}

// Credit soma m ao saldo e devolve o lançamento correspondente.
// Se qualquer passo falhar, o estado da carteira NÃO é alterado.
func (w *Wallet) Credit(txID ID, m Money, now time.Time) (LedgerEntry, error) {
	if err := w.validateMovement(m); err != nil {
		return LedgerEntry{}, err
	}
	after, err := w.balance.Add(m)
	if err != nil {
		return LedgerEntry{}, err
	}
	return w.apply(txID, DirectionCredit, m, after, now)
}

// Debit subtrai m do saldo. Saldo insuficiente vira RejectionError(INSUFFICIENT_FUNDS).
func (w *Wallet) Debit(txID ID, m Money, now time.Time) (LedgerEntry, error) {
	if err := w.validateMovement(m); err != nil {
		return LedgerEntry{}, err
	}
	after, err := w.balance.Sub(m)
	if err != nil {
		return LedgerEntry{}, err
	}
	if after.IsNegative() {
		return LedgerEntry{}, &RejectionError{Code: CodeInsufficientFunds, Err: ErrInsufficientFunds}
	}
	return w.apply(txID, DirectionDebit, m, after, now)
}

// apply constrói o lançamento ANTES de mutar; se a construção falhar, nada muda.
func (w *Wallet) apply(txID ID, dir Direction, m, after Money, now time.Time) (LedgerEntry, error) {
	entry, err := NewLedgerEntry(NewID(), w.id, txID, dir, m, w.balance, after, now)
	if err != nil {
		return LedgerEntry{}, err
	}
	w.balance = after
	w.version++
	w.updatedAt = now.UTC()
	return entry, nil
}
