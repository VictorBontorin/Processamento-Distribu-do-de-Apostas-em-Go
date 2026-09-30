DROP INDEX IF EXISTS idx_ledger_entries_wallet_seq;

ALTER TABLE ledger_entries
    DROP COLUMN IF EXISTS seq;
