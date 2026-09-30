package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wager/internal/domain"
)

type WagerTransactionTxRepository struct {
	tx pgx.Tx
}

func NewWagerTransactionTxRepository(tx pgx.Tx) *WagerTransactionTxRepository {
	return &WagerTransactionTxRepository{tx: tx}
}

// CreateIfAbsent tenta criar a transação.
// Retorna true quando criou.
// Retorna false quando já existe por idempotency_key ou external_transaction_id.
func (r *WagerTransactionTxRepository) CreateIfAbsent(
	ctx context.Context,
	transaction *domain.WagerTransaction,
) (bool, error) {
	const query = `
		INSERT INTO wager_transactions (
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		ON CONFLICT DO NOTHING
	`

	commandTag, err := r.tx.Exec(
		ctx,
		query,
		transaction.ID().String(),
		nullableString(transaction.ExternalTransactionID()),
		nullableString(transaction.ProviderID()),
		nullableString(transaction.IdempotencyKey()),
		nullableString(transaction.PayloadHash()),
		transaction.WalletID().String(),
		transaction.PlayerID().String(),
		nullableString(transaction.RoundID()),
		nullableString(transaction.GameID()),
		string(transaction.Kind()),
		transaction.Money().Units(),
		string(transaction.Money().Currency()),
		nullableString(transaction.ReferenceExternalID()),
		nullableID(transaction.ReferenceTransactionID()),
		string(transaction.State()),
		nullableFailureCode(transaction.FailureCode()),
		transaction.CreatedAt(),
		transaction.UpdatedAt(),
	)
	if err != nil {
		return false, fmt.Errorf("create wager transaction: %w", err)
	}

	return commandTag.RowsAffected() == 1, nil
}

func (r *WagerTransactionTxRepository) Save(
	ctx context.Context,
	transaction *domain.WagerTransaction,
) error {
	const query = `
		INSERT INTO wager_transactions (
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		ON CONFLICT (id) DO UPDATE SET
			reference_transaction_id = EXCLUDED.reference_transaction_id,
			state = EXCLUDED.state,
			failure_code = EXCLUDED.failure_code,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.tx.Exec(
		ctx,
		query,
		transaction.ID().String(),
		nullableString(transaction.ExternalTransactionID()),
		nullableString(transaction.ProviderID()),
		nullableString(transaction.IdempotencyKey()),
		nullableString(transaction.PayloadHash()),
		transaction.WalletID().String(),
		transaction.PlayerID().String(),
		nullableString(transaction.RoundID()),
		nullableString(transaction.GameID()),
		string(transaction.Kind()),
		transaction.Money().Units(),
		string(transaction.Money().Currency()),
		nullableString(transaction.ReferenceExternalID()),
		nullableID(transaction.ReferenceTransactionID()),
		string(transaction.State()),
		nullableFailureCode(transaction.FailureCode()),
		transaction.CreatedAt(),
		transaction.UpdatedAt(),
	)

	if err != nil {
		return fmt.Errorf("save wager transaction: %w", err)
	}

	return nil
}

func (r *WagerTransactionTxRepository) GetByIdempotencyKey(
	ctx context.Context,
	providerID string,
	idempotencyKey string,
) (*domain.WagerTransaction, error) {
	const query = `
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND idempotency_key = $2
		FOR UPDATE
	`

	return r.getByQuery(
		ctx,
		query,
		providerID,
		idempotencyKey,
	)
}

func (r *WagerTransactionTxRepository) GetByExternalID(
	ctx context.Context,
	providerID string,
	externalTransactionID string,
) (*domain.WagerTransaction, error) {
	const query = `
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND external_transaction_id = $2
		FOR UPDATE
	`

	return r.getByQuery(
		ctx,
		query,
		providerID,
		externalTransactionID,
	)
}

func (r *WagerTransactionTxRepository) SaveResultSnapshot(
	ctx context.Context,
	id domain.ID,
	balance domain.Money,
) error {
	const query = `
		UPDATE wager_transactions
		SET
			result_balance = $2,
			result_currency = $3
		WHERE id = $1
	`

	commandTag, err := r.tx.Exec(
		ctx,
		query,
		id.String(),
		balance.Units(),
		string(balance.Currency()),
	)
	if err != nil {
		return fmt.Errorf("save transaction result snapshot: %w", err)
	}

	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf(
			"save transaction result snapshot: transaction %s not found",
			id,
		)
	}

	return nil
}

func (r *WagerTransactionTxRepository) GetResultSnapshot(
	ctx context.Context,
	id domain.ID,
) (TransactionResult, error) {
	const query = `
		SELECT
			result_balance,
			result_currency
		FROM wager_transactions
		WHERE id = $1
	`

	var (
		resultBalance  *int64
		resultCurrency *string
	)

	err := r.tx.QueryRow(
		ctx,
		query,
		id.String(),
	).Scan(
		&resultBalance,
		&resultCurrency,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TransactionResult{}, fmt.Errorf(
				"get transaction result snapshot: %w",
				pgx.ErrNoRows,
			)
		}

		return TransactionResult{}, fmt.Errorf(
			"get transaction result snapshot: %w",
			err,
		)
	}

	if resultBalance == nil || resultCurrency == nil {
		return TransactionResult{
			HasValue: false,
		}, nil
	}

	currency, err := domain.ParseCurrency(*resultCurrency)
	if err != nil {
		return TransactionResult{}, fmt.Errorf(
			"parse result currency: %w",
			err,
		)
	}

	balance, err := domain.FromUnits(
		*resultBalance,
		currency,
	)
	if err != nil {
		return TransactionResult{}, fmt.Errorf(
			"parse result balance: %w",
			err,
		)
	}

	return TransactionResult{
		Balance:  balance,
		HasValue: true,
	}, nil
}

func (r *WagerTransactionTxRepository) GetByID(
	ctx context.Context,
	id domain.ID,
) (*domain.WagerTransaction, error) {
	const query = `
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		FROM wager_transactions
		WHERE id = $1
	`

	row := r.tx.QueryRow(
		ctx,
		query,
		id.String(),
	)

	var (
		transactionID          string
		externalTransactionID  *string
		providerID             *string
		idempotencyKey         *string
		payloadHash            *string
		walletID               string
		playerID               string
		roundID                *string
		gameID                 *string
		kind                   string
		amount                 int64
		currency               string
		referenceExternalID    *string
		referenceTransactionID *string
		state                  string
		failureCode            *string
		createdAt              time.Time
		updatedAt              time.Time
	)

	err := row.Scan(
		&transactionID,
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&amount,
		&currency,
		&referenceExternalID,
		&referenceTransactionID,
		&state,
		&failureCode,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	parsedID, err := domain.ParseID(transactionID)
	if err != nil {
		return nil, fmt.Errorf("parse transaction id: %w", err)
	}

	parsedWalletID, err := domain.ParseID(walletID)
	if err != nil {
		return nil, fmt.Errorf("parse wallet id: %w", err)
	}

	parsedPlayerID, err := domain.ParseID(playerID)
	if err != nil {
		return nil, fmt.Errorf("parse player id: %w", err)
	}

	parsedCurrency, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, fmt.Errorf("parse currency: %w", err)
	}

	money, err := domain.FromUnits(amount, parsedCurrency)
	if err != nil {
		return nil, fmt.Errorf("parse transaction money: %w", err)
	}

	var referenceID domain.ID
	if referenceTransactionID != nil {
		referenceID, err = domain.ParseID(*referenceTransactionID)
		if err != nil {
			return nil, fmt.Errorf(
				"parse reference transaction id: %w",
				err,
			)
		}
	}

	var failure domain.FailureCode
	if failureCode != nil {
		failure = domain.FailureCode(*failureCode)
	}

	return domain.RehydrateWagerTransaction(
		parsedID,
		deref(externalTransactionID),
		deref(providerID),
		deref(idempotencyKey),
		deref(payloadHash),
		parsedWalletID,
		parsedPlayerID,
		deref(roundID),
		deref(gameID),
		domain.TransactionKind(kind),
		money,
		deref(referenceExternalID),
		referenceID,
		domain.TransactionState(state),
		failure,
		createdAt,
		updatedAt,
	)
}

func (r *WagerTransactionTxRepository) getByQuery(
	ctx context.Context,
	query string,
	arg1 string,
	arg2 string,
) (*domain.WagerTransaction, error) {
	row := r.tx.QueryRow(
		ctx,
		query,
		arg1,
		arg2,
	)

	var (
		id                     string
		externalTransactionID  *string
		providerID             *string
		idempotencyKey         *string
		payloadHash            *string
		walletID               string
		playerID               string
		roundID                *string
		gameID                 *string
		kind                   string
		amount                 int64
		currency               string
		referenceExternalID    *string
		referenceTransactionID *string
		state                  string
		failureCode            *string
		createdAt              = time.Time{}
		updatedAt              = time.Time{}
	)

	err := row.Scan(
		&id,
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&amount,
		&currency,
		&referenceExternalID,
		&referenceTransactionID,
		&state,
		&failureCode,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	parsedID, err := domain.ParseID(id)
	if err != nil {
		return nil, fmt.Errorf("parse transaction id: %w", err)
	}

	parsedWalletID, err := domain.ParseID(walletID)
	if err != nil {
		return nil, fmt.Errorf("parse wallet id: %w", err)
	}

	parsedPlayerID, err := domain.ParseID(playerID)
	if err != nil {
		return nil, fmt.Errorf("parse player id: %w", err)
	}

	parsedCurrency, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, fmt.Errorf("parse currency: %w", err)
	}

	money, err := domain.FromUnits(
		amount,
		parsedCurrency,
	)
	if err != nil {
		return nil, fmt.Errorf("parse transaction money: %w", err)
	}

	var referenceID domain.ID

	if referenceTransactionID != nil {
		referenceID, err = domain.ParseID(*referenceTransactionID)
		if err != nil {
			return nil, fmt.Errorf(
				"parse reference transaction id: %w",
				err,
			)
		}
	}

	var failure domain.FailureCode

	if failureCode != nil {
		failure = domain.FailureCode(*failureCode)
	}

	return domain.RehydrateWagerTransaction(
		parsedID,
		deref(externalTransactionID),
		deref(providerID),
		deref(idempotencyKey),
		deref(payloadHash),
		parsedWalletID,
		parsedPlayerID,
		deref(roundID),
		deref(gameID),
		domain.TransactionKind(kind),
		money,
		deref(referenceExternalID),
		referenceID,
		domain.TransactionState(state),
		failure,
		createdAt,
		updatedAt,
	)
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func nullableID(id domain.ID) *string {
	if id.IsZero() {
		return nil
	}

	value := id.String()
	return &value
}

func nullableFailureCode(code domain.FailureCode) *string {
	if code == "" {
		return nil
	}

	value := string(code)
	return &value
}
