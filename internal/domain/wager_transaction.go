package domain

import (
	"fmt"
	"time"
)

type TransactionKind string

const (
	TransactionOpening  TransactionKind = "OPENING"
	TransactionBet      TransactionKind = "BET"
	TransactionWin      TransactionKind = "WIN"
	TransactionLoss     TransactionKind = "LOSS"
	TransactionRefund   TransactionKind = "REFUND"
	TransactionRollback TransactionKind = "ROLLBACK"
)

type TransactionState string

const (
	StatePending          TransactionState = "PENDING"
	StatePendingReference TransactionState = "PENDING_REFERENCE"
	StateProcessed        TransactionState = "PROCESSED"
	StateRejected         TransactionState = "REJECTED"
	StateFailed           TransactionState = "FAILED"
)

type WagerTransaction struct {
	id                     ID
	externalTransactionID  string
	providerID             string
	idempotencyKey         string
	payloadHash            string
	walletID               ID
	playerID               ID
	roundID                string
	gameID                 string
	kind                   TransactionKind
	money                  Money
	referenceExternalID    string
	referenceTransactionID ID
	state                  TransactionState
	failureCode            FailureCode
	createdAt              time.Time
	updatedAt              time.Time
}

func NewExternalTransaction(
	id ID,
	externalTransactionID string,
	providerID string,
	idempotencyKey string,
	payloadHash string,
	walletID ID,
	playerID ID,
	roundID string,
	gameID string,
	kind TransactionKind,
	money Money,
	referenceExternalID string,
	now time.Time,
) (*WagerTransaction, error) {
	if id.IsZero() || walletID.IsZero() || playerID.IsZero() || now.IsZero() {
		return nil, fmt.Errorf("%w: missing identity or timestamp", ErrInvalidTransaction)
	}

	if externalTransactionID == "" || providerID == "" || idempotencyKey == "" {
		return nil, fmt.Errorf("%w: missing external transaction data", ErrInvalidTransaction)
	}

	if !isExternalTransactionKind(kind) {
		return nil, fmt.Errorf("%w: invalid external transaction kind %q", ErrInvalidTransaction, kind)
	}

	if !money.IsValid() {
		return nil, ErrUninitialized
	}

	if err := validateTransactionAmount(kind, money); err != nil {
		return nil, err
	}

	if (kind == TransactionRefund || kind == TransactionRollback) && referenceExternalID == "" {
		return nil, fmt.Errorf("%w: reference is required for %s", ErrInvalidTransaction, kind)
	}

	now = now.UTC()

	return &WagerTransaction{
		id:                    id,
		externalTransactionID: externalTransactionID,
		providerID:            providerID,
		idempotencyKey:        idempotencyKey,
		payloadHash:           payloadHash,
		walletID:              walletID,
		playerID:              playerID,
		roundID:               roundID,
		gameID:                gameID,
		kind:                  kind,
		money:                 money,
		referenceExternalID:   referenceExternalID,
		state:                 StatePending,
		createdAt:             now,
		updatedAt:             now,
	}, nil
}

func NewOpeningTransaction(
	id ID,
	walletID ID,
	playerID ID,
	money Money,
	now time.Time,
) (*WagerTransaction, error) {
	if id.IsZero() || walletID.IsZero() || playerID.IsZero() || now.IsZero() {
		return nil, fmt.Errorf("%w: missing identity or timestamp", ErrInvalidTransaction)
	}

	if !money.IsValid() {
		return nil, ErrUninitialized
	}

	if money.IsNegative() {
		return nil, ErrNegativeAmount
	}

	now = now.UTC()

	return &WagerTransaction{
		id:        id,
		walletID:  walletID,
		playerID:  playerID,
		kind:      TransactionOpening,
		money:     money,
		state:     StatePending,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// RehydrateWagerTransaction reconstrói uma transação persistida sem aplicar
// nenhuma movimentação ou transição de estado.
func RehydrateWagerTransaction(
	id ID,
	externalTransactionID string,
	providerID string,
	idempotencyKey string,
	payloadHash string,
	walletID ID,
	playerID ID,
	roundID string,
	gameID string,
	kind TransactionKind,
	money Money,
	referenceExternalID string,
	referenceTransactionID ID,
	state TransactionState,
	failureCode FailureCode,
	createdAt time.Time,
	updatedAt time.Time,
) (*WagerTransaction, error) {
	if id.IsZero() || walletID.IsZero() || playerID.IsZero() {
		return nil, fmt.Errorf("%w: missing identity", ErrInvalidTransaction)
	}

	if createdAt.IsZero() || updatedAt.IsZero() {
		return nil, fmt.Errorf("%w: missing timestamp", ErrInvalidTransaction)
	}

	if !money.IsValid() {
		return nil, ErrUninitialized
	}

	if kind == TransactionOpening {
		if externalTransactionID != "" ||
			providerID != "" ||
			idempotencyKey != "" ||
			payloadHash != "" ||
			referenceExternalID != "" ||
			!referenceTransactionID.IsZero() {
			return nil, fmt.Errorf("%w: OPENING contains external fields", ErrInvalidTransaction)
		}

		if money.IsNegative() {
			return nil, ErrNegativeAmount
		}
	} else {
		if !isExternalTransactionKind(kind) {
			return nil, fmt.Errorf("%w: invalid transaction kind %q", ErrInvalidTransaction, kind)
		}

		if externalTransactionID == "" || providerID == "" || idempotencyKey == "" {
			return nil, fmt.Errorf("%w: missing external transaction data", ErrInvalidTransaction)
		}

		if err := validateTransactionAmount(kind, money); err != nil {
			return nil, err
		}

		if (kind == TransactionRefund || kind == TransactionRollback) &&
			referenceExternalID == "" {
			return nil, fmt.Errorf("%w: reference is required for %s", ErrInvalidTransaction, kind)
		}
	}

	if !isValidTransactionState(state) {
		return nil, fmt.Errorf("%w: invalid transaction state %q", ErrInvalidTransaction, state)
	}

	if isTerminalState(state) && failureCode == "" && (state == StateRejected || state == StateFailed) {
		return nil, fmt.Errorf("%w: terminal failure state requires failure code", ErrInvalidTransaction)
	}

	if !isTerminalState(state) && failureCode != "" {
		return nil, fmt.Errorf("%w: non-terminal state cannot have failure code", ErrInvalidTransaction)
	}

	return &WagerTransaction{
		id:                     id,
		externalTransactionID:  externalTransactionID,
		providerID:             providerID,
		idempotencyKey:         idempotencyKey,
		payloadHash:            payloadHash,
		walletID:               walletID,
		playerID:               playerID,
		roundID:                roundID,
		gameID:                 gameID,
		kind:                   kind,
		money:                  money,
		referenceExternalID:    referenceExternalID,
		referenceTransactionID: referenceTransactionID,
		state:                  state,
		failureCode:            failureCode,
		createdAt:              createdAt.UTC(),
		updatedAt:              updatedAt.UTC(),
	}, nil
}

func (t *WagerTransaction) ID() ID {
	return t.id
}

func (t *WagerTransaction) ExternalTransactionID() string {
	return t.externalTransactionID
}

func (t *WagerTransaction) ProviderID() string {
	return t.providerID
}

func (t *WagerTransaction) IdempotencyKey() string {
	return t.idempotencyKey
}

func (t *WagerTransaction) PayloadHash() string {
	return t.payloadHash
}

func (t *WagerTransaction) WalletID() ID {
	return t.walletID
}

func (t *WagerTransaction) PlayerID() ID {
	return t.playerID
}

func (t *WagerTransaction) RoundID() string {
	return t.roundID
}

func (t *WagerTransaction) GameID() string {
	return t.gameID
}

func (t *WagerTransaction) Kind() TransactionKind {
	return t.kind
}

func (t *WagerTransaction) Money() Money {
	return t.money
}

func (t *WagerTransaction) ReferenceExternalID() string {
	return t.referenceExternalID
}

func (t *WagerTransaction) ReferenceTransactionID() ID {
	return t.referenceTransactionID
}

func (t *WagerTransaction) State() TransactionState {
	return t.state
}

func (t *WagerTransaction) FailureCode() FailureCode {
	return t.failureCode
}

func (t *WagerTransaction) CreatedAt() time.Time {
	return t.createdAt
}

func (t *WagerTransaction) UpdatedAt() time.Time {
	return t.updatedAt
}

func (t *WagerTransaction) Process(now time.Time) error {
	if err := t.validateTransition(StateProcessed); err != nil {
		return err
	}

	if now.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidTransaction)
	}

	t.state = StateProcessed
	t.updatedAt = now.UTC()

	return nil
}

func (t *WagerTransaction) WaitForReference(now time.Time) error {
	if err := t.validateTransition(StatePendingReference); err != nil {
		return err
	}

	if now.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidTransaction)
	}

	t.state = StatePendingReference
	t.updatedAt = now.UTC()

	return nil
}

func (t *WagerTransaction) Reject(code FailureCode, now time.Time) error {
	if err := t.validateTransition(StateRejected); err != nil {
		return err
	}

	if code == "" {
		return fmt.Errorf("%w: failure code is required", ErrInvalidTransaction)
	}

	if now.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidTransaction)
	}

	t.state = StateRejected
	t.failureCode = code
	t.updatedAt = now.UTC()

	return nil
}

func (t *WagerTransaction) Fail(code FailureCode, now time.Time) error {
	if err := t.validateTransition(StateFailed); err != nil {
		return err
	}

	if code == "" {
		return fmt.Errorf("%w: failure code is required", ErrInvalidTransaction)
	}

	if now.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidTransaction)
	}

	t.state = StateFailed
	t.failureCode = code
	t.updatedAt = now.UTC()

	return nil
}

func (t *WagerTransaction) SetReferenceTransactionID(id ID) error {
	if id.IsZero() {
		return fmt.Errorf("%w: reference transaction id is required", ErrInvalidTransaction)
	}

	if t.referenceExternalID == "" {
		return fmt.Errorf("%w: transaction has no external reference", ErrInvalidTransaction)
	}

	t.referenceTransactionID = id

	return nil
}

func (t *WagerTransaction) validateTransition(next TransactionState) error {
	if isTerminalState(t.state) {
		return fmt.Errorf(
			"%w: terminal state %s cannot transition to %s",
			ErrInvalidTransaction,
			t.state,
			next,
		)
	}

	switch t.state {
	case StatePending:
		switch next {
		case StateProcessed, StatePendingReference, StateRejected, StateFailed:
			return nil
		}

	case StatePendingReference:
		switch next {
		case StateProcessed, StateRejected, StateFailed:
			return nil
		}
	}

	return fmt.Errorf(
		"%w: invalid transition %s -> %s",
		ErrInvalidTransaction,
		t.state,
		next,
	)
}

func isTerminalState(state TransactionState) bool {
	return state == StateProcessed ||
		state == StateRejected ||
		state == StateFailed
}

func isValidTransactionState(state TransactionState) bool {
	switch state {
	case StatePending,
		StatePendingReference,
		StateProcessed,
		StateRejected,
		StateFailed:
		return true
	default:
		return false
	}
}

func isExternalTransactionKind(kind TransactionKind) bool {
	switch kind {
	case TransactionBet,
		TransactionWin,
		TransactionLoss,
		TransactionRefund,
		TransactionRollback:
		return true
	default:
		return false
	}
}

func validateTransactionAmount(kind TransactionKind, money Money) error {
	switch kind {
	case TransactionLoss:
		if !money.IsZero() {
			return fmt.Errorf(
				"%w: LOSS must have amount 0.00",
				ErrInvalidTransaction,
			)
		}

		return nil

	case TransactionBet,
		TransactionWin,
		TransactionRefund,
		TransactionRollback:
		if !money.IsPositive() {
			return ErrNonPositiveAmount
		}

		return nil

	default:
		return fmt.Errorf(
			"%w: unsupported transaction kind %q",
			ErrInvalidTransaction,
			kind,
		)
	}
}
