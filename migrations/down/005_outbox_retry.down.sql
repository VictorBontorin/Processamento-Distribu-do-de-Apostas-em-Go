DROP INDEX IF EXISTS idx_outbox_events_locked;
DROP INDEX IF EXISTS idx_outbox_events_pending;

ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_attempts_non_negative;

ALTER TABLE outbox_events
    DROP COLUMN IF EXISTS locked_until;

ALTER TABLE outbox_events
    DROP COLUMN IF EXISTS next_attempt_at;

ALTER TABLE outbox_events
    DROP COLUMN IF EXISTS attempts;