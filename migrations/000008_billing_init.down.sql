-- migrations/000008_billing_init.down.sql
DROP TABLE IF EXISTS billing_tenant_plans;
DROP TABLE IF EXISTS billing_plan_method_rates;
DROP TABLE IF EXISTS billing_plans;
