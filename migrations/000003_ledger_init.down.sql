-- migrations/000003_ledger_init.down.sql
DROP TRIGGER IF EXISTS trg_check_entry_balance ON postings;
DROP FUNCTION IF EXISTS check_entry_balance();
DROP TABLE IF EXISTS postings;
DROP TABLE IF EXISTS account_balances;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS ledger_accounts;
