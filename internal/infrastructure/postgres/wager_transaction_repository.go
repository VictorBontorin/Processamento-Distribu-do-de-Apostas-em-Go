package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wager/internal/application"
	"wager/internal/domain"
)

type WagerTransactionRepository struct {
	pool *pgxpool.Pool
}

func NewWagerTransactionRepository(pool *pgxpool.Pool) *WagerTransactionRepository {
	return &WagerTransactionRepository{
		pool: pool,
	}
}

var _ application.WagerTransactionRepository = (*WagerTransactionRepository)(nil)

type TransactionResult struct {
	Balance  domain.Money
	HasValue bool
}

func (r *WagerTransactionRepository) GetByID(
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

	return r.get(ctx, query, id.String())
}

func (r *WagerTransactionRepository) GetByIdempotencyKey(
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
	`

	return r.get(ctx, query, providerID, idempotencyKey)
}

func (r *WagerTransactionRepository) GetByExternalID(
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
	`

	return r.get(ctx, query, providerID, externalTransactionID)
}

func (r *WagerTransactionRepository) GetByReferenceExternalID(
	ctx context.Context,
	providerID string,
	referenceExternalID string,
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
		  AND reference_external_id = $2
		LIMIT 1
	`

	return r.get(ctx, query, providerID, referenceExternalID)
}

func (r *WagerTransactionRepository) Save(
	ctx context.Context,
	tx *domain.WagerTransaction,
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
			state = EXCLUDED.state,
			failure_code = EXCLUDED.failure_code,
			reference_transaction_id = EXCLUDED.reference_transaction_id,
			updated_at = EXCLUDED.updated_at
	`

	var externalID *string
	var providerID *string
	var idempotencyKey *string
	var payloadHash *string
	var roundID *string
	var gameID *string
	var referenceExternalID *string
	var referenceTransactionID *string
	var failureCode *string

	if tx.ExternalTransactionID() != "" {
		value := tx.ExternalTransactionID()
		externalID = &value
	}

	if tx.ProviderID() != "" {
		value := tx.ProviderID()
		providerID = &value
	}

	if tx.IdempotencyKey() != "" {
		value := tx.IdempotencyKey()
		idempotencyKey = &value
	}

	if tx.PayloadHash() != "" {
		value := tx.PayloadHash()
		payloadHash = &value
	}

	if tx.RoundID() != "" {
		value := tx.RoundID()
		roundID = &value
	}

	if tx.GameID() != "" {
		value := tx.GameID()
		gameID = &value
	}

	if tx.ReferenceExternalID() != "" {
		value := tx.ReferenceExternalID()
		referenceExternalID = &value
	}

	if !tx.ReferenceTransactionID().IsZero() {
		value := tx.ReferenceTransactionID().String()
		referenceTransactionID = &value
	}

	if tx.FailureCode() != "" {
		value := string(tx.FailureCode())
		failureCode = &value
	}

	_, err := r.pool.Exec(
		ctx,
		query,
		tx.ID().String(),
		externalID,
		providerID,
		idempotencyKey,
		payloadHash,
		tx.WalletID().String(),
		tx.PlayerID().String(),
		roundID,
		gameID,
		string(tx.Kind()),
		tx.Money().Units(),
		string(tx.Money().Currency()),
		referenceExternalID,
		referenceTransactionID,
		string(tx.State()),
		failureCode,
		tx.CreatedAt(),
		tx.UpdatedAt(),
	)

	if err != nil {
		return fmt.Errorf("save wager transaction: %w", err)
	}

	return nil
}

func (r *WagerTransactionRepository) SaveResultSnapshot(
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

	tag, err := r.pool.Exec(
		ctx,
		query,
		id.String(),
		balance.Units(),
		string(balance.Currency()),
	)
	if err != nil {
		return fmt.Errorf(
			"save transaction result snapshot: %w",
			err,
		)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"save transaction result snapshot: %w",
			application.ErrNotFound,
		)
	}

	return nil
}

func (r *WagerTransactionRepository) GetResultSnapshot(
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
		balance  *int64
		currency *string
	)

	err := r.pool.QueryRow(
		ctx,
		query,
		id.String(),
	).Scan(
		&balance,
		&currency,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return TransactionResult{}, fmt.Errorf(
			"%w",
			application.ErrNotFound,
		)
	}

	if err != nil {
		return TransactionResult{}, fmt.Errorf(
			"get transaction result snapshot: %w",
			err,
		)
	}

	if balance == nil || currency == nil {
		return TransactionResult{}, nil
	}

	parsedCurrency, err := domain.ParseCurrency(*currency)
	if err != nil {
		return TransactionResult{}, fmt.Errorf(
			"parse result currency: %w",
			err,
		)
	}

	money, err := domain.FromUnits(
		*balance,
		parsedCurrency,
	)
	if err != nil {
		return TransactionResult{}, fmt.Errorf(
			"parse result balance: %w",
			err,
		)
	}

	return TransactionResult{
		Balance:  money,
		HasValue: true,
	}, nil
}

func (r *WagerTransactionRepository) get(
	ctx context.Context,
	query string,
	args ...any,
) (*domain.WagerTransaction, error) {
	var (
		rawID                  string
		externalTransactionID  *string
		providerID             *string
		idempotencyKey         *string
		payloadHash            *string
		rawWalletID            string
		rawPlayerID            string
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

	err := r.pool.QueryRow(
		ctx,
		query,
		args...,
	).Scan(
		&rawID,
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&rawWalletID,
		&rawPlayerID,
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

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf(
			"%w",
			application.ErrNotFound,
		)
	}

	if err != nil {
		return nil, fmt.Errorf(
			"get wager transaction: %w",
			err,
		)
	}

	id, err := domain.ParseID(rawID)
	if err != nil {
		return nil, fmt.Errorf(
			"parse transaction id: %w",
			err,
		)
	}

	walletID, err := domain.ParseID(rawWalletID)
	if err != nil {
		return nil, fmt.Errorf(
			"parse wallet id: %w",
			err,
		)
	}

	playerID, err := domain.ParseID(rawPlayerID)
	if err != nil {
		return nil, fmt.Errorf(
			"parse player id: %w",
			err,
		)
	}

	moneyCurrency, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, fmt.Errorf(
			"parse transaction currency: %w",
			err,
		)
	}

	money, err := domain.FromUnits(
		amount,
		moneyCurrency,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"rehydrate transaction money: %w",
			err,
		)
	}

	var externalIDValue string
	var providerIDValue string
	var idempotencyKeyValue string
	var payloadHashValue string
	var roundIDValue string
	var gameIDValue string
	var referenceExternalIDValue string
	var failureCodeValue domain.FailureCode

	if externalTransactionID != nil {
		externalIDValue = *externalTransactionID
	}

	if providerID != nil {
		providerIDValue = *providerID
	}

	if idempotencyKey != nil {
		idempotencyKeyValue = *idempotencyKey
	}

	if payloadHash != nil {
		payloadHashValue = *payloadHash
	}

	if roundID != nil {
		roundIDValue = *roundID
	}

	if gameID != nil {
		gameIDValue = *gameID
	}

	if referenceExternalID != nil {
		referenceExternalIDValue = *referenceExternalID
	}

	var referenceID domain.ID

	if referenceTransactionID != nil {
		referenceID, err = domain.ParseID(
			*referenceTransactionID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"parse reference transaction id: %w",
				err,
			)
		}
	}

	if failureCode != nil {
		failureCodeValue = domain.FailureCode(
			*failureCode,
		)
	}

	tx, err := domain.RehydrateWagerTransaction(
		id,
		externalIDValue,
		providerIDValue,
		idempotencyKeyValue,
		payloadHashValue,
		walletID,
		playerID,
		roundIDValue,
		gameIDValue,
		domain.TransactionKind(kind),
		money,
		referenceExternalIDValue,
		referenceID,
		domain.TransactionState(state),
		failureCodeValue,
		createdAt,
		updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"rehydrate wager transaction: %w",
			err,
		)
	}

	return tx, nil
}
