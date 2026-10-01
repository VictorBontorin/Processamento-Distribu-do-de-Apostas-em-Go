//go:build integration

// Exige PostgreSQL com as migrations aplicadas (docker compose up -d postgres migrate).
// Executar com: go test -tags=integration ./internal/infrastructure/postgres/

package postgres

import (
	"context"
	"testing"
	"time"

	"wager/internal/domain"
)

func TestWalletRepository_SaveAndGetByID(t *testing.T) {
	ctx := context.Background()

	cfg := LoadConfig() // DB_* do ambiente; padrão: localhost/wager

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	repo := NewWalletRepository(pool)

	wallet, err := domain.NewWallet(
		domain.NewID(),
		domain.NewID(),
		mustMoney(t, "1000.00", "BRL"),
		time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	if err := repo.Save(ctx, wallet); err != nil {
		t.Fatalf("save wallet: %v", err)
	}

	got, err := repo.GetByID(ctx, wallet.ID())
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}

	if got.ID() != wallet.ID() {
		t.Errorf("id: got %s, want %s", got.ID(), wallet.ID())
	}

	if got.PlayerID() != wallet.PlayerID() {
		t.Errorf("player id: got %s, want %s", got.PlayerID(), wallet.PlayerID())
	}

	if got.Currency() != wallet.Currency() {
		t.Errorf("currency: got %s, want %s", got.Currency(), wallet.Currency())
	}

	if got.Balance().Amount() != "1000.00" {
		t.Errorf("balance: got %s, want 1000.00", got.Balance())
	}

	if got.Version() != 1 {
		t.Errorf("version: got %d, want 1", got.Version())
	}
}

func mustMoney(t *testing.T, amount string, currency string) domain.Money {
	t.Helper()

	money, err := domain.ParseMoney(amount, currency)
	if err != nil {
		t.Fatalf("parse money: %v", err)
	}

	return money
}
