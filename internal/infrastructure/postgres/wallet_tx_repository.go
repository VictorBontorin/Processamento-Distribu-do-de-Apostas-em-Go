package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wager/internal/domain"
)

type WalletTxRepository struct {
	tx pgx.Tx
}

func NewWalletTxRepository(tx pgx.Tx) *WalletTxRepository {
	return &WalletTxRepository{tx: tx}
}

func (r *WalletTxRepository) GetByIDForUpdate(
	ctx context.Context,
	id domain.ID,
) (*domain.Wallet, error) {
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
		FOR UPDATE
	`

	var (
		rawID       string
		rawPlayerID string
		rawCurrency string
		balance     int64
		version     int64
		createdAt   time.Time
		updatedAt   time.Time
	)

	err := r.tx.QueryRow(ctx, query, id.String()).Scan(
		&rawID,
		&rawPlayerID,
		&rawCurrency,
		&balance,
		&version,
		&createdAt,
		&updatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("wallet %s: %w", id, errors.New("not found"))
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet for update: %w", err)
	}

	walletID, err := domain.ParseID(rawID)
	if err != nil {
		return nil, err
	}

	playerID, err := domain.ParseID(rawPlayerID)
	if err != nil {
		return nil, err
	}

	currency, err := domain.ParseCurrency(rawCurrency)
	if err != nil {
		return nil, err
	}

	money, err := domain.FromUnits(balance, currency)
	if err != nil {
		return nil, err
	}

	return domain.RehydrateWallet(
		walletID,
		playerID,
		money,
		version,
		createdAt,
		updatedAt,
	)
}

func (r *WalletTxRepository) Save(ctx context.Context, wallet *domain.Wallet) error {
	const query = `
		UPDATE wallets
		SET
			balance = $2,
			version = $3,
			updated_at = $4
		WHERE id = $1
	`

	tag, err := r.tx.Exec(
		ctx,
		query,
		wallet.ID().String(),
		wallet.Balance().Units(),
		wallet.Version(),
		wallet.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("save wallet: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("wallet %s: update affected %d rows", wallet.ID(), tag.RowsAffected())
	}

	return nil
}
