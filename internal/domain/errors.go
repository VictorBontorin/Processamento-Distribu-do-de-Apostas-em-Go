// Package domain contém as regras de negócio puras. Só importa a stdlib.
package domain

import "errors"

// Erros de Money e de valores inválidos.
var (
	ErrInvalidAmount      = errors.New("invalid amount")
	ErrNegativeAmount     = errors.New("negative amount not allowed")
	ErrScaleExceeded      = errors.New("amount has more than 2 decimal places")
	ErrOverflow           = errors.New("monetary overflow")
	ErrInvalidCurrency    = errors.New("invalid currency")
	ErrCurrencyMismatch   = errors.New("currency mismatch")
	ErrUninitialized      = errors.New("uninitialized domain value")
	ErrNonPositiveAmount  = errors.New("amount must be greater than zero")
	ErrInvalidTransaction = errors.New("invalid wager transaction")
)

// Erros da carteira e do ledger.
var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidID         = errors.New("invalid id")
	ErrInvalidWallet     = errors.New("invalid wallet state")
	ErrInvalidLedger     = errors.New("invalid ledger entry")
)

// FailureCode é o código estável e documentado de uma rejeição de negócio.
type FailureCode string

const (
	CodeInsufficientFunds         FailureCode = "INSUFFICIENT_FUNDS"
	CodeReversalInsufficientFunds FailureCode = "REVERSAL_INSUFFICIENT_FUNDS"
	CodeReferenceNotFound         FailureCode = "REFERENCE_NOT_FOUND"
	CodeReferenceNotProcessed     FailureCode = "REFERENCE_NOT_PROCESSED"
	CodeDuplicateReversal         FailureCode = "DUPLICATE_REVERSAL"
	CodeIdempotencyConflict       FailureCode = "IDEMPOTENCY_CONFLICT"
	CodeInvalidReversal FailureCode = "INVALID_REVERSAL"
)

// RejectionError representa uma rejeição de negócio classificável.
// Use errors.As(err, &rej) para ler o Code e errors.Is para o erro embrulhado.
type RejectionError struct {
	Code FailureCode
	Err  error
}

func (e *RejectionError) Error() string { return string(e.Code) + ": " + e.Err.Error() }
func (e *RejectionError) Unwrap() error { return e.Err }
