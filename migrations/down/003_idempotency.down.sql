ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_transactions_result_currency_valid;

ALTER TABLE wager_transactions
    DROP CONSTRAINT IF EXISTS wager_transactions_result_balance_non_negative;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS result_currency;

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS result_balance;