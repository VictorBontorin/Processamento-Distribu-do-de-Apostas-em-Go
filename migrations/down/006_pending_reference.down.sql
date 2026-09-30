DROP INDEX IF EXISTS idx_wager_transactions_pending_reference;

ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_transactions_reference_attempts_non_negative;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS reference_expires_at;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS reference_next_attempt_at;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS reference_attempts;