package sqs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"wager/internal/application"
	"wager/internal/correlation"
	"wager/internal/domain"
	"wager/internal/fault"
	"wager/internal/logging"
	"wager/internal/metrics"

	"go.uber.org/fx"
)

const consumerName = "wager-transaction-consumer"

type WagerProcessor interface {
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
}

type Consumer struct {
	client    *Client
	processor WagerProcessor

	cancelReceive context.CancelFunc
	workCtx       context.Context
	cancelWork    context.CancelFunc
	wg            sync.WaitGroup
}

func NewConsumer(
	lc fx.Lifecycle,
	client *Client,
	processor WagerProcessor,
) *Consumer {
	consumer := &Consumer{
		client:    client,
		processor: processor,
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			consumer.Start()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return consumer.Stop(ctx)
		},
	})

	return consumer
}

// Start inicia o loop de consumo. O contexto de recebimento e o de
// processamento são independentes: no shutdown, primeiro se para de buscar
// mensagens; o trabalho em andamento só é interrompido se estourar o prazo.
func (c *Consumer) Start() {
	receiveCtx, cancelReceive := context.WithCancel(context.Background())
	c.cancelReceive = cancelReceive

	c.workCtx, c.cancelWork = context.WithCancel(context.Background())

	c.wg.Add(1)

	go func() {
		defer c.wg.Done()
		c.loop(receiveCtx)
	}()
}

// Stop interrompe novas leituras e aguarda a mensagem em andamento até o
// prazo de ctx. Vencido o prazo, cancela o processamento (a transação SQL é
// revertida) e libera a mensagem para reentrega imediata.
func (c *Consumer) Stop(ctx context.Context) error {
	if c.cancelReceive != nil {
		c.cancelReceive()
	}

	done := make(chan struct{})

	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if c.cancelWork != nil {
			c.cancelWork()
		}

		return nil

	case <-ctx.Done():
		if c.cancelWork != nil {
			c.cancelWork()
		}

		<-done

		return ctx.Err()
	}
}

func (c *Consumer) loop(ctx context.Context) {
	slog.Info("SQS consumer started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("SQS consumer stopped")
			return
		default:
		}

		messages, err := c.client.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			slog.Error("SQS receive failed", "error", err)

			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}

			continue
		}

		for _, message := range messages {
			// Mensagens já lidas mas não iniciadas voltam à fila quando o
			// visibility timeout expira.
			if ctx.Err() != nil {
				c.release(message)
				continue
			}

			c.handle(message)
		}
	}
}

// handle trata uma mensagem e decide entre remover, enviar à DLQ ou
// agendar nova tentativa.
//
//   - sucesso (inclui rejeição de negócio confirmada): remove a mensagem
//     somente depois do commit;
//   - erro permanente (mensagem inválida, conflito de conteúdo): envia à
//     DLQ e remove da fila principal;
//   - erro transitório: mantém a mensagem, com backoff exponencial via
//     visibility timeout; esgotado MaxReceiveCount, o redrive do SQS a
//     move para a DLQ.
func (c *Consumer) handle(message Message) {
	ctx := logging.WithAttrs(
		c.workCtx,
		"sqsMessageId", message.ID,
		"messageId", peekMessageID(message.Body),
		"receiveCount", message.ReceiveCount,
	)

	err := c.processMessage(ctx, message)

	switch {
	case err == nil:
		metrics.SQSMessagesTotal.WithLabelValues("processed").Inc()
		// Ponto de injeção de falha dos testes: commit feito, mensagem
		// ainda não removida.
		fault.ExitIf("sqs_after_commit")
		c.remove(message)

	case errors.Is(err, application.ErrPermanent):
		dlqCtx, cancel := detached(5 * time.Second)
		defer cancel()

		if dlqErr := c.client.SendToDLQ(dlqCtx, message, err.Error()); dlqErr != nil {
			slog.Error(
				"SQS send to DLQ failed",
				"sqsMessageId", message.ID,
				"messageId", peekMessageID(message.Body),
				"error", dlqErr,
			)
			c.backoff(message)

			return
		}

		metrics.SQSMessagesTotal.WithLabelValues("dlq").Inc()
		metrics.SQSDLQTotal.WithLabelValues("permanent").Inc()

		slog.WarnContext(
			ctx,
			"SQS message sent to DLQ",
			"sqsMessageId", message.ID,
			"messageId", peekMessageID(message.Body),
			"error", err,
		)
		c.remove(message)

	case c.workCtx.Err() != nil:
		// Encerramento forçado durante o processamento.
		metrics.SQSMessagesTotal.WithLabelValues("released").Inc()
		c.release(message)

	default:
		metrics.SQSMessagesTotal.WithLabelValues("retry").Inc()
		metrics.SQSRetriesTotal.Inc()

		if message.ReceiveCount >= MaxReceiveCount {
			metrics.SQSDLQTotal.WithLabelValues("redrive_expected").Inc()
		}

		slog.WarnContext(
			ctx,
			"SQS message will be retried",
			"sqsMessageId", message.ID,
			"messageId", peekMessageID(message.Body),
			"receiveCount", message.ReceiveCount,
			"error", err,
		)
		c.backoff(message)
	}
}

func (c *Consumer) remove(message Message) {
	ctx, cancel := detached(5 * time.Second)
	defer cancel()

	if err := c.client.Delete(ctx, message.ReceiptHandle); err != nil {
		slog.Error(
			"SQS delete message failed",
			"sqsMessageId", message.ID,
			"messageId", peekMessageID(message.Body),
			"error", err,
		)
	}
}

// release devolve a mensagem à fila para reentrega imediata.
func (c *Consumer) release(message Message) {
	ctx, cancel := detached(5 * time.Second)
	defer cancel()

	if err := c.client.ChangeVisibility(ctx, message.ReceiptHandle, 0); err != nil {
		slog.Error(
			"SQS release message failed",
			"sqsMessageId", message.ID,
			"messageId", peekMessageID(message.Body),
			"error", err,
		)
	}
}

func (c *Consumer) backoff(message Message) {
	ctx, cancel := detached(5 * time.Second)
	defer cancel()

	seconds := retryDelaySeconds(message.ReceiveCount)

	if err := c.client.ChangeVisibility(ctx, message.ReceiptHandle, seconds); err != nil {
		slog.Error(
			"SQS change visibility failed",
			"sqsMessageId", message.ID,
			"messageId", peekMessageID(message.Body),
			"error", err,
		)
	}
}

// retryDelaySeconds: 2s, 4s, 8s ... limitado a 120s.
func retryDelaySeconds(receiveCount int) int32 {
	if receiveCount > 6 {
		receiveCount = 6
	}

	if receiveCount < 1 {
		receiveCount = 1
	}

	seconds := int32(1) << receiveCount
	if seconds > 120 {
		seconds = 120
	}

	return seconds
}

// detached cria um contexto com prazo que não é cancelado junto com o
// shutdown, para que remoção/liberação de mensagens ainda ocorram.
func detached(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

func permanent(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{application.ErrPermanent}, args...)...)
}

func (c *Consumer) processMessage(
	ctx context.Context,
	message Message,
) error {
	messageHash := sha256.Sum256([]byte(message.Body))
	hash := hex.EncodeToString(messageHash[:])

	envelope, err := DecodeEnvelope(message.Body)
	if err != nil {
		return permanent("invalid envelope: %v", err)
	}

	if envelope.Type != "WagerTransactionRequested" {
		return permanent("unsupported message type: %s", envelope.Type)
	}

	data, err := DecodeWagerTransaction(envelope.Data)
	if err != nil {
		return permanent("%v", err)
	}

	walletID, err := domain.ParseID(data.WalletID)
	if err != nil {
		return permanent("parse walletId: %v", err)
	}

	playerID, err := domain.ParseID(data.PlayerID)
	if err != nil {
		return permanent("parse playerId: %v", err)
	}

	money, err := domain.ParseMoney(
		data.Money.Amount,
		data.Money.Currency,
	)
	if err != nil {
		return permanent("parse money: %v", err)
	}

	kind := domain.TransactionKind(
		strings.ToUpper(strings.TrimSpace(data.Kind)),
	)

	transaction, err := domain.NewExternalTransaction(
		domain.NewID(),
		data.ExternalTransactionID,
		data.ProviderID,
		data.IdempotencyKey,
		"",
		walletID,
		playerID,
		data.RoundID,
		data.GameID,
		kind,
		money,
		data.ReferenceExternalTransactionID,
		envelope.OccurredAt,
	)
	if err != nil {
		return permanent("create transaction: %v", err)
	}

	// Mesmo hash canônico do caminho HTTP.
	transaction, err = application.WithPayloadHash(transaction)
	if err != nil {
		return permanent("hash transaction: %v", err)
	}

	ctx = correlation.WithID(ctx, domain.NewID())
	ctx = logging.WithAttrs(
		ctx,
		"walletId", walletID.String(),
		"providerId", data.ProviderID,
		"externalTransactionId", data.ExternalTransactionID,
	)

	now := time.Now().UTC()

	return c.processor.ProcessMessage(
		ctx,
		consumerName,
		envelope.MessageID,
		hash,
		transaction,
		now,
	)
}

// peekMessageID extrai o messageId do envelope para correlação de logs,
// mesmo quando o restante da mensagem é inválido.
func peekMessageID(body string) string {
	var envelope struct {
		MessageID string `json:"messageId"`
	}

	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}

	return envelope.MessageID
}
