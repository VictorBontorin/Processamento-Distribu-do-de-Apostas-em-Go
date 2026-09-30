-- Cada transação original aceita no máximo UMA reversão processada
-- (REFUND ou ROLLBACK). Barreira final no banco, independente de locks
-- da aplicação: impede devolver o mesmo débito duas vezes.
CREATE UNIQUE INDEX IF NOT EXISTS wager_transactions_single_reversal_uq
    ON wager_transactions (reference_transaction_id)
    WHERE reference_transaction_id IS NOT NULL
      AND state = 'PROCESSED'
      AND kind IN ('REFUND', 'ROLLBACK');
