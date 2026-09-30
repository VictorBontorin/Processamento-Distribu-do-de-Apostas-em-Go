package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"wager/internal/domain"
)

var ErrWalletAlreadyExists = errors.New(
	"wallet already exists for player and currency",
)

// Create insere uma carteira nova. Retorna ErrWalletAlreadyExists quando
// o par (player_id, currency) já possui carteira.
func (r *WalletTxRepository) Create(
	ctx context.Context,
	wallet *domain.Wallet,
) error {
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
	`

	_, err := r.tx.Exec(
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
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "wallets_player_currency_unique" {
			return ErrWalletAlreadyExists
		}

		return fmt.Errorf("create wallet: %w", err)
	}

	return nil
}

// OpenWallet cria a carteira e, quando o saldo inicial é positivo, a
// transação OPENING (PROCESSED), o lançamento de crédito e os eventos
// WagerTransactionProcessed e WalletBalanceChanged, tudo no mesmo commit.
// Saldo inicial zero não cria OPENING, ledger nem eventos.
func (s *WagerStore) OpenWallet(
	ctx context.Context,
	playerID domain.ID,
	initial domain.Money,
	now time.Time,
) (*domain.Wallet, error) {
	wallet, err := domain.NewWallet(
		domain.NewID(),
		playerID,
		initial,
		now,
	)
	if err != nil {
		return nil, err
	}

	err = s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		if err := r.Wallet.Create(ctx, wallet); err != nil {
			return err
		}

		if initial.IsZero() {
			return nil
		}

		opening, err := domain.NewOpeningTransaction(
			domain.NewID(),
			wallet.ID(),
			playerID,
			initial,
			now,
		)
		if err != nil {
			return err
		}

		zero, err := domain.Zero(initial.Currency())
		if err != nil {
			return err
		}

		entry, err := domain.NewLedgerEntry(
			domain.NewID(),
			wallet.ID(),
			opening.ID(),
			domain.DirectionCredit,
			initial,
			zero,
			initial,
			now,
		)
		if err != nil {
			return err
		}

		if err := opening.Process(now); err != nil {
			return err
		}

		if err := r.Transactions.Save(ctx, opening); err != nil {
			return fmt.Errorf("save opening transaction: %w", err)
		}

		if err := r.Ledger.Save(ctx, &entry); err != nil {
			return fmt.Errorf("save opening ledger entry: %w", err)
		}

		if err := r.Transactions.SaveResultSnapshot(
			ctx,
			opening.ID(),
			wallet.Balance(),
		); err != nil {
			return err
		}

		if err := r.Outbox.Save(ctx, OutboxEvent{
			ID:          domain.NewID(),
			EventType:   "WagerTransactionProcessed",
			AggregateID: opening.ID(),
			CausationID: opening.ID(),
			OccurredAt:  now,
			Version:     1,
			Payload: map[string]any{
				"transactionId": opening.ID(),
				"walletId":      wallet.ID(),
				"playerId":      playerID,
				"kind":          opening.Kind(),
				"state":         opening.State(),
				"money":         initial,
			},
		}); err != nil {
			return fmt.Errorf("save opening processed event: %w", err)
		}

		return r.Outbox.Save(ctx, OutboxEvent{
			ID:          domain.NewID(),
			EventType:   "WalletBalanceChanged",
			AggregateID: wallet.ID(),
			CausationID: opening.ID(),
			OccurredAt:  now,
			Version:     1,
			Payload: map[string]any{
				"walletId":      wallet.ID(),
				"transactionId": opening.ID(),
				"direction":     entry.Direction(),
				"money":         entry.Amount(),
				"balanceBefore": entry.BalanceBefore(),
				"balanceAfter":  entry.BalanceAfter(),
				"walletVersion": wallet.Version(),
			},
		})
	})
	if err != nil {
		return nil, err
	}

	return wallet, nil
}
