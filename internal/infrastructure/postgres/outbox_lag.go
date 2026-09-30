package postgres

import (
	"context"
	"fmt"
)

// OldestPendingAgeSeconds devolve a idade (em segundos) do evento mais
// antigo ainda não publicado; 0 quando a outbox está vazia.
func (r *OutboxRepository) OldestPendingAgeSeconds(ctx context.Context) (float64, error) {
	var age float64

	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at))), 0)::float8
		FROM outbox_events
		WHERE published_at IS NULL
	`).Scan(&age)
	if err != nil {
		return 0, fmt.Errorf("outbox lag: %w", err)
	}

	if age < 0 {
		age = 0
	}

	return age, nil
}
