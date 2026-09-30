package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type WagerUnitOfWork struct {
	uow *UnitOfWork
}

func NewWagerUnitOfWork(uow *UnitOfWork) *WagerUnitOfWork {
	return &WagerUnitOfWork{uow: uow}
}

type WagerTransactionContext struct {
	Wallet       *WalletTxRepository
	Transactions *WagerTransactionTxRepository
	Ledger       *LedgerTxRepository
	Outbox       *OutboxTxRepository
	Inbox        *InboxTxRepository
}

func (u *WagerUnitOfWork) Run(
	ctx context.Context,
	fn func(*WagerTransactionContext) error,
) error {
	return u.uow.Run(ctx, func(tx pgx.Tx) error {
		txContext := &WagerTransactionContext{
			Wallet:       NewWalletTxRepository(tx),
			Transactions: NewWagerTransactionTxRepository(tx),
			Ledger:       NewLedgerTxRepository(tx),
			Outbox:       NewOutboxTxRepository(tx),
			Inbox:        NewInboxTxRepository(tx),
		}

		return fn(txContext)
	})
}
