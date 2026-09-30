package application

import (
	"context"

	"wager/internal/domain"
)

type LedgerRepository interface {
	ListByWallet(
		ctx context.Context,
		walletID domain.ID,
		cursor string,
		limit int,
	) ([]domain.LedgerEntry, string, error)
}
