package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"wager/internal/domain"
)

type LedgerTxRepository struct {
	tx pgx.Tx
}

func NewLedgerTxRepository(tx pgx.Tx) *LedgerTxRepository {
	return &LedgerTxRepository{tx: tx}
}

func (r *LedgerTxRepository) Save(
	ctx context.Context,
	entry *domain.LedgerEntry,
) error {
	const query = `
		INSERT INTO ledger_entries (
			id,
			wallet_id,
			transaction_id,
			direction,
			amount,
			balance_before,
			balance_after,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.tx.Exec(
		ctx,
		query,
		entry.ID().String(),
		entry.WalletID().String(),
		entry.TransactionID().String(),
		string(entry.Direction()),
		entry.Amount().Units(),
		entry.BalanceBefore().Units(),
		entry.BalanceAfter().Units(),
		entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("save ledger entry: %w", err)
	}

	return nil
}
