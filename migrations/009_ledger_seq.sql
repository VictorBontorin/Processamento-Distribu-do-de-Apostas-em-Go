-- Sequência por inserção: sob o lock da carteira, a ordem de seq coincide
-- com a ordem de commit, o que dá paginação estável por cursor.
ALTER TABLE ledger_entries
    ADD COLUMN IF NOT EXISTS seq BIGSERIAL;

CREATE INDEX IF NOT EXISTS idx_ledger_entries_wallet_seq
    ON ledger_entries (wallet_id, seq);
