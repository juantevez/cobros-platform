-- migrations/000036_ledger_platform_accounts_backfill.down.sql
-- Sin reversa: las cuentas completadas no se distinguen de las que crea
-- Onboarding, y borrarlas dejaría a esos tenants sin poder registrar pagos.
SELECT 1;
