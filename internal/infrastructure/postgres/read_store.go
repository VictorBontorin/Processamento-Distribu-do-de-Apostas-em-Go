package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wager/internal/domain"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalidCursor = errors.New("invalid cursor")
)

type ReadStore struct {
	pool *pgxpool.Pool
}

func NewReadStore(pool *pgxpool.Pool) *ReadStore {
	return &ReadStore{pool: pool}
}

// ---------- Ledger ----------

type LedgerEntryView struct {
	ID            string       `json:"id"`
	WalletID      string       `json:"walletId"`
	TransactionID string       `json:"transactionId"`
	Direction     string       `json:"direction"`
	Money         domain.Money `json:"money"`
	BalanceBefore domain.Money `json:"balanceBefore"`
	BalanceAfter  domain.Money `json:"balanceAfter"`
	CreatedAt     time.Time    `json:"createdAt"`
}

type LedgerPage struct {
	Entries    []LedgerEntryView `json:"entries"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

func encodeCursor(seq int64) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte("v1:" + strconv.FormatInt(seq, 10)),
	)
}

func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidCursor
	}

	value, ok := strings.CutPrefix(string(raw), "v1:")
	if !ok {
		return 0, ErrInvalidCursor
	}

	seq, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seq < 0 {
		return 0, ErrInvalidCursor
	}

	return seq, nil
}

// GetLedger pagina o ledger da carteira por cursor opaco, em ordem estável
// de inserção (seq).
func (s *ReadStore) GetLedger(
	ctx context.Context,
	walletID domain.ID,
	cursor string,
	limit int,
) (LedgerPage, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}

	var currency string

	if err := s.pool.QueryRow(
		ctx,
		`SELECT currency FROM wallets WHERE id = $1`,
		walletID.String(),
	).Scan(&currency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LedgerPage{}, ErrNotFound
		}

		return LedgerPage{}, fmt.Errorf("get wallet currency: %w", err)
	}

	rows, err := s.pool.Query(
		ctx,
		`
		SELECT
			id::text,
			transaction_id::text,
			direction,
			amount,
			balance_before,
			balance_after,
			created_at,
			seq
		FROM ledger_entries
		WHERE wallet_id = $1
		  AND seq > $2
		ORDER BY seq
		LIMIT $3
		`,
		walletID.String(),
		after,
		limit+1,
	)
	if err != nil {
		return LedgerPage{}, fmt.Errorf("query ledger: %w", err)
	}
	defer rows.Close()

	var (
		page    = LedgerPage{Entries: []LedgerEntryView{}}
		lastSeq int64
	)

	for rows.Next() {
		var (
			entry                  LedgerEntryView
			amount, before, afterB int64
			seq                    int64
		)

		if err := rows.Scan(
			&entry.ID,
			&entry.TransactionID,
			&entry.Direction,
			&amount,
			&before,
			&afterB,
			&entry.CreatedAt,
			&seq,
		); err != nil {
			return LedgerPage{}, fmt.Errorf("scan ledger: %w", err)
		}

		if len(page.Entries) == limit {
			// Existe mais uma página.
			page.NextCursor = encodeCursor(lastSeq)
			break
		}

		cur := domain.Currency(currency)

		if entry.Money, err = domain.FromUnits(amount, cur); err != nil {
			return LedgerPage{}, err
		}

		if entry.BalanceBefore, err = domain.FromUnits(before, cur); err != nil {
			return LedgerPage{}, err
		}

		if entry.BalanceAfter, err = domain.FromUnits(afterB, cur); err != nil {
			return LedgerPage{}, err
		}

		entry.WalletID = walletID.String()
		lastSeq = seq
		page.Entries = append(page.Entries, entry)
	}

	if err := rows.Err(); err != nil {
		return LedgerPage{}, fmt.Errorf("iterate ledger: %w", err)
	}

	return page, nil
}

// ---------- Transações ----------

type TransactionView struct {
	TransactionID                  string        `json:"transactionId"`
	ProviderID                     string        `json:"providerId,omitempty"`
	ExternalTransactionID          string        `json:"externalTransactionId,omitempty"`
	WalletID                       string        `json:"walletId"`
	PlayerID                       string        `json:"playerId"`
	RoundID                        string        `json:"roundId,omitempty"`
	GameID                         string        `json:"gameId,omitempty"`
	Kind                           string        `json:"kind"`
	Money                          domain.Money  `json:"money"`
	ReferenceExternalTransactionID string        `json:"referenceExternalTransactionId,omitempty"`
	ReferenceTransactionID         string        `json:"referenceTransactionId,omitempty"`
	Status                         string        `json:"status"`
	FailureCode                    string        `json:"failureCode,omitempty"`
	Balance                        *domain.Money `json:"balance,omitempty"`
	ReferenceAttempts              int           `json:"referenceAttempts,omitempty"`
	ReferenceExpiresAt             *time.Time    `json:"referenceExpiresAt,omitempty"`
	CreatedAt                      time.Time     `json:"createdAt"`
	UpdatedAt                      time.Time     `json:"updatedAt"`
}

const transactionViewSelect = `
	SELECT
		t.id::text,
		t.provider_id,
		t.external_transaction_id,
		t.wallet_id::text,
		t.player_id::text,
		t.round_id,
		t.game_id,
		t.kind,
		t.amount,
		t.currency,
		t.reference_external_id,
		t.reference_transaction_id::text,
		t.state,
		t.failure_code,
		t.result_balance,
		t.result_currency,
		t.reference_attempts,
		t.reference_expires_at,
		t.created_at,
		t.updated_at
	FROM wager_transactions t
`

func (s *ReadStore) queryTransaction(
	ctx context.Context,
	where string,
	args ...any,
) (TransactionView, error) {
	var (
		v                                                   TransactionView
		provider, external, round, game, refExt, refID, fc *string
		amount                                              int64
		currency                                            string
		resultBalance                                       *int64
		resultCurrency                                      *string
	)

	err := s.pool.QueryRow(ctx, transactionViewSelect+where, args...).Scan(
		&v.TransactionID,
		&provider,
		&external,
		&v.WalletID,
		&v.PlayerID,
		&round,
		&game,
		&v.Kind,
		&amount,
		&currency,
		&refExt,
		&refID,
		&v.Status,
		&fc,
		&resultBalance,
		&resultCurrency,
		&v.ReferenceAttempts,
		&v.ReferenceExpiresAt,
		&v.CreatedAt,
		&v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TransactionView{}, ErrNotFound
		}

		return TransactionView{}, fmt.Errorf("query transaction: %w", err)
	}

	deref := func(p *string) string {
		if p == nil {
			return ""
		}

		return *p
	}

	v.ProviderID = deref(provider)
	v.ExternalTransactionID = deref(external)
	v.RoundID = deref(round)
	v.GameID = deref(game)
	v.ReferenceExternalTransactionID = deref(refExt)
	v.ReferenceTransactionID = deref(refID)
	v.FailureCode = deref(fc)

	if v.Money, err = domain.FromUnits(amount, domain.Currency(currency)); err != nil {
		return TransactionView{}, err
	}

	if resultBalance != nil && resultCurrency != nil {
		balance, err := domain.FromUnits(
			*resultBalance,
			domain.Currency(*resultCurrency),
		)
		if err != nil {
			return TransactionView{}, err
		}

		v.Balance = &balance
	}

	return v, nil
}

func (s *ReadStore) GetTransaction(
	ctx context.Context,
	id domain.ID,
) (TransactionView, error) {
	return s.queryTransaction(ctx, ` WHERE t.id = $1`, id.String())
}

func (s *ReadStore) GetProviderTransaction(
	ctx context.Context,
	providerID string,
	externalTransactionID string,
) (TransactionView, error) {
	return s.queryTransaction(
		ctx,
		` WHERE t.provider_id = $1 AND t.external_transaction_id = $2`,
		providerID,
		externalTransactionID,
	)
}

// ---------- Reconciliação ----------

type Reconciliation struct {
	WalletID          string       `json:"walletId"`
	StoredBalance     domain.Money `json:"storedBalance"`
	CalculatedBalance domain.Money `json:"calculatedBalance"`
	Difference        domain.Money `json:"difference"`
	Consistent        bool         `json:"consistent"`
	CheckedEntries    int64        `json:"checkedEntries"`
}

// Reconcile reconstrói o saldo a partir do ledger e compara com o saldo
// armazenado, em uma única visão consistente (REPEATABLE READ, somente
// leitura). Não altera nenhum dado.
func (s *ReadStore) Reconcile(
	ctx context.Context,
	walletID domain.ID,
) (Reconciliation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Reconciliation{}, fmt.Errorf("begin reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		stored   int64
		currency string
	)

	if err := tx.QueryRow(
		ctx,
		`SELECT balance, currency FROM wallets WHERE id = $1`,
		walletID.String(),
	).Scan(&stored, &currency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reconciliation{}, ErrNotFound
		}

		return Reconciliation{}, fmt.Errorf("get wallet: %w", err)
	}

	var (
		calculated int64
		count      int64
	)

	if err := tx.QueryRow(
		ctx,
		`
		SELECT
			COALESCE(SUM(
				CASE direction WHEN 'CREDIT' THEN amount ELSE -amount END
			), 0)::bigint,
			COUNT(*)
		FROM ledger_entries
		WHERE wallet_id = $1
		`,
		walletID.String(),
	).Scan(&calculated, &count); err != nil {
		return Reconciliation{}, fmt.Errorf("sum ledger: %w", err)
	}

	cur := domain.Currency(currency)

	storedMoney, err := domain.FromUnits(stored, cur)
	if err != nil {
		return Reconciliation{}, err
	}

	calculatedMoney, err := domain.FromUnits(calculated, cur)
	if err != nil {
		return Reconciliation{}, err
	}

	difference, err := storedMoney.Sub(calculatedMoney)
	if err != nil {
		return Reconciliation{}, err
	}

	result := Reconciliation{
		WalletID:          walletID.String(),
		StoredBalance:     storedMoney,
		CalculatedBalance: calculatedMoney,
		Difference:        difference,
		Consistent:        difference.IsZero(),
		CheckedEntries:    count,
	}

	if !result.Consistent {
		slog.ErrorContext(
			ctx,
			"wallet reconciliation divergence",
			"walletId", result.WalletID,
			"storedBalance", storedMoney.Amount(),
			"calculatedBalance", calculatedMoney.Amount(),
			"difference", difference.Amount(),
			"currency", currency,
			"checkedEntries", count,
		)
	}

	return result, nil
}
