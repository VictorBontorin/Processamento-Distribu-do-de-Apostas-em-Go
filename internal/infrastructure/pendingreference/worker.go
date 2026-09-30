package pendingreference

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"wager/internal/infrastructure/postgres"
)

type Worker struct {
	store    *postgres.WagerStore
	interval time.Duration
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewWorker(store *postgres.WagerStore) *Worker {
	return &Worker{
		store:    store,
		interval: 500 * time.Millisecond,
	}
}

// Start inicia o loop em segundo plano. O contexto recebido do Fx só vale
// durante a inicialização, então o worker usa um contexto próprio,
// cancelado em Stop.
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(context.WithoutCancel(ctx))

	w.wg.Add(1)

	go func() {
		defer w.wg.Done()

		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			if err := w.processBatch(ctx); err != nil {
				// O worker não pode morrer por causa de uma única
				// falha transitória.
				if ctx.Err() == nil {
					slog.ErrorContext(ctx, "pending reference batch failed", "error", err)
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (w *Worker) Stop(ctx context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}

	done := make(chan struct{})

	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) processBatch(ctx context.Context) error {
	transactions, err := w.store.ClaimPendingReferences(
		ctx,
		10,
		60*time.Second,
	)
	if err != nil {
		return err
	}

	for _, tx := range transactions {
		if err := w.store.ProcessPendingReference(ctx, tx); err != nil {
			// A própria transação decide se a falha é terminal
			// ou se deve permanecer pendente.
			slog.WarnContext(
				ctx,
				"pending reference processing failed",
				"transactionId", tx.TransactionID.String(),
				"error", err,
			)
		}
	}

	return nil
}
