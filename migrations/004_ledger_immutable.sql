CREATE OR REPLACE FUNCTION prevent_ledger_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION
        'ledger_entries is append-only: UPDATE and DELETE are forbidden';
END;
$$;

DROP TRIGGER IF EXISTS ledger_entries_immutable_update_delete
ON ledger_entries;

CREATE TRIGGER ledger_entries_immutable_update_delete
BEFORE UPDATE OR DELETE
ON ledger_entries
FOR EACH ROW
EXECUTE FUNCTION prevent_ledger_mutation();

DROP TRIGGER IF EXISTS ledger_entries_immutable_truncate
ON ledger_entries;

CREATE TRIGGER ledger_entries_immutable_truncate
BEFORE TRUNCATE
ON ledger_entries
FOR EACH STATEMENT
EXECUTE FUNCTION prevent_ledger_mutation();