ALTER TABLE wager_transactions
    ADD COLUMN result_balance BIGINT,
    ADD COLUMN result_currency VARCHAR(3);

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_result_balance_non_negative
        CHECK (
            result_balance IS NULL
            OR result_balance >= 0
        );

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_result_currency_valid
        CHECK (
            result_currency IS NULL
            OR length(result_currency) = 3
        );