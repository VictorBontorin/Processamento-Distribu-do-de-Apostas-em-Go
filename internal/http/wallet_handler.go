package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"wager/internal/domain"
	"wager/internal/infrastructure/postgres"
)

// WalletOpener abre uma carteira (e o OPENING, quando há saldo inicial).
type WalletOpener interface {
	OpenWallet(
		ctx context.Context,
		playerID domain.ID,
		initial domain.Money,
		now time.Time,
	) (*domain.Wallet, error)
}

type WalletHandler struct {
	wallets interface {
		GetByID(ctx context.Context, id domain.ID) (*domain.Wallet, error)
		Save(ctx context.Context, wallet *domain.Wallet) error
	}
	opener WalletOpener
}

func NewWalletHandler(
	wallets interface {
		GetByID(ctx context.Context, id domain.ID) (*domain.Wallet, error)
		Save(ctx context.Context, wallet *domain.Wallet) error
	},
	opener WalletOpener,
) *WalletHandler {
	return &WalletHandler{wallets: wallets, opener: opener}
}

type createWalletRequest struct {
	PlayerID       string       `json:"playerId"`
	InitialBalance moneyRequest `json:"initialBalance"`
}

func (h *WalletHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createWalletRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	playerID, err := domain.ParseID(strings.TrimSpace(req.PlayerID))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid playerId")
		return
	}

	initial, err := domain.ParseMoney(
		req.InitialBalance.Amount,
		req.InitialBalance.Currency,
	)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid initialBalance")
		return
	}

	wallet, err := h.opener.OpenWallet(
		r.Context(),
		playerID,
		initial,
		time.Now().UTC(),
	)
	if err != nil {
		if errors.Is(err, postgres.ErrWalletAlreadyExists) {
			writeJSONError(
				w,
				http.StatusConflict,
				"wallet already exists for player and currency",
			)
			return
		}

		writeJSONError(w, http.StatusInternalServerError, "could not create wallet")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":       wallet.ID(),
		"playerId": wallet.PlayerID(),
		"balance":  wallet.Balance(),
		"version":  wallet.Version(),
	})
}

func (h *WalletHandler) Get(w http.ResponseWriter, r *http.Request) {
	walletID, err := domain.ParseID(r.PathValue("walletId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid walletId")
		return
	}

	wallet, err := h.wallets.GetByID(r.Context(), walletID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "wallet not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")

	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":        wallet.ID(),
		"playerId":  wallet.PlayerID(),
		"balance":   wallet.Balance(),
		"version":   wallet.Version(),
		"createdAt": wallet.CreatedAt(),
		"updatedAt": wallet.UpdatedAt(),
	})
}
