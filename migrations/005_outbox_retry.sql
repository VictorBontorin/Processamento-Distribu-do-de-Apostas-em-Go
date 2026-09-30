ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0;

ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

ALTER TABLE outbox_events
    ADD CONSTRAINT outbox_events_attempts_non_negative
    CHECK (attempts >= 0);

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
ON outbox_events (next_attempt_at, occurred_at)
WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_outbox_events_locked
ON outbox_events (locked_until)
WHERE published_at IS NULL;