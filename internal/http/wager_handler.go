package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"wager/internal/application"
	"wager/internal/auth"
	"wager/internal/domain"
	"wager/internal/infrastructure/postgres"
	"wager/internal/logging"
)

type WagerHandler struct {
	store interface {
		ProcessExternal(
			ctx context.Context,
			tx *domain.WagerTransaction,
			now time.Time,
		) (postgres.ProcessResult, error)
	}
}

func NewWagerHandler(store interface {
	ProcessExternal(
		ctx context.Context,
		tx *domain.WagerTransaction,
		now time.Time,
	) (postgres.ProcessResult, error)
}) *WagerHandler {
	return &WagerHandler{
		store: store,
	}
}

type createWagerRequest struct {
	ExternalTransactionID string       `json:"externalTransactionId"`
	ProviderID            string       `json:"providerId"`
	WalletID              string       `json:"walletId"`
	PlayerID              string       `json:"playerId"`
	RoundID               string       `json:"roundId"`
	GameID                string       `json:"gameId"`
	Kind                  string       `json:"kind"`
	Money                 moneyRequest `json:"money"`
	ReferenceExternalID   string       `json:"referenceExternalTransactionId"`
}

type moneyRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (h *WagerHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	idempotencyKey := strings.TrimSpace(
		r.Header.Get("Idempotency-Key"),
	)

	if idempotencyKey == "" {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"missing Idempotency-Key",
		)
		return
	}

	var req createWagerRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid JSON",
		)
		return
	}

	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSONError(
			w,
			http.StatusUnauthorized,
			"authentication required",
		)
		return
	}

	// O provedor autorizado vem do token; o corpo não pode escolher outro.
	if principal.ProviderID == "" ||
		strings.TrimSpace(req.ProviderID) != principal.ProviderID {
		writeJSONError(
			w,
			http.StatusForbidden,
			"providerId does not match the authenticated provider",
		)
		return
	}

	walletID, err := domain.ParseID(
		strings.TrimSpace(req.WalletID),
	)
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid walletId",
		)
		return
	}

	playerID, err := domain.ParseID(
		strings.TrimSpace(req.PlayerID),
	)
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid playerId",
		)
		return
	}

	money, err := domain.ParseMoney(
		req.Money.Amount,
		req.Money.Currency,
	)
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid money",
		)
		return
	}

	kind := domain.TransactionKind(
		strings.ToUpper(
			strings.TrimSpace(req.Kind),
		),
	)

	tx, err := domain.NewExternalTransaction(
		domain.NewID(),
		strings.TrimSpace(req.ExternalTransactionID),
		strings.TrimSpace(req.ProviderID),
		idempotencyKey,
		"",
		walletID,
		playerID,
		strings.TrimSpace(req.RoundID),
		strings.TrimSpace(req.GameID),
		kind,
		money,
		strings.TrimSpace(req.ReferenceExternalID),
		time.Now().UTC(),
	)
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid transaction",
		)
		return
	}

	payloadHash, err := application.CanonicalTransactionHash(tx)
	if err != nil {
		writeJSONError(
			w,
			http.StatusInternalServerError,
			"could not calculate transaction hash",
		)
		return
	}

	/*
			Recriamos a transação com o hash canônico.

		Os campos de negócio permanecem exatamente os mesmos.
	*/
	tx, err = domain.NewExternalTransaction(
		tx.ID(),
		tx.ExternalTransactionID(),
		tx.ProviderID(),
		tx.IdempotencyKey(),
		payloadHash,
		tx.WalletID(),
		tx.PlayerID(),
		tx.RoundID(),
		tx.GameID(),
		tx.Kind(),
		tx.Money(),
		tx.ReferenceExternalID(),
		tx.CreatedAt(),
	)
	if err != nil {
		writeJSONError(
			w,
			http.StatusBadRequest,
			"invalid transaction",
		)
		return
	}

	ctx := logging.WithAttrs(
		r.Context(),
		"providerId", principal.ProviderID,
		"externalTransactionId", tx.ExternalTransactionID(),
	)

	result, err := h.store.ProcessExternal(
		ctx,
		tx,
		time.Now().UTC(),
	)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrIdempotencyConflict):
			writeJSONErrorCode(
				w,
				http.StatusConflict,
				"IDEMPOTENCY_CONFLICT",
				"idempotency key was reused with different content",
			)

		case errors.Is(err, postgres.ErrExternalTransactionConflict):
			writeJSONErrorCode(
				w,
				http.StatusConflict,
				"EXTERNAL_TRANSACTION_CONFLICT",
				"external transaction already exists with different content",
			)

		case isTransient(err):
			// Nada foi confirmado: repetir com a mesma Idempotency-Key.
			slog.WarnContext(ctx, "transient failure processing transaction", "error", err)
			w.Header().Set("Retry-After", "1")
			writeJSONErrorCode(
				w,
				http.StatusServiceUnavailable,
				"TEMPORARILY_UNAVAILABLE",
				"temporarily unavailable, retry with the same Idempotency-Key",
			)

		default:
			slog.ErrorContext(ctx, "could not process transaction", "error", err)
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"could not process transaction",
			)
		}

		return
	}

	// Contrato de respostas:
	//   201 PROCESSED (novo)         200 PROCESSED (replay)
	//   202 PENDING / PENDING_REFERENCE (aceito, aguardando referência)
	//   422 REJECTED (regra de negócio, com failureCode)
	//   500 FAILED (falha permanente registrada, com failureCode)
	transaction := result.Transaction

	response := map[string]any{
		"transactionId":    transaction.ID(),
		"status":           transaction.State(),
		"idempotentReplay": result.Replay,
	}

	if !result.Pending {
		response["balance"] = result.Balance
	}

	if code := transaction.FailureCode(); code != "" {
		response["failureCode"] = code
	}

	status := http.StatusInternalServerError

	switch transaction.State() {
	case domain.StateProcessed:
		status = http.StatusCreated
		if result.Replay {
			status = http.StatusOK
		}

	case domain.StatePending, domain.StatePendingReference:
		status = http.StatusAccepted

	case domain.StateRejected:
		status = http.StatusUnprocessableEntity
	}

	writeJSON(w, status, response)
}
