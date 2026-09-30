package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"wager/internal/domain"
)

// CanonicalTransactionHash calcula o hash de idempotência (SHA-256 em hex)
// de um JSON canônico com chaves em ordem alfabética (json.Marshal ordena
// as chaves de map[string]string).
//
// Campos: providerId, externalTransactionId, playerId, walletId, roundId,
// gameId, kind, amount, currency, referenceExternalTransactionId.
// Excluídos: Idempotency-Key, messageId e metadados de transporte.
// Normalização: kind em maiúsculas e sem espaços; valores monetários já
// chegam em escala fixa de duas casas (entradas equivalentes são rejeitadas,
// não normalizadas). O mesmo cálculo vale para HTTP e SQS.
func CanonicalTransactionHash(tx *domain.WagerTransaction) (string, error) {
	payload := map[string]string{
		"providerId":                     tx.ProviderID(),
		"externalTransactionId":          tx.ExternalTransactionID(),
		"playerId":                       tx.PlayerID().String(),
		"walletId":                       tx.WalletID().String(),
		"roundId":                        tx.RoundID(),
		"gameId":                         tx.GameID(),
		"kind":                           string(tx.Kind()),
		"amount":                         tx.Money().Amount(),
		"currency":                       string(tx.Money().Currency()),
		"referenceExternalTransactionId": tx.ReferenceExternalID(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal canonical transaction: %w", err)
	}

	hash := sha256.Sum256(data)

	return hex.EncodeToString(hash[:]), nil
}

// WithPayloadHash devolve a mesma operação externa carregando o hash
// canônico. Usado por HTTP e SQS para garantir hashes equivalentes.
func WithPayloadHash(
	tx *domain.WagerTransaction,
) (*domain.WagerTransaction, error) {
	hash, err := CanonicalTransactionHash(tx)
	if err != nil {
		return nil, err
	}

	return domain.NewExternalTransaction(
		tx.ID(),
		tx.ExternalTransactionID(),
		tx.ProviderID(),
		tx.IdempotencyKey(),
		hash,
		tx.WalletID(),
		tx.PlayerID(),
		tx.RoundID(),
		tx.GameID(),
		tx.Kind(),
		tx.Money(),
		tx.ReferenceExternalID(),
		tx.CreatedAt(),
	)
}
