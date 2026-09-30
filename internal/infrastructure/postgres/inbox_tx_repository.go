package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wager/internal/application"
)

var ErrInboxMessageHashMismatch = fmt.Errorf(
	"%w: inbox message hash mismatch",
	application.ErrPermanent,
)

type InboxMessage struct {
	ConsumerName string
	MessageID    string
	MessageHash  string
	ReceivedAt   time.Time
	CompletedAt  *time.Time
}

type InboxTxRepository struct {
	tx pgx.Tx
}

func NewInboxTxRepository(tx pgx.Tx) *InboxTxRepository {
	return &InboxTxRepository{tx: tx}
}

func (r *InboxTxRepository) Register(
	ctx context.Context,
	consumerName string,
	messageID string,
	messageHash string,
	now time.Time,
) (bool, error) {
	const query = `
		INSERT INTO inbox_messages (
			consumer_name,
			message_id,
			message_hash,
			received_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer_name, message_id)
		DO NOTHING
	`

	result, err := r.tx.Exec(
		ctx,
		query,
		consumerName,
		messageID,
		messageHash,
		now,
	)
	if err != nil {
		return false, fmt.Errorf("register inbox message: %w", err)
	}

	if result.RowsAffected() == 1 {
		return true, nil
	}

	var existingHash string
	var completedAt *time.Time

	const selectQuery = `
		SELECT
			message_hash,
			completed_at
		FROM inbox_messages
		WHERE consumer_name = $1
		  AND message_id = $2
		FOR UPDATE
	`

	err = r.tx.QueryRow(
		ctx,
		selectQuery,
		consumerName,
		messageID,
	).Scan(
		&existingHash,
		&completedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf(
				"check inbox message: %w",
				application.ErrNotFound,
			)
		}

		return false, fmt.Errorf("check inbox message: %w", err)
	}

	if existingHash != messageHash {
		return false, fmt.Errorf(
			"%w: message_id=%s",
			ErrInboxMessageHashMismatch,
			messageID,
		)
	}

	if completedAt != nil {
		return false, nil
	}

	return true, nil
}

func (r *InboxTxRepository) Complete(
	ctx context.Context,
	consumerName string,
	messageID string,
	now time.Time,
) error {
	const query = `
		UPDATE inbox_messages
		SET completed_at = $3
		WHERE consumer_name = $1
		  AND message_id = $2
		  AND completed_at IS NULL
	`

	_, err := r.tx.Exec(
		ctx,
		query,
		consumerName,
		messageID,
		now,
	)
	if err != nil {
		return fmt.Errorf("complete inbox message: %w", err)
	}

	return nil
}
