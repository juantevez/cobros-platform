-- migrations/000047_onboarding_verify_approved_bank_accounts.up.sql
-- Aprobar el KYC verifica la cuenta bancaria de la solicitud: el operador la
-- revisa como parte de la aprobación. Nada la marcaba verified, así que Payout
-- no podía exigirlo. Las solicitudes ya aprobadas se ponen al día.
UPDATE onboarding_bank_accounts oba
SET verified = true
FROM onboarding_applications oa
WHERE oa.id = oba.application_id
  AND oa.status = 'approved'
  AND NOT oba.verified;
