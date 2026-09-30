package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wager/internal/application"
	"wager/internal/domain"
)

type WagerStore struct {
	uow *WagerUnitOfWork
}

func NewWagerStore(uow *WagerUnitOfWork) *WagerStore {
	return &WagerStore{uow: uow}
}

type ProcessResult struct {
	Transaction *domain.WagerTransaction
	Balance     domain.Money
	Replay      bool

	// Pending indica operação aceita, mas ainda aguardando a referência;
	// nesse caso não há saldo observado.
	Pending bool
}

// replayResult monta o resultado de um replay a partir do snapshot
// persistido. Operações ainda pendentes não têm snapshot.
func replayResult(
	existing *domain.WagerTransaction,
	snapshot TransactionResult,
) (ProcessResult, error) {
	if snapshot.HasValue {
		return ProcessResult{
			Transaction: existing,
			Balance:     snapshot.Balance,
			Replay:      true,
		}, nil
	}

	if existing.State() == domain.StatePending ||
		existing.State() == domain.StatePendingReference {
		return ProcessResult{
			Transaction: existing,
			Replay:      true,
			Pending:     true,
		}, nil
	}

	return ProcessResult{}, fmt.Errorf(
		"result snapshot missing for transaction %s",
		existing.ID(),
	)
}

var ErrIdempotencyConflict = fmt.Errorf(
	"%w: idempotency conflict",
	application.ErrPermanent,
)

var ErrExternalTransactionConflict = fmt.Errorf(
	"%w: external transaction conflict",
	application.ErrPermanent,
)

func (s *WagerStore) ProcessExternal(
	ctx context.Context,
	txModel *domain.WagerTransaction,
	now time.Time,
) (ProcessResult, error) {
	var result ProcessResult

	err := s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		/*
			Primeiro verificamos se a Idempotency-Key já existe.

			Se existir com conteúdo diferente, é conflito.
			Se existir com o mesmo conteúdo, é replay.
		*/
		if txModel.IdempotencyKey() != "" &&
			txModel.ProviderID() != "" {

			existing, err := r.Transactions.GetByIdempotencyKey(
				ctx,
				txModel.ProviderID(),
				txModel.IdempotencyKey(),
			)

			if err == nil {
				if existing.PayloadHash() != txModel.PayloadHash() {
					return ErrIdempotencyConflict
				}

				snapshot, err := r.Transactions.GetResultSnapshot(
					ctx,
					existing.ID(),
				)
				if err != nil {
					return fmt.Errorf(
						"get replay result: %w",
						err,
					)
				}

				replayed, err := replayResult(existing, snapshot)
				if err != nil {
					return err
				}

				result = replayed

				return nil
			}

			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf(
					"check idempotency: %w",
					err,
				)
			}
		}

		/*
			Agora verificamos o externalTransactionId.

			Isso impede que a mesma operação financeira seja
			reaplicada com outra Idempotency-Key.
		*/
		if txModel.ExternalTransactionID() != "" &&
			txModel.ProviderID() != "" {

			existing, err := r.Transactions.GetByExternalID(
				ctx,
				txModel.ProviderID(),
				txModel.ExternalTransactionID(),
			)

			if err == nil {
				if existing.IdempotencyKey() != txModel.IdempotencyKey() {
					return ErrExternalTransactionConflict
				}

				if existing.PayloadHash() != txModel.PayloadHash() {
					return ErrExternalTransactionConflict
				}

				snapshot, err := r.Transactions.GetResultSnapshot(
					ctx,
					existing.ID(),
				)
				if err != nil {
					return fmt.Errorf(
						"get external transaction result: %w",
						err,
					)
				}

				replayed, err := replayResult(existing, snapshot)
				if err != nil {
					return err
				}

				result = replayed

				return nil
			}

			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf(
					"check external transaction: %w",
					err,
				)
			}
		}

		/*
			Agora fazemos a reserva concorrente.

			CreateIfAbsent usa:

				INSERT ... ON CONFLICT DO NOTHING

			Isso é importante porque duas requisições podem chegar
			exatamente ao mesmo tempo.

			A primeira cria a transação.
			A segunda não cria uma segunda transação e recupera
			a primeira.
		*/
		created, err := r.Transactions.CreateIfAbsent(
			ctx,
			txModel,
		)
		if err != nil {
			return fmt.Errorf(
				"reserve transaction: %w",
				err,
			)
		}

		if !created {
			/*
				Outra requisição criou a mesma operação enquanto
				esta requisição estava concorrendo.

				Primeiro tentamos localizar pela Idempotency-Key.
			*/
			if txModel.IdempotencyKey() != "" &&
				txModel.ProviderID() != "" {

				existing, err := r.Transactions.GetByIdempotencyKey(
					ctx,
					txModel.ProviderID(),
					txModel.IdempotencyKey(),
				)

				if err == nil {
					if existing.PayloadHash() != txModel.PayloadHash() {
						return ErrIdempotencyConflict
					}

					snapshot, err := r.Transactions.GetResultSnapshot(
						ctx,
						existing.ID(),
					)
					if err != nil {
						return fmt.Errorf(
							"get concurrent replay result: %w",
							err,
						)
					}

					replayed, err := replayResult(existing, snapshot)
					if err != nil {
						return err
					}

					result = replayed

					return nil
				}

				if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf(
						"get concurrent idempotency transaction: %w",
						err,
					)
				}
			}

			/*
				Se não foi encontrada pela Idempotency-Key,
				procuramos pelo externalTransactionId.
			*/
			if txModel.ExternalTransactionID() != "" &&
				txModel.ProviderID() != "" {

				existing, err := r.Transactions.GetByExternalID(
					ctx,
					txModel.ProviderID(),
					txModel.ExternalTransactionID(),
				)

				if err == nil {
					if existing.IdempotencyKey() != txModel.IdempotencyKey() {
						return ErrExternalTransactionConflict
					}

					if existing.PayloadHash() != txModel.PayloadHash() {
						return ErrExternalTransactionConflict
					}

					snapshot, err := r.Transactions.GetResultSnapshot(
						ctx,
						existing.ID(),
					)
					if err != nil {
						return fmt.Errorf(
							"get concurrent external result: %w",
							err,
						)
					}

					replayed, err := replayResult(existing, snapshot)
					if err != nil {
						return err
					}

					result = replayed

					return nil
				}

				if !errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf(
						"get concurrent external transaction: %w",
						err,
					)
				}
			}

			return fmt.Errorf(
				"transaction conflict could not be resolved",
			)
		}

		/*
			Somente quem conseguiu criar a transação chega aqui.

			Portanto somente essa requisição pode executar a
			movimentação financeira.
		*/
		if err := s.processInTransaction(
			ctx,
			r,
			txModel,
			now,
		); err != nil {
			return err
		}

		// Aguardando a referência: aceito, sem saldo observado ainda.
		if txModel.State() == domain.StatePending ||
			txModel.State() == domain.StatePendingReference {
			result = ProcessResult{
				Transaction: txModel,
				Pending:     true,
			}

			return nil
		}

		snapshot, err := r.Transactions.GetResultSnapshot(
			ctx,
			txModel.ID(),
		)
		if err != nil {
			return fmt.Errorf(
				"get transaction result: %w",
				err,
			)
		}

		/*
			Alguns caminhos de rejeição não movimentam a carteira.

			Nesses casos, garantimos que também exista um snapshot
			para que um replay consiga devolver o saldo observado.
		*/
		if !snapshot.HasValue {
			wallet, err := r.Wallet.GetByIDForUpdate(
				ctx,
				txModel.WalletID(),
			)
			if err != nil {
				return fmt.Errorf(
					"get wallet for result snapshot: %w",
					err,
				)
			}

			if err := r.Transactions.SaveResultSnapshot(
				ctx,
				txModel.ID(),
				wallet.Balance(),
			); err != nil {
				return err
			}

			snapshot = TransactionResult{
				Balance:  wallet.Balance(),
				HasValue: true,
			}
		}

		result = ProcessResult{
			Transaction: txModel,
			Balance:     snapshot.Balance,
			Replay:      false,
		}

		return nil
	})

	if err != nil {
		return ProcessResult{}, err
	}

	return result, nil
}

var _ interface {
	Process(
		context.Context,
		*domain.WagerTransaction,
		time.Time,
	) error

	ProcessReversal(
		context.Context,
		*domain.WagerTransaction,
		time.Time,
	) error

	ProcessMessage(
		context.Context,
		string,
		string,
		string,
		*domain.WagerTransaction,
		time.Time,
	) error
} = (*WagerStore)(nil)

func (s *WagerStore) Process(
	ctx context.Context,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	return s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		return s.processExternalTransaction(
			ctx,
			r,
			txModel,
			now,
		)
	})
}

func (s *WagerStore) ProcessMessage(
	ctx context.Context,
	consumerName string,
	messageID string,
	messageHash string,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	return s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		shouldProcess, err := r.Inbox.Register(
			ctx,
			consumerName,
			messageID,
			messageHash,
			now,
		)
		if err != nil {
			return err
		}

		if !shouldProcess {
			return nil
		}

		if err := s.processExternalTransaction(
			ctx,
			r,
			txModel,
			now,
		); err != nil {
			return err
		}

		if err := r.Inbox.Complete(
			ctx,
			consumerName,
			messageID,
			now,
		); err != nil {
			return err
		}

		return nil
	})
}

func (s *WagerStore) processExternalTransaction(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	/*
		Primeiro verificamos a Idempotency-Key.
	*/
	if txModel.IdempotencyKey() != "" &&
		txModel.ProviderID() != "" {

		existing, err := r.Transactions.GetByIdempotencyKey(
			ctx,
			txModel.ProviderID(),
			txModel.IdempotencyKey(),
		)

		if err == nil {
			if existing.PayloadHash() != txModel.PayloadHash() {
				return ErrIdempotencyConflict
			}

			return nil
		}

		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf(
				"check idempotency: %w",
				err,
			)
		}
	}

	/*
		Verificamos também o externalTransactionId.
	*/
	if txModel.ExternalTransactionID() != "" &&
		txModel.ProviderID() != "" {

		existing, err := r.Transactions.GetByExternalID(
			ctx,
			txModel.ProviderID(),
			txModel.ExternalTransactionID(),
		)

		if err == nil {
			if existing.IdempotencyKey() != txModel.IdempotencyKey() {
				return ErrExternalTransactionConflict
			}

			if existing.PayloadHash() != txModel.PayloadHash() {
				return ErrExternalTransactionConflict
			}

			return nil
		}

		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf(
				"check external transaction: %w",
				err,
			)
		}
	}

	/*
		Reserva concorrente.

		Somente uma requisição consegue criar a transação.
	*/
	created, err := r.Transactions.CreateIfAbsent(
		ctx,
		txModel,
	)
	if err != nil {
		return fmt.Errorf(
			"reserve transaction: %w",
			err,
		)
	}

	if !created {
		/*
			A transação já foi criada por outra requisição.

			Se for a mesma Idempotency-Key + mesmo payload,
			é replay.
		*/
		if txModel.IdempotencyKey() != "" &&
			txModel.ProviderID() != "" {

			existing, err := r.Transactions.GetByIdempotencyKey(
				ctx,
				txModel.ProviderID(),
				txModel.IdempotencyKey(),
			)

			if err == nil {
				if existing.PayloadHash() != txModel.PayloadHash() {
					return ErrIdempotencyConflict
				}

				return nil
			}

			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf(
					"get concurrent idempotency transaction: %w",
					err,
				)
			}
		}

		/*
			Se não encontramos pela Idempotency-Key,
			tentamos pelo externalTransactionId.
		*/
		if txModel.ExternalTransactionID() != "" &&
			txModel.ProviderID() != "" {

			existing, err := r.Transactions.GetByExternalID(
				ctx,
				txModel.ProviderID(),
				txModel.ExternalTransactionID(),
			)

			if err == nil {
				if existing.IdempotencyKey() != txModel.IdempotencyKey() {
					return ErrExternalTransactionConflict
				}

				if existing.PayloadHash() != txModel.PayloadHash() {
					return ErrExternalTransactionConflict
				}

				return nil
			}

			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf(
					"get concurrent external transaction: %w",
					err,
				)
			}
		}

		return fmt.Errorf(
			"transaction conflict could not be resolved",
		)
	}

	/*
		Somente o vencedor da criação executa a movimentação financeira.
	*/
	return s.processInTransaction(
		ctx,
		r,
		txModel,
		now,
	)
}

func (s *WagerStore) processInTransaction(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	switch txModel.Kind() {
	case domain.TransactionRefund, domain.TransactionRollback:
		return s.processReversalInTransaction(
			ctx,
			r,
			txModel,
			now,
		)

	default:
		return s.processTransaction(
			ctx,
			r,
			txModel,
			now,
		)
	}
}

func (s *WagerStore) processTransaction(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	wallet, err := r.Wallet.GetByIDForUpdate(
		ctx,
		txModel.WalletID(),
	)
	if err != nil {
		return fmt.Errorf("get wallet: %w", err)
	}

	var entry domain.LedgerEntry
	hasEntry := false

	switch txModel.Kind() {
	case domain.TransactionBet:
		entry, err = wallet.Debit(
			txModel.ID(),
			txModel.Money(),
			now,
		)
		hasEntry = err == nil

	case domain.TransactionWin:
		entry, err = wallet.Credit(
			txModel.ID(),
			txModel.Money(),
			now,
		)
		hasEntry = err == nil

	case domain.TransactionLoss:
		if !txModel.Money().IsZero() {
			return fmt.Errorf("loss must have zero amount")
		}

	default:
		return fmt.Errorf(
			"transaction kind %s is not supported",
			txModel.Kind(),
		)
	}

	if err != nil {
		if rej, ok := err.(*domain.RejectionError); ok {
			if rejectErr := txModel.Reject(
				rej.Code,
				now,
			); rejectErr != nil {
				return rejectErr
			}

			if err := r.Transactions.Save(
				ctx,
				txModel,
			); err != nil {
				return fmt.Errorf(
					"save rejected transaction: %w",
					err,
				)
			}

			if err := r.Transactions.SaveResultSnapshot(
				ctx,
				txModel.ID(),
				wallet.Balance(),
			); err != nil {
				return err
			}

			return r.Outbox.Save(ctx, transactionRejectedEvent(txModel, now))
		}

		_ = txModel.Fail(
			domain.FailureCode("WALLET_ERROR"),
			now,
		)

		return err
	}

	if err := txModel.Process(now); err != nil {
		return err
	}

	if hasEntry {
		if err := r.Wallet.Save(
			ctx,
			wallet,
		); err != nil {
			return fmt.Errorf(
				"save wallet: %w",
				err,
			)
		}

		if err := r.Ledger.Save(
			ctx,
			&entry,
		); err != nil {
			return fmt.Errorf(
				"save ledger: %w",
				err,
			)
		}

		if err := r.Outbox.Save(ctx, walletBalanceChangedEvent(wallet, entry, txModel.ID(), now)); err != nil {
			return fmt.Errorf(
				"save wallet event: %w",
				err,
			)
		}
	}

	if err := r.Transactions.Save(
		ctx,
		txModel,
	); err != nil {
		return fmt.Errorf(
			"save transaction: %w",
			err,
		)
	}

	if err := r.Transactions.SaveResultSnapshot(
		ctx,
		txModel.ID(),
		wallet.Balance(),
	); err != nil {
		return err
	}

	return r.Outbox.Save(ctx, transactionProcessedEvent(txModel, wallet.Balance(), now))
}

func (s *WagerStore) ProcessReversal(
	ctx context.Context,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	return s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		return s.processExternalTransaction(
			ctx,
			r,
			txModel,
			now,
		)
	})
}

func (s *WagerStore) processReversalInTransaction(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	reference, err := getReferenceForUpdate(
		ctx,
		r,
		txModel.ProviderID(),
		txModel.ReferenceExternalID(),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.waitForReference(ctx, r, txModel, now)
		}

		return fmt.Errorf(
			"get reference: %w",
			err,
		)
	}

	// Vincula a reversão à transação original. O índice único parcial
	// da migration 007 usa esta coluna como barreira final no banco.
	if err := txModel.SetReferenceTransactionID(reference.ID()); err != nil {
		return err
	}

	// Referência existe, mas ainda não terminou (PENDING ou
	// PENDING_REFERENCE): a reversão espera, com o mesmo TTL.
	if reference.State() == domain.StatePending ||
		reference.State() == domain.StatePendingReference {
		return s.waitForReference(ctx, r, txModel, now)
	}

	// Referência terminou sem sucesso (REJECTED ou FAILED): rejeição
	// definitiva, pois ela nunca terá um movimento a ser revertido.
	if reference.State() != domain.StateProcessed {
		return s.rejectInTransaction(
			ctx, r, txModel, domain.CodeReferenceNotProcessed, now,
		)
	}

	wallet, err := r.Wallet.GetByIDForUpdate(
		ctx,
		txModel.WalletID(),
	)
	if err != nil {
		return fmt.Errorf(
			"get wallet: %w",
			err,
		)
	}

	if wallet.ID() != reference.WalletID() ||
		txModel.PlayerID() != reference.PlayerID() ||
		txModel.Money().Currency() != reference.Money().Currency() ||
		txModel.Money().Units() != reference.Money().Units() ||
		txModel.RoundID() != reference.RoundID() {
		return s.rejectInTransaction(
			ctx, r, txModel, domain.CodeInvalidReversal, now,
		)
	}

	// A linha da referência está travada (FOR UPDATE), então reversões
	// concorrentes da mesma referência são serializadas aqui. Política:
	// cada transação original aceita no máximo UMA reversão processada,
	// seja REFUND ou ROLLBACK, para nunca devolver o mesmo débito duas vezes.
	duplicate, err := hasProcessedReversal(
		ctx,
		r,
		reference.ID(),
		txModel.ID(),
	)
	if err != nil {
		return fmt.Errorf("check duplicate reversal: %w", err)
	}

	if duplicate {
		return s.rejectInTransaction(
			ctx, r, txModel, domain.CodeDuplicateReversal, now,
		)
	}

	var entry domain.LedgerEntry
	var movementErr error
	hasEntry := false

	switch {
	case txModel.Kind() == domain.TransactionRefund &&
		reference.Kind() == domain.TransactionBet:

		entry, movementErr = wallet.Credit(
			txModel.ID(),
			txModel.Money(),
			now,
		)
		hasEntry = movementErr == nil

	case txModel.Kind() == domain.TransactionRollback &&
		reference.Kind() == domain.TransactionBet:

		entry, movementErr = wallet.Credit(
			txModel.ID(),
			txModel.Money(),
			now,
		)
		hasEntry = movementErr == nil

	case txModel.Kind() == domain.TransactionRollback &&
		(reference.Kind() == domain.TransactionWin ||
			reference.Kind() == domain.TransactionRefund):

		// Desfaz um crédito anterior (WIN ou REFUND) com um débito.
		entry, movementErr = wallet.Debit(
			txModel.ID(),
			txModel.Money(),
			now,
		)
		hasEntry = movementErr == nil

	default:
		return s.rejectInTransaction(
			ctx, r, txModel, domain.CodeInvalidReversal, now,
		)
	}

	if movementErr != nil {
		var rej *domain.RejectionError
		if errors.As(movementErr, &rej) {
			code := rej.Code

			// Reversão que não cabe no saldo tem código próprio,
			// distinto do de uma aposta sem saldo.
			if code == domain.CodeInsufficientFunds {
				code = domain.CodeReversalInsufficientFunds
			}

			return s.rejectInTransaction(ctx, r, txModel, code, now)
		}

		_ = txModel.Fail(
			domain.FailureCode("WALLET_ERROR"),
			now,
		)

		return movementErr
	}

	if err := txModel.Process(now); err != nil {
		return err
	}

	if hasEntry {
		if err := r.Wallet.Save(
			ctx,
			wallet,
		); err != nil {
			return fmt.Errorf(
				"save wallet: %w",
				err,
			)
		}

		if err := r.Ledger.Save(
			ctx,
			&entry,
		); err != nil {
			return fmt.Errorf(
				"save ledger: %w",
				err,
			)
		}

		if err := r.Outbox.Save(ctx, walletBalanceChangedEvent(wallet, entry, txModel.ID(), now)); err != nil {
			return fmt.Errorf(
				"save wallet event: %w",
				err,
			)
		}
	}

	if err := r.Transactions.Save(
		ctx,
		txModel,
	); err != nil {
		return fmt.Errorf(
			"save transaction: %w",
			err,
		)
	}

	if err := r.Transactions.SaveResultSnapshot(
		ctx,
		txModel.ID(),
		wallet.Balance(),
	); err != nil {
		return err
	}

	return r.Outbox.Save(ctx, transactionProcessedEvent(txModel, wallet.Balance(), now))
}

// Política de referências pendentes:
//   - a operação espera até pendingReferenceTTL (5 min) pela referência;
//   - o worker tenta de novo com backoff exponencial (1s, 2s, 4s ... 60s);
//   - expirado o TTL, a operação vira REJECTED com REFERENCE_NOT_FOUND
//     (referência nunca apareceu) ou REFERENCE_NOT_PROCESSED (a
//     referência existia, mas seguiu pendente até expirar).
const (
	pendingReferenceTTL        = 5 * time.Minute
	pendingReferenceMaxBackoff = 60 * time.Second
)

// waitForReference coloca a operação em PENDING_REFERENCE e agenda a
// retomada durável pelo worker. Se ela já estiver esperando (retomada
// pelo worker), mantém o estado sem repetir o evento.
func (s *WagerStore) waitForReference(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	now time.Time,
) error {
	if txModel.State() == domain.StatePendingReference {
		return nil
	}

	if err := txModel.WaitForReference(now); err != nil {
		return err
	}

	if err := r.Transactions.Save(ctx, txModel); err != nil {
		return fmt.Errorf("save pending reference transaction: %w", err)
	}

	if _, err := r.Wallet.tx.Exec(
		ctx,
		`
		UPDATE wager_transactions
		SET
			reference_attempts = 0,
			reference_next_attempt_at = NOW(),
			reference_expires_at = NOW() + make_interval(secs => $2::float8),
			updated_at = NOW()
		WHERE id = $1
		`,
		txModel.ID().String(),
		pendingReferenceTTL.Seconds(),
	); err != nil {
		return fmt.Errorf("schedule pending reference: %w", err)
	}

	return r.Outbox.Save(ctx, OutboxEvent{
		ID:          domain.NewID(),
		EventType:   "WagerTransactionPendingReference",
		AggregateID: txModel.ID(),
		CausationID: txModel.ID(),
		OccurredAt:  now,
		Version:     1,
		Payload: map[string]any{
			"transactionId":                  txModel.ID(),
			"state":                          txModel.State(),
			"referenceExternalTransactionId": txModel.ReferenceExternalID(),
		},
	})
}

// rejectInTransaction registra a rejeição de negócio e o evento
// WagerTransactionRejected na mesma transação SQL.
func (s *WagerStore) rejectInTransaction(
	ctx context.Context,
	r *WagerTransactionContext,
	txModel *domain.WagerTransaction,
	code domain.FailureCode,
	now time.Time,
) error {
	if err := txModel.Reject(code, now); err != nil {
		return err
	}

	if err := r.Transactions.Save(ctx, txModel); err != nil {
		return fmt.Errorf("save rejected transaction: %w", err)
	}

	// Guarda o saldo observado para que um replay posterior (HTTP ou
	// SQS) devolva o mesmo resultado, mesmo quando a rejeição ocorre
	// fora do caminho HTTP (SQS ou worker de referências).
	wallet, err := r.Wallet.GetByIDForUpdate(ctx, txModel.WalletID())
	if err != nil {
		return fmt.Errorf("get wallet for rejection snapshot: %w", err)
	}

	if err := r.Transactions.SaveResultSnapshot(
		ctx,
		txModel.ID(),
		wallet.Balance(),
	); err != nil {
		return err
	}

	return r.Outbox.Save(ctx, transactionRejectedEvent(txModel, now))
}

// hasProcessedReversal informa se a transação de referência já recebeu
// uma reversão (REFUND ou ROLLBACK) processada com sucesso.
func hasProcessedReversal(
	ctx context.Context,
	r *WagerTransactionContext,
	referenceID domain.ID,
	excludeID domain.ID,
) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM wager_transactions
			WHERE reference_transaction_id = $1
			  AND id <> $2
			  AND kind IN ('REFUND', 'ROLLBACK')
			  AND state = 'PROCESSED'
		)
	`

	var exists bool

	if err := r.Wallet.tx.QueryRow(
		ctx,
		query,
		referenceID.String(),
		excludeID.String(),
	).Scan(&exists); err != nil {
		return false, err
	}

	return exists, nil
}

func getReferenceForUpdate(
	ctx context.Context,
	r *WagerTransactionContext,
	providerID string,
	externalID string,
) (*domain.WagerTransaction, error) {
	const query = `
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount,
			currency,
			reference_external_id,
			reference_transaction_id,
			state,
			failure_code,
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND external_transaction_id = $2
		FOR UPDATE
	`

	row := r.Wallet.tx.QueryRow(
		ctx,
		query,
		providerID,
		externalID,
	)

	var (
		id                     string
		externalTxID           *string
		provider               *string
		idempotency            *string
		payloadHash            *string
		walletID               string
		playerID               string
		roundID                *string
		gameID                 *string
		referenceExternalID    *string
		referenceTransactionID *string
		kind                   string
		currency               string
		state                  string
		amount                 int64
		failureCode            *string
		createdAt              time.Time
		updatedAt              time.Time
	)

	err := row.Scan(
		&id,
		&externalTxID,
		&provider,
		&idempotency,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&amount,
		&currency,
		&referenceExternalID,
		&referenceTransactionID,
		&state,
		&failureCode,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	parsedID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}

	parsedWalletID, err := domain.ParseID(walletID)
	if err != nil {
		return nil, err
	}

	parsedPlayerID, err := domain.ParseID(playerID)
	if err != nil {
		return nil, err
	}

	parsedCurrency, err := domain.ParseCurrency(currency)
	if err != nil {
		return nil, err
	}

	money, err := domain.FromUnits(
		amount,
		parsedCurrency,
	)
	if err != nil {
		return nil, err
	}

	var referenceID domain.ID

	if referenceTransactionID != nil {
		referenceID, err = domain.ParseID(
			*referenceTransactionID,
		)
		if err != nil {
			return nil, err
		}
	}

	var failure domain.FailureCode

	if failureCode != nil {
		failure = domain.FailureCode(*failureCode)
	}

	return domain.RehydrateWagerTransaction(
		parsedID,
		deref(externalTxID),
		deref(provider),
		deref(idempotency),
		deref(payloadHash),
		parsedWalletID,
		parsedPlayerID,
		deref(roundID),
		deref(gameID),
		domain.TransactionKind(kind),
		money,
		deref(referenceExternalID),
		referenceID,
		domain.TransactionState(state),
		failure,
		createdAt,
		updatedAt,
	)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

type PendingReference struct {
	TransactionID domain.ID
	Attempts      int
	NextAttemptAt time.Time
	ExpiresAt     *time.Time
}

func (s *WagerStore) ClaimPendingReferences(

	ctx context.Context,
	limit int,
	maxBackoff time.Duration,
) ([]PendingReference, error) {
	var result []PendingReference

	err := s.uow.Run(ctx, func(tx *WagerTransactionContext) error {
		const query = `
			WITH candidates AS (
				SELECT id
				FROM wager_transactions
				WHERE state = 'PENDING_REFERENCE'
				  AND reference_next_attempt_at <= NOW()
				ORDER BY reference_next_attempt_at, created_at
				FOR UPDATE SKIP LOCKED
				LIMIT $1
			)
			UPDATE wager_transactions wt
			SET
				reference_attempts = wt.reference_attempts + 1,
				reference_next_attempt_at = NOW() + make_interval(
					secs => LEAST(
						POWER(2::float8, LEAST(wt.reference_attempts, 20)::float8),
						$2::float8
					)
				),
				updated_at = NOW()
			FROM candidates c
			WHERE wt.id = c.id
			RETURNING
				wt.id,
				wt.reference_attempts,
				wt.reference_next_attempt_at,
				wt.reference_expires_at
		`

		rows, err := tx.Wallet.tx.Query(
			ctx,
			query,
			limit,
			maxBackoff.Seconds(),
		)
		if err != nil {
			return fmt.Errorf("claim pending references: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				id        string
				attempts  int
				next      time.Time
				expiresAt *time.Time
			)

			if err := rows.Scan(
				&id,
				&attempts,
				&next,
				&expiresAt,
			); err != nil {
				return fmt.Errorf("scan pending reference: %w", err)
			}

			parsedID, err := domain.ParseID(id)
			if err != nil {
				return fmt.Errorf("parse pending reference id: %w", err)
			}

			result = append(result, PendingReference{
				TransactionID: parsedID,
				Attempts:      attempts,
				NextAttemptAt: next,
				ExpiresAt:     expiresAt,
			})
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate pending references: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *WagerStore) ProcessPendingReference(
	ctx context.Context,
	pending PendingReference,
) error {
	return s.uow.Run(ctx, func(r *WagerTransactionContext) error {
		// Trava a linha: outra instância não processa a mesma pendência
		// ao mesmo tempo, e o estado lido aqui é o definitivo.
		var (
			state   string
			expired bool
		)

		if err := r.Wallet.tx.QueryRow(
			ctx,
			`
			SELECT
				state,
				COALESCE(reference_expires_at <= NOW(), FALSE)
			FROM wager_transactions
			WHERE id = $1
			FOR UPDATE
			`,
			pending.TransactionID.String(),
		).Scan(&state, &expired); err != nil {
			return fmt.Errorf("lock pending reference transaction: %w", err)
		}

		// Outra instância pode ter concluído a transação antes.
		if domain.TransactionState(state) != domain.StatePendingReference {
			return nil
		}

		txModel, err := r.Transactions.GetByID(
			ctx,
			pending.TransactionID,
		)
		if err != nil {
			return fmt.Errorf("get pending reference transaction: %w", err)
		}

		now := time.Now().UTC()

		reference, err := getReferenceForUpdate(
			ctx,
			r,
			txModel.ProviderID(),
			txModel.ReferenceExternalID(),
		)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Referência ainda não chegou.
			if expired {
				return s.rejectInTransaction(
					ctx, r, txModel, domain.CodeReferenceNotFound, now,
				)
			}

			return nil

		case err != nil:
			return fmt.Errorf("get pending reference: %w", err)

		case reference.State() == domain.StatePending ||
			reference.State() == domain.StatePendingReference:
			// Referência existe, mas ainda não terminou.
			if expired {
				return s.rejectInTransaction(
					ctx, r, txModel, domain.CodeReferenceNotProcessed, now,
				)
			}

			return nil
		}

		// Referência resolvida (processada ou terminada sem sucesso):
		// a lógica normal decide entre movimentar e rejeitar.
		return s.processInTransaction(ctx, r, txModel, now)
	})
}
