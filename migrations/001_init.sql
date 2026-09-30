CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL,
    currency VARCHAR(3) NOT NULL,
    balance BIGINT NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT wallets_balance_non_negative CHECK (balance >= 0),
    CONSTRAINT wallets_version_positive CHECK (version >= 1),
    CONSTRAINT wallets_player_currency_unique UNIQUE (player_id, currency)
);

CREATE TABLE wager_transactions (
    id UUID PRIMARY KEY,
    external_transaction_id VARCHAR(255),
    provider_id VARCHAR(255),
    idempotency_key VARCHAR(255),
    payload_hash VARCHAR(255),
    wallet_id UUID NOT NULL,
    player_id UUID NOT NULL,
    round_id VARCHAR(255),
    game_id VARCHAR(255),
    kind VARCHAR(20) NOT NULL,
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    reference_external_id VARCHAR(255),
    reference_transaction_id UUID,
    state VARCHAR(30) NOT NULL,
    failure_code VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT wager_transactions_wallet_fk
        FOREIGN KEY (wallet_id) REFERENCES wallets(id),

    CONSTRAINT wager_transactions_reference_fk
        FOREIGN KEY (reference_transaction_id)
        REFERENCES wager_transactions(id),

    CONSTRAINT wager_transactions_external_unique
        UNIQUE (provider_id, external_transaction_id),

    CONSTRAINT wager_transactions_idempotency_unique
        UNIQUE (provider_id, idempotency_key)
);

CREATE TABLE ledger_entries (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL,
    transaction_id UUID NOT NULL,
    direction VARCHAR(10) NOT NULL,
    amount BIGINT NOT NULL,
    balance_before BIGINT NOT NULL,
    balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT ledger_wallet_fk
        FOREIGN KEY (wallet_id) REFERENCES wallets(id),

    CONSTRAINT ledger_transaction_fk
        FOREIGN KEY (transaction_id) REFERENCES wager_transactions(id),

    CONSTRAINT ledger_transaction_unique
        UNIQUE (wallet_id, transaction_id),

    CONSTRAINT ledger_amount_positive
        CHECK (amount > 0),

    CONSTRAINT ledger_balance_before_non_negative
        CHECK (balance_before >= 0),

    CONSTRAINT ledger_balance_after_non_negative
        CHECK (balance_after >= 0)
);

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,
    event_type VARCHAR(255) NOT NULL,
    aggregate_id UUID NOT NULL,
    correlation_id UUID,
    causation_id UUID,
    occurred_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL,
    payload JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_wager_transactions_reference
    ON wager_transactions(provider_id, reference_external_id);

CREATE INDEX idx_wager_transactions_wallet
    ON wager_transactions(wallet_id);

CREATE INDEX idx_ledger_wallet
    ON ledger_entries(wallet_id);

CREATE INDEX idx_outbox_unpublished
    ON outbox_events(created_at)
    WHERE published_at IS NULL;