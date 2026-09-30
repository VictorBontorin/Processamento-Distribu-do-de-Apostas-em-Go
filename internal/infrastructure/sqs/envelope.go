package sqs

import (
	"encoding/json"
	"fmt"
	"time"
)

type MessageEnvelope struct {
	MessageID  string          `json:"messageId"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

type MoneyMessage struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type WagerTransactionMessage struct {
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	IdempotencyKey                 string       `json:"idempotencyKey"`
	PlayerID                       string       `json:"playerId"`
	WalletID                       string       `json:"walletId"`
	RoundID                        string       `json:"roundId"`
	GameID                         string       `json:"gameId"`
	Kind                           string       `json:"kind"`
	Money                          MoneyMessage `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId"`
}

func DecodeEnvelope(body string) (MessageEnvelope, error) {
	var envelope MessageEnvelope

	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return MessageEnvelope{}, fmt.Errorf(
			"decode message envelope: %w",
			err,
		)
	}

	if envelope.MessageID == "" {
		return MessageEnvelope{}, fmt.Errorf(
			"messageId is required",
		)
	}

	if envelope.Type == "" {
		return MessageEnvelope{}, fmt.Errorf(
			"message type is required",
		)
	}

	if envelope.OccurredAt.IsZero() {
		return MessageEnvelope{}, fmt.Errorf(
			"occurredAt is required",
		)
	}

	if len(envelope.Data) == 0 {
		return MessageEnvelope{}, fmt.Errorf(
			"message data is required",
		)
	}

	return envelope, nil
}

func DecodeWagerTransaction(
	data json.RawMessage,
) (WagerTransactionMessage, error) {
	var message WagerTransactionMessage

	if err := json.Unmarshal(data, &message); err != nil {
		return WagerTransactionMessage{}, fmt.Errorf(
			"decode wager transaction: %w",
			err,
		)
	}

	if message.ProviderID == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"providerId is required",
		)
	}

	if message.ExternalTransactionID == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"externalTransactionId is required",
		)
	}

	if message.IdempotencyKey == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"idempotencyKey is required",
		)
	}

	if message.PlayerID == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"playerId is required",
		)
	}

	if message.WalletID == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"walletId is required",
		)
	}

	if message.Kind == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"kind is required",
		)
	}

	if message.Money.Amount == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"money.amount is required",
		)
	}

	if message.Money.Currency == "" {
		return WagerTransactionMessage{}, fmt.Errorf(
			"money.currency is required",
		)
	}

	return message, nil
}
