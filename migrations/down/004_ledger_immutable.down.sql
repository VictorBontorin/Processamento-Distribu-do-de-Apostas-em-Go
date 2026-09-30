DROP TRIGGER IF EXISTS ledger_entries_immutable_truncate
ON ledger_entries;

DROP TRIGGER IF EXISTS ledger_entries_immutable_update_delete
ON ledger_entries;

DROP FUNCTION IF EXISTS prevent_ledger_mutation();