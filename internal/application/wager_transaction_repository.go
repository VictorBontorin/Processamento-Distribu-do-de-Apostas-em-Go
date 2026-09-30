package application

import (
	"context"

	"wager/internal/domain"
)

type WagerTransactionRepository interface {
	GetByID(ctx context.Context, id domain.ID) (*domain.WagerTransaction, error)

	GetByIdempotencyKey(
		ctx context.Context,
		providerID string,
		idempotencyKey string,
	) (*domain.WagerTransaction, error)

	GetByExternalID(
		ctx context.Context,
		providerID string,
		externalTransactionID string,
	) (*domain.WagerTransaction, error)

	Save(ctx context.Context, tx *domain.WagerTransaction) error

	GetByReferenceExternalID(
	ctx context.Context,
	providerID string,
	referenceExternalID string,
) (*domain.WagerTransaction, error)
}
