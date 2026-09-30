-- Distingue operações internas (OPENING) de externas e impede
-- crédito inicial duplicado por carteira.
ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_origin_check
    CHECK (
        (
            kind = 'OPENING'
            AND provider_id IS NULL
            AND external_transaction_id IS NULL
            AND idempotency_key IS NULL
            AND payload_hash IS NULL
            AND round_id IS NULL
            AND game_id IS NULL
            AND reference_external_id IS NULL
            AND reference_transaction_id IS NULL
        )
        OR
        (
            kind <> 'OPENING'
            AND provider_id IS NOT NULL
            AND external_transaction_id IS NOT NULL
            AND idempotency_key IS NOT NULL
        )
    );

CREATE UNIQUE INDEX IF NOT EXISTS wager_transactions_single_opening_uq
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';
