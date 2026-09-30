package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wager/internal/domain"
)

var ErrOutboxEventNotFound = errors.New("outbox event not found")

type PendingOutboxEvent struct {
	ID            domain.ID
	EventType     string
	AggregateID   domain.ID
	CorrelationID domain.ID
	CausationID   domain.ID
	OccurredAt    time.Time
	Version       int64
	Payload       json.RawMessage
	Attempts      int
}

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

func (r *OutboxRepository) ClaimNext(
	ctx context.Context,
	lease time.Duration,
) (*PendingOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const query = `
		WITH candidate AS (
			SELECT id
			FROM outbox_events
			WHERE published_at IS NULL
			  AND next_attempt_at <= NOW()
			  AND (
				locked_until IS NULL
				OR locked_until < NOW()
			  )
			ORDER BY next_attempt_at, occurred_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE outbox_events o
		SET
			locked_until = NOW() + $1::interval,
			attempts = attempts + 1
		FROM candidate c
		WHERE o.id = c.id
		RETURNING
			o.id,
			o.event_type,
			o.aggregate_id,
			o.correlation_id,
			o.causation_id,
			o.occurred_at,
			o.version,
			o.payload,
			o.attempts
	`

	var (
		id            string
		eventType     string
		aggregateID   string
		correlationID *string
		causationID   *string
		occurredAt    time.Time
		version       int64
		payload       []byte
		attempts      int
	)

	err = tx.QueryRow(ctx, query, lease.String()).Scan(
		&id,
		&eventType,
		&aggregateID,
		&correlationID,
		&causationID,
		&occurredAt,
		&version,
		&payload,
		&attempts,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, fmt.Errorf("claim outbox event: %w", err)
	}

	parsedID, err := domain.ParseID(id)
	if err != nil {
		return nil, fmt.Errorf("parse outbox event id: %w", err)
	}

	parsedAggregateID, err := domain.ParseID(aggregateID)
	if err != nil {
		return nil, fmt.Errorf("parse outbox aggregate id: %w", err)
	}

	event := &PendingOutboxEvent{
		ID:          parsedID,
		EventType:   eventType,
		AggregateID: parsedAggregateID,
		OccurredAt:  occurredAt,
		Version:     version,
		Payload:     json.RawMessage(payload),
		Attempts:    attempts,
	}

	if correlationID != nil {
		event.CorrelationID, err = domain.ParseID(*correlationID)
		if err != nil {
			return nil, fmt.Errorf("parse outbox correlation id: %w", err)
		}
	}

	if causationID != nil {
		event.CausationID, err = domain.ParseID(*causationID)
		if err != nil {
			return nil, fmt.Errorf("parse outbox causation id: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit outbox claim: %w", err)
	}

	return event, nil
}

func (r *OutboxRepository) MarkPublished(
	ctx context.Context,
	id domain.ID,
) error {
	const query = `
		UPDATE outbox_events
		SET
			published_at = NOW(),
			locked_until = NULL
		WHERE id = $1
		  AND published_at IS NULL
	`

	tag, err := r.pool.Exec(ctx, query, id.String())
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"mark outbox event published: event %s not found or already published",
			id,
		)
	}

	return nil
}

func (r *OutboxRepository) ScheduleRetry(
	ctx context.Context,
	id domain.ID,
	delay time.Duration,
) error {
	const query = `
		UPDATE outbox_events
		SET
			next_attempt_at = NOW() + $2::interval,
			locked_until = NULL
		WHERE id = $1
		  AND published_at IS NULL
	`

	tag, err := r.pool.Exec(
		ctx,
		query,
		id.String(),
		delay.String(),
	)
	if err != nil {
		return fmt.Errorf("schedule outbox retry: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"schedule outbox retry: event %s not found or already published",
			id,
		)
	}

	return nil
}
