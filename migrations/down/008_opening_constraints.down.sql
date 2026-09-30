DROP INDEX IF EXISTS wager_transactions_single_opening_uq;

ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_transactions_origin_check;
