package http

import (
	"errors"
	"net/http"
	"strconv"

	"wager/internal/auth"
	"wager/internal/domain"
	"wager/internal/infrastructure/postgres"
)

type ReadHandler struct {
	store *postgres.ReadStore
}

func NewReadHandler(store *postgres.ReadStore) *ReadHandler {
	return &ReadHandler{store: store}
}

const (
	defaultLedgerLimit = 50
	maxLedgerLimit     = 200
)

// GET /wallets/{walletId}/ledger?cursor=...&limit=50  (papel internal)
func (h *ReadHandler) GetLedger(w http.ResponseWriter, r *http.Request) {
	walletID, err := domain.ParseID(r.PathValue("walletId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid walletId")
		return
	}

	limit := defaultLedgerLimit

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLedgerLimit {
			writeJSONError(
				w,
				http.StatusBadRequest,
				"limit must be between 1 and 200",
			)
			return
		}
	}

	page, err := h.store.GetLedger(
		r.Context(),
		walletID,
		r.URL.Query().Get("cursor"),
		limit,
	)
	if err != nil {
		h.writeReadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, page)
}

// POST /wallets/{walletId}/reconciliation  (papel internal)
func (h *ReadHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	walletID, err := domain.ParseID(r.PathValue("walletId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid walletId")
		return
	}

	result, err := h.store.Reconcile(r.Context(), walletID)
	if err != nil {
		h.writeReadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GET /wagering/transactions/{transactionId}  (provider: só as próprias)
func (h *ReadHandler) GetTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(r.PathValue("transactionId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid transactionId")
		return
	}

	view, err := h.store.GetTransaction(r.Context(), id)
	if err != nil {
		h.writeReadError(w, err)
		return
	}

	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Provedores só enxergam as próprias transações; de outro provedor
	// a resposta é 404, para não revelar a existência.
	if !principal.HasRole(auth.RoleInternal) &&
		view.ProviderID != principal.ProviderID {
		writeJSONError(w, http.StatusNotFound, "transaction not found")
		return
	}

	writeJSON(w, http.StatusOK, view)
}

// GET /providers/{providerId}/wagering/transactions/{externalTransactionId}
func (h *ReadHandler) GetProviderTransaction(
	w http.ResponseWriter,
	r *http.Request,
) {
	providerID := r.PathValue("providerId")

	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if !principal.HasRole(auth.RoleInternal) &&
		principal.ProviderID != providerID {
		writeJSONError(
			w,
			http.StatusForbidden,
			"providerId does not match the authenticated provider",
		)
		return
	}

	view, err := h.store.GetProviderTransaction(
		r.Context(),
		providerID,
		r.PathValue("externalTransactionId"),
	)
	if err != nil {
		h.writeReadError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, view)
}

func (h *ReadHandler) writeReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not found")
	case errors.Is(err, postgres.ErrInvalidCursor):
		writeJSONError(w, http.StatusBadRequest, "invalid cursor")
	case isTransient(err):
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusServiceUnavailable, "temporarily unavailable")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}
