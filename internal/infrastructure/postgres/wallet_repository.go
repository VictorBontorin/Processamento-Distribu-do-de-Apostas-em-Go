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

type WalletRepository struct {
	pool *pgxpool.Pool
}

func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

var _ application.WalletRepository = (*WalletRepository)(nil)

func (r *WalletRepository) GetByID(ctx context.Context, id domain.ID) (*domain.Wallet, error) {
	const query = `
		SELECT
			id,
			player_id,
			currency,
			balance,
			version,
			created_at,
			updated_at
		FROM wallets
		WHERE id = $1
	`

	var (
		rawID       string
		rawPlayerID string
		currency    string
		balance     int64
		version     int64
		createdAt   time.Time
		updatedAt   time.Time
	)

	err := r.pool.QueryRow(ctx, query, id.String()).Scan(
		&rawID,
		&rawPlayerID,
		&currency,
		&balance,
		&version,
		&createdAt,
		&updatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("wallet %s: %w", id, application.ErrNotFound)
	}

	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}

	parsedID, err := domain.ParseID(rawID)
	if err != nil {
		return nil, fmt.Errorf("parse wallet id: %w", err)
	}

	playerID, err := domain.ParseID(rawPlayerID)
	if err != nil {
		return nil, fmt.Errorf("parse player id: %w", err)
	}

	parsedCurrency, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, fmt.Errorf("parse wallet currency: %w", err)
	}

	money, err := domain.FromUnits(balance, parsedCurrency)
	if err != nil {
		return nil, fmt.Errorf("rehydrate wallet money: %w", err)
	}

	wallet, err := domain.RehydrateWallet(
		parsedID,
		playerID,
		money,
		version,
		createdAt,
		updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("rehydrate wallet: %w", err)
	}

	return wallet, nil
}

func (r *WalletRepository) Save(ctx context.Context, wallet *domain.Wallet) error {
	const query = `
		INSERT INTO wallets (
			id,
			player_id,
			currency,
			balance,
			version,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			balance = EXCLUDED.balance,
			version = EXCLUDED.version,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.pool.Exec(
		ctx,
		query,
		wallet.ID().String(),
		wallet.PlayerID().String(),
		string(wallet.Currency()),
		wallet.Balance().Units(),
		wallet.Version(),
		wallet.CreatedAt(),
		wallet.UpdatedAt(),
	)

	if err != nil {
		return fmt.Errorf("save wallet: %w", err)
	}

	return nil
}
