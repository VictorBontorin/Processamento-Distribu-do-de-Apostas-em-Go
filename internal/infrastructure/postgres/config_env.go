package postgres

import (
	"fmt"
	"os"
	"time"
)

// LoadConfig lê a conexão do PostgreSQL do ambiente.
// Variáveis: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME.
func LoadConfig() Config {
	return Config{
		Host:     getenv("DB_HOST", "localhost"),
		Port:     getenv("DB_PORT", "5432"),
		User:     getenv("DB_USER", "wager"),
		Password: getenv("DB_PASSWORD", "wager"),
		Database: getenv("DB_NAME", "wager"),
	}
}

// StoreConfig reúne parâmetros de negócio do store.
type StoreConfig struct {
	// PendingReferenceTTL é quanto tempo uma reversão espera pela
	// referência antes de ser rejeitada. Variável: PENDING_REFERENCE_TTL.
	PendingReferenceTTL time.Duration
}

func LoadStoreConfig() (StoreConfig, error) {
	cfg := StoreConfig{PendingReferenceTTL: 5 * time.Minute}

	if raw := os.Getenv("PENDING_REFERENCE_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl <= 0 {
			return StoreConfig{}, fmt.Errorf(
				"invalid PENDING_REFERENCE_TTL %q",
				raw,
			)
		}

		cfg.PendingReferenceTTL = ttl
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
