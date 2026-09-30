package postgres

import (
	"time"

	"wager/internal/domain"
)

// Versão do esquema dos eventos de integração publicados.
const eventSchemaVersion = 1

func walletBalanceChangedEvent(
	wallet *domain.Wallet,
	entry domain.LedgerEntry,
	transactionID domain.ID,
	now time.Time,
) OutboxEvent {
	return OutboxEvent{
		ID:          domain.NewID(),
		EventType:   "WalletBalanceChanged",
		AggregateID: wallet.ID(),
		CausationID: transactionID,
		OccurredAt:  now,
		Version:     eventSchemaVersion,
		Payload: map[string]any{
			"walletId":      wallet.ID(),
			"transactionId": transactionID,
			"direction":     entry.Direction(),
			"money":         entry.Amount(),
			"balanceBefore": entry.BalanceBefore(),
			"balanceAfter":  entry.BalanceAfter(),
			"walletVersion": wallet.Version(),
		},
	}
}

func transactionProcessedEvent(
	tx *domain.WagerTransaction,
	balance domain.Money,
	now time.Time,
) OutboxEvent {
	return OutboxEvent{
		ID:          domain.NewID(),
		EventType:   "WagerTransactionProcessed",
		AggregateID: tx.ID(),
		CausationID: tx.ID(),
		OccurredAt:  now,
		Version:     eventSchemaVersion,
		Payload: map[string]any{
			"transactionId":         tx.ID(),
			"walletId":              tx.WalletID(),
			"playerId":              tx.PlayerID(),
			"providerId":            tx.ProviderID(),
			"externalTransactionId": tx.ExternalTransactionID(),
			"kind":                  tx.Kind(),
			"state":                 tx.State(),
			"money":                 tx.Money(),
			"balance":               balance,
		},
	}
}

func transactionRejectedEvent(
	tx *domain.WagerTransaction,
	now time.Time,
) OutboxEvent {
	return OutboxEvent{
		ID:          domain.NewID(),
		EventType:   "WagerTransactionRejected",
		AggregateID: tx.ID(),
		CausationID: tx.ID(),
		OccurredAt:  now,
		Version:     eventSchemaVersion,
		Payload: map[string]any{
			"transactionId":         tx.ID(),
			"walletId":              tx.WalletID(),
			"playerId":              tx.PlayerID(),
			"providerId":            tx.ProviderID(),
			"externalTransactionId": tx.ExternalTransactionID(),
			"kind":                  tx.Kind(),
			"state":                 tx.State(),
			"failureCode":           tx.FailureCode(),
		},
	}
}
