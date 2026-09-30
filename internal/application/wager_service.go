package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"wager/internal/domain"
)

type WalletSaver interface {
	GetByID(ctx context.Context, id domain.ID) (*domain.Wallet, error)
	Save(ctx context.Context, wallet *domain.Wallet) error
}

type WagerProcessor interface {
	Process(
		ctx context.Context,
		tx *domain.WagerTransaction,
		now time.Time,
	) error

	ProcessReversal(
		ctx context.Context,
		tx *domain.WagerTransaction,
		now time.Time,
	) error
}

type WagerService struct {
	wallets      WalletSaver
	transactions WagerTransactionRepository
}

func NewWagerService(
	wallets WalletSaver,
	transactions WagerTransactionRepository,
) *WagerService {
	return &WagerService{
		wallets:      wallets,
		transactions: transactions,
	}
}

func (s *WagerService) Process(
	ctx context.Context,
	tx *domain.WagerTransaction,
	now time.Time,
) error {
	wallet, err := s.wallets.GetByID(ctx, tx.WalletID())
	if err != nil {
		return fmt.Errorf("get wallet: %w", err)
	}

	switch tx.Kind() {
	case domain.TransactionBet:
		if _, err := wallet.Debit(tx.ID(), tx.Money(), now); err != nil {
			if rej, ok := err.(*domain.RejectionError); ok {
				_ = tx.Reject(rej.Code, now)
				return nil
			}

			_ = tx.Fail(domain.FailureCode("WALLET_ERROR"), now)
			return err
		}

	case domain.TransactionWin:
		if _, err := wallet.Credit(tx.ID(), tx.Money(), now); err != nil {
			_ = tx.Fail(domain.FailureCode("WALLET_ERROR"), now)
			return err
		}

	case domain.TransactionLoss:
		// LOSS com 0.00 não movimenta a carteira.

	default:
		return fmt.Errorf("transaction kind %s is not supported by Process", tx.Kind())
	}

	if err := s.wallets.Save(ctx, wallet); err != nil {
		_ = tx.Fail(domain.FailureCode("PERSISTENCE_ERROR"), now)
		return fmt.Errorf("save wallet: %w", err)
	}

	if err := tx.Process(now); err != nil {
		return fmt.Errorf("process transaction: %w", err)
	}

	return nil

}
func (s *WagerService) ProcessExternal(
	ctx context.Context,
	tx *domain.WagerTransaction,
	now time.Time,
) error {
	existing, err := s.transactions.GetByIdempotencyKey(
		ctx,
		tx.ProviderID(),
		tx.IdempotencyKey(),
	)

	if err == nil {
		if existing.PayloadHash() != tx.PayloadHash() {
			return tx.Reject(domain.CodeIdempotencyConflict, now)
		}

		return nil
	}

	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("check idempotency: %w", err)
	}

	if err := s.Process(ctx, tx, now); err != nil {
		return err
	}

	return s.transactions.Save(ctx, tx)

}
func (s *WagerService) ProcessReversal(
	ctx context.Context,
	tx *domain.WagerTransaction,
	now time.Time,
) error {
	reference, err := s.transactions.GetByExternalID(
		ctx,
		tx.ProviderID(),
		tx.ReferenceExternalID(),
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = tx.Reject(domain.CodeReferenceNotFound, now)
			return nil
		}

		return fmt.Errorf("get reference transaction: %w", err)
	}

	if reference.State() != domain.StateProcessed {
		_ = tx.Reject(domain.CodeReferenceNotProcessed, now)
		return nil
	}

	// Não permite reverter uma reversão.
	if reference.Kind() == domain.TransactionRefund ||
		reference.Kind() == domain.TransactionRollback {
		_ = tx.Reject(domain.CodeInvalidReversal, now)
		return nil
	}

	// Verifica se essa transação já foi usada como referência.
	existing, err := s.transactions.GetByReferenceExternalID(
		ctx,
		tx.ProviderID(),
		tx.ReferenceExternalID(),
	)
	if err == nil && existing.ID() != tx.ID() {
		_ = tx.Reject(domain.CodeDuplicateReversal, now)
		return nil
	}

	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("check duplicate reversal: %w", err)
	}

	wallet, err := s.wallets.GetByID(ctx, tx.WalletID())
	if err != nil {
		return fmt.Errorf("get wallet: %w", err)
	}

	if wallet.ID() != reference.WalletID() {
		_ = tx.Reject(domain.CodeInvalidReversal, now)
		return nil
	}

	var movementErr error

	switch {
	case tx.Kind() == domain.TransactionRefund &&
		reference.Kind() == domain.TransactionBet:

		_, movementErr = wallet.Credit(tx.ID(), tx.Money(), now)

	case tx.Kind() == domain.TransactionRollback &&
		reference.Kind() == domain.TransactionBet:

		_, movementErr = wallet.Credit(tx.ID(), tx.Money(), now)

	case tx.Kind() == domain.TransactionRollback &&
		reference.Kind() == domain.TransactionWin:

		_, movementErr = wallet.Debit(tx.ID(), tx.Money(), now)

	default:
		_ = tx.Reject(domain.CodeInvalidReversal, now)
		return nil
	}

	if movementErr != nil {
		if rej, ok := movementErr.(*domain.RejectionError); ok {
			_ = tx.Reject(rej.Code, now)
			return nil
		}

		_ = tx.Fail(domain.FailureCode("WALLET_ERROR"), now)
		return movementErr
	}

	if err := s.wallets.Save(ctx, wallet); err != nil {
		_ = tx.Fail(domain.FailureCode("PERSISTENCE_ERROR"), now)
		return fmt.Errorf("save wallet: %w", err)
	}

	if err := tx.Process(now); err != nil {
		return fmt.Errorf("process reversal: %w", err)
	}

	return s.transactions.Save(ctx, tx)
}
