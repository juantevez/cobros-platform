-- migrations/000047_onboarding_verify_approved_bank_accounts.down.sql
-- No se revierte: no hay forma de distinguir las cuentas que se marcaron acá
-- de las que se verificaron al aprobar después de esta migración.
SELECT 1;
