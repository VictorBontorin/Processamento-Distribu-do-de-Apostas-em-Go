package postgres

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"wager/internal/domain"
	"wager/internal/metrics"
)

// ProcessExternal é a entrada HTTP: mede latência, registra métricas e log.
func (s *WagerStore) ProcessExternal(
	ctx context.Context,
	txModel *domain.WagerTransaction,
	now time.Time,
) (ProcessResult, error) {
	start := time.Now()

	result, err := s.processExternal(ctx, txModel, now)

	metrics.ProcessingDuration.WithLabelValues("http").Observe(time.Since(start).Seconds())

	if err != nil {
		if isContentConflict(err) {
			metrics.DuplicatesTotal.WithLabelValues("http", "conflict").Inc()
		}

		return result, err
	}

	recordHandled(ctx, "http", result.Transaction, result.Replay)

	return result, nil
}

func isContentConflict(err error) bool {
	return errors.Is(err, ErrIdempotencyConflict) ||
		errors.Is(err, ErrExternalTransactionConflict) ||
		errors.Is(err, ErrInboxMessageHashMismatch)
}

// recordHandled registra o desfecho de uma operação (após o commit).
func recordHandled(
	ctx context.Context,
	source string,
	tx *domain.WagerTransaction,
	replay bool,
) {
	if tx == nil {
		return
	}

	state := string(tx.State())

	if replay {
		metrics.DuplicatesTotal.WithLabelValues(source, "replay").Inc()
	} else {
		metrics.TransactionsTotal.WithLabelValues(source, string(tx.Kind()), state).Inc()

		if tx.State() == domain.StateRejected {
			metrics.RejectionsTotal.WithLabelValues(string(tx.FailureCode())).Inc()
		}
	}

	slog.InfoContext(
		ctx,
		"wager transaction handled",
		"source", source,
		"transactionId", tx.ID().String(),
		"walletId", tx.WalletID().String(),
		"providerId", tx.ProviderID(),
		"externalTransactionId", tx.ExternalTransactionID(),
		"kind", string(tx.Kind()),
		"status", state,
		"failureCode", string(tx.FailureCode()),
		"replay", replay,
	)
}

// recordConflict contabiliza conflitos de concorrência do PostgreSQL.
func recordConflict(err error) {
	if err == nil {
		return
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return
	}

	switch pgErr.Code {
	case "40001":
		metrics.ConcurrencyConflicts.WithLabelValues("serialization_failure").Inc()
	case "40P01":
		metrics.ConcurrencyConflicts.WithLabelValues("deadlock").Inc()
	case "23505":
		metrics.ConcurrencyConflicts.WithLabelValues("unique_violation").Inc()
	}
}
