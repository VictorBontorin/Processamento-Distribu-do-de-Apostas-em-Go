package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wager/internal/correlation"
	"wager/internal/domain"
)

type OutboxEvent struct {
	ID            domain.ID
	EventType     string
	AggregateID   domain.ID
	CorrelationID domain.ID
	CausationID   domain.ID
	OccurredAt    time.Time
	Version       int64
	Payload       any
}

type OutboxTxRepository struct {
	tx pgx.Tx
}

func NewOutboxTxRepository(tx pgx.Tx) *OutboxTxRepository {
	return &OutboxTxRepository{tx: tx}
}

func (r *OutboxTxRepository) Save(
	ctx context.Context,
	event OutboxEvent,
) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	const query = `
		INSERT INTO outbox_events (
			id,
			event_type,
			aggregate_id,
			correlation_id,
			causation_id,
			occurred_at,
			version,
			payload,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	now := time.Now().UTC()

	if event.CorrelationID.IsZero() {
		event.CorrelationID = correlation.FromContext(ctx)
	}

	var correlationID *string
	if !event.CorrelationID.IsZero() {
		value := event.CorrelationID.String()
		correlationID = &value
	}

	var causationID *string
	if !event.CausationID.IsZero() {
		value := event.CausationID.String()
		causationID = &value
	}

	_, err = r.tx.Exec(
		ctx,
		query,
		event.ID.String(),
		event.EventType,
		event.AggregateID.String(),
		correlationID,
		causationID,
		event.OccurredAt,
		event.Version,
		payload,
		now,
	)
	if err != nil {
		return fmt.Errorf("save outbox event: %w", err)
	}

	return nil
}
