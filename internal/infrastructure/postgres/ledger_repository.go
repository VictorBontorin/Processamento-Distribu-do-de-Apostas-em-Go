package postgres

import (
	"context"
	"fmt"
	"time"

	"wager/internal/application"
	"wager/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type LedgerRepository struct {
	pool *pgxpool.Pool
}

func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

func (r *LedgerRepository) ListByWallet(
	ctx context.Context,
	walletID domain.ID,
	cursor string,
	limit int,
) ([]domain.LedgerEntry, string, error) {
	if limit <= 0 {
		limit = 20
	}

	if limit > 100 {
		limit = 100
	}

	query := `
		SELECT
			l.id,
			l.wallet_id,
			l.transaction_id,
			l.direction,
			l.amount,
			l.balance_before,
			l.balance_after,
			l.created_at,
			w.currency
		FROM ledger_entries l
		INNER JOIN wallets w ON w.id = l.wallet_id
		WHERE l.wallet_id = $1
	`

	args := []any{walletID.String()}
	argIndex := 2

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}

		query += fmt.Sprintf(" AND l.created_at < $%d", argIndex)
		args = append(args, cursorTime)
		argIndex++
	}

	query += fmt.Sprintf(`
		ORDER BY l.created_at DESC
		LIMIT $%d
	`, argIndex)

	args = append(args, limit+1)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list ledger: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.LedgerEntry, 0, limit)

	for rows.Next() {
		var (
			rawID          string
			rawWalletID    string
			rawTransaction string
			direction      string
			amount         int64
			balanceBefore  int64
			balanceAfter   int64
			createdAt      time.Time
			rawCurrency    string
		)

		if err := rows.Scan(
			&rawID,
			&rawWalletID,
			&rawTransaction,
			&direction,
			&amount,
			&balanceBefore,
			&balanceAfter,
			&createdAt,
			&rawCurrency,
		); err != nil {
			return nil, "", fmt.Errorf("scan ledger: %w", err)
		}

		id, err := domain.ParseID(rawID)
		if err != nil {
			return nil, "", err
		}

		walletIDParsed, err := domain.ParseID(rawWalletID)
		if err != nil {
			return nil, "", err
		}

		transactionID, err := domain.ParseID(rawTransaction)
		if err != nil {
			return nil, "", err
		}

		currency, err := domain.ParseCurrency(rawCurrency)
		if err != nil {
			return nil, "", fmt.Errorf("parse currency: %w", err)
		}

		amountMoney, err := domain.FromUnits(amount, currency)
		if err != nil {
			return nil, "", fmt.Errorf("parse amount: %w", err)
		}

		balanceBeforeMoney, err := domain.FromUnits(balanceBefore, currency)
		if err != nil {
			return nil, "", fmt.Errorf("parse balance before: %w", err)
		}

		balanceAfterMoney, err := domain.FromUnits(balanceAfter, currency)
		if err != nil {
			return nil, "", fmt.Errorf("parse balance after: %w", err)
		}

		entry, err := domain.RehydrateLedgerEntry(
			id,
			walletIDParsed,
			transactionID,
			domain.Direction(direction),
			amountMoney,
			balanceBeforeMoney,
			balanceAfterMoney,
			createdAt,
		)
		if err != nil {
			return nil, "", fmt.Errorf("rehydrate ledger: %w", err)
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate ledger: %w", err)
	}

	var nextCursor string

	if len(entries) > limit {
		entries = entries[:limit]
		nextCursor = entries[len(entries)-1].CreatedAt().Format(time.RFC3339Nano)
	}

	return entries, nextCursor, nil
}

var _ application.LedgerRepository = (*LedgerRepository)(nil)
