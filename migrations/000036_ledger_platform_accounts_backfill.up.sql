-- migrations/000036_ledger_platform_accounts_backfill.up.sql
-- Completa las cuentas in_transit y platform_fees de los comercios ya aprobados.
--
-- El consumer de Onboarding creaba 5 de las 7 cuentas: sin in_transit ningún
-- pago capturado llegaba al Ledger, y sin platform_fees tampoco las comisiones
-- ni las disputas perdidas. Desde ahora Onboarding crea las 7; esto cubre a los
-- tenants anteriores, una cuenta por cada (tenant, moneda) con merchant_balance.
--
-- No se emite ledger.account.created.v1 para estas cuentas (no pasan por el
-- outbox), así que Audit no las registra.

WITH missing AS (
    SELECT gen_random_uuid() AS id, mb.tenant_id, t.type, mb.currency, t.description
    FROM ledger_accounts mb
    CROSS JOIN (VALUES
        ('in_transit',    'Fondos cobrados que el PSP aún no liquidó'),
        ('platform_fees', 'Comisiones de la plataforma')
    ) AS t (type, description)
    WHERE mb.type = 'merchant_balance'
      AND NOT EXISTS (
          SELECT 1 FROM ledger_accounts la
          WHERE la.tenant_id = mb.tenant_id
            AND la.type      = t.type
            AND la.currency  = mb.currency
      )
), inserted AS (
    INSERT INTO ledger_accounts (id, tenant_id, type, currency, description, created_at)
    SELECT id, tenant_id, type, currency, description, now()
    FROM missing
    RETURNING id
)
INSERT INTO account_balances (account_id, balance, updated_at)
SELECT id, 0, now() FROM inserted;
