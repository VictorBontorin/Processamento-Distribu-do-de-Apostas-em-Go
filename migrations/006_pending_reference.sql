ALTER TABLE wager_transactions
    ADD COLUMN IF NOT EXISTS reference_attempts INTEGER NOT NULL DEFAULT 0;

ALTER TABLE wager_transactions
    ADD COLUMN IF NOT EXISTS reference_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE wager_transactions
    ADD COLUMN IF NOT EXISTS reference_expires_at TIMESTAMPTZ;

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_reference_attempts_non_negative
    CHECK (reference_attempts >= 0);

CREATE INDEX IF NOT EXISTS idx_wager_transactions_pending_reference
ON wager_transactions (
    reference_next_attempt_at,
    created_at
)
WHERE state = 'PENDING_REFERENCE';