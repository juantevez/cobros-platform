-- migrations/000028_notification_kyc_template_keys.up.sql
-- Los templates de KYC pasan a indexarse por el event type canónico
-- (onboarding.application.approved/rejected), igual que el resto: con las
-- claves viejas (kyc.approved/kyc.rejected) el evento normalizado no
-- encontraba template y el email se descartaba. Se migran las preferencias
-- guardadas bajo las claves viejas; si hay filas bajo ambas claves para el
-- mismo tenant, se conserva la más reciente.

-- 1. Fila bajo la clave nueva más vieja que la de la clave vieja: se descarta.
DELETE FROM notification_preferences n
USING notification_preferences o,
      (VALUES ('kyc.approved', 'onboarding.application.approved'),
              ('kyc.rejected', 'onboarding.application.rejected')) AS m(old_key, new_key)
WHERE o.event_type = m.old_key AND n.event_type = m.new_key
  AND n.tenant_id = o.tenant_id AND n.channel = o.channel
  AND n.updated_at < o.updated_at;

-- 2. Claves viejas sin fila bajo la clave nueva: se renombran.
UPDATE notification_preferences o
SET event_type = m.new_key
FROM (VALUES ('kyc.approved', 'onboarding.application.approved'),
             ('kyc.rejected', 'onboarding.application.rejected')) AS m(old_key, new_key)
WHERE o.event_type = m.old_key
  AND NOT EXISTS (
      SELECT 1 FROM notification_preferences n
      WHERE n.tenant_id = o.tenant_id AND n.channel = o.channel AND n.event_type = m.new_key);

-- 3. Lo que quede bajo claves viejas tiene una fila nueva más reciente.
DELETE FROM notification_preferences WHERE event_type IN ('kyc.approved', 'kyc.rejected');
