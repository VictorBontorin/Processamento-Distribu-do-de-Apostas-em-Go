package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"wager/internal/infrastructure/postgres"
	"wager/internal/infrastructure/sqs"
)

type EventEnvelope struct {
	EventID       string          `json:"eventId"`
	Type          string          `json:"type"`
	AggregateID   string          `json:"aggregateId"`
	CorrelationID string          `json:"correlationId,omitempty"`
	CausationID   string          `json:"causationId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Version       int64           `json:"version"`
	Data          json.RawMessage `json:"data"`
}

type Publisher struct {
	repository *postgres.OutboxRepository
	sqs        *sqs.Client
	lease      time.Duration
	interval   time.Duration
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func NewPublisher(
	repository *postgres.OutboxRepository,
	client *sqs.Client,
) *Publisher {
	return &Publisher{
		repository: repository,
		sqs:        client,
		lease:      30 * time.Second,
		interval:   500 * time.Millisecond,
	}
}

// Start inicia o loop em segundo plano. O contexto recebido do Fx só vale
// durante a inicialização, então o publisher usa um contexto próprio,
// cancelado em Stop.
func (p *Publisher) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(context.WithoutCancel(ctx))

	p.wg.Add(1)

	go func() {
		defer p.wg.Done()

		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			if err := p.publishOne(ctx); err != nil {
				// O retry fica persistido no PostgreSQL.
				// O worker continua processando outros eventos.
				_ = err
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (p *Publisher) Stop(ctx context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}

	done := make(chan struct{})

	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Publisher) publishOne(ctx context.Context) error {
	event, err := p.repository.ClaimNext(ctx, p.lease)
	if err != nil {
		return err
	}

	if event == nil {
		return nil
	}

	envelope := EventEnvelope{
		EventID:     event.ID.String(),
		Type:        event.EventType,
		AggregateID: event.AggregateID.String(),
		OccurredAt:  event.OccurredAt,
		Version:     event.Version,
		Data:        event.Payload,
	}

	if !event.CorrelationID.IsZero() {
		envelope.CorrelationID = event.CorrelationID.String()
	}

	if !event.CausationID.IsZero() {
		envelope.CausationID = event.CausationID.String()
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return p.retry(ctx, event, fmt.Errorf("marshal event envelope: %w", err))
	}

	err = p.sqs.SendEvent(
		ctx,
		string(body),
		event.AggregateID.String(),
		event.ID.String(),
	)
	if err != nil {
		return p.retry(ctx, event, err)
	}

	if err := p.repository.MarkPublished(ctx, event.ID); err != nil {
		return err
	}

	return nil
}

func (p *Publisher) retry(
	ctx context.Context,
	event *postgres.PendingOutboxEvent,
	cause error,
) error {
	delay := retryDelay(event.Attempts)

	if err := p.repository.ScheduleRetry(ctx, event.ID, delay); err != nil {
		return fmt.Errorf(
			"outbox publish failed: %w; schedule retry failed: %v",
			cause,
			err,
		)
	}

	return cause
}

func retryDelay(attempts int) time.Duration {
	if attempts <= 0 {
		return time.Second
	}

	const (
		base = time.Second
		max  = 5 * time.Minute
	)

	power := math.Pow(2, float64(attempts-1))
	delay := time.Duration(float64(base) * power)

	if delay > max {
		return max
	}

	return delay
}

var _ interface {
	Start(context.Context)
	Stop(context.Context) error
} = (*Publisher)(nil)
