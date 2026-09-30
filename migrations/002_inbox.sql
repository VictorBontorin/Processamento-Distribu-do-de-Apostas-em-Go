CREATE TABLE inbox_messages (
    consumer_name VARCHAR(255) NOT NULL,
    message_id VARCHAR(255) NOT NULL,
    message_hash VARCHAR(64) NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,

    CONSTRAINT inbox_messages_pk
        PRIMARY KEY (consumer_name, message_id)
);

CREATE INDEX idx_inbox_messages_pending
    ON inbox_messages(consumer_name, received_at)
    WHERE completed_at IS NULL;