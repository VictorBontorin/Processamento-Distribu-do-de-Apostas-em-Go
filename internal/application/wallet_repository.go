package application

import (
	"context"

	"wager/internal/domain"
)

type WalletRepository interface {
	GetByID(ctx context.Context, id domain.ID) (*domain.Wallet, error)

	Save(ctx context.Context, wallet *domain.Wallet) error
}