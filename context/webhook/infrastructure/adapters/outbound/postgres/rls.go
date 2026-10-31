package postgres

import (
	"context"

	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsTenantKey es la variable de sesión de las políticas RLS por tenant.
const rlsTenantKey = "app.current_tenant"

// withTenant ejecuta fn con app.current_tenant fijado en la sesión:
// webhook_endpoints, webhook_deliveries y webhook_delivery_attempts aíslan por
// tenant con RLS, y sin la variable un rol sin BYPASSRLS no ve ni escribe
// filas. Fuera de una transacción, RunWithAppTenant abre una efímera para el
// SET LOCAL; dentro, la fija el TxManager con el tenant del contexto.
func withTenant(ctx context.Context, pool pkgpostgres.Conn, tenantID string, fn func(context.Context, pkgpostgres.Conn) error) error {
	if tenantID != "" {
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	}
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}

// asPlatformOperator ejecuta fn con la política de operador (lectura
// cross-tenant): solo para el listado del RetryPoller.
func asPlatformOperator(ctx context.Context, pool pkgpostgres.Conn, fn func(context.Context, pkgpostgres.Conn) error) error {
	ctx = pkgpostgres.WithPlatformOperator(ctx)
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}

// callerTenant es el tenant del contexto ("" si no hay): lo usan las búsquedas
// por ID, que no reciben el tenant.
func callerTenant(ctx context.Context) string {
	tid, _ := pkgpostgres.TenantIDFromContext(ctx)
	return tid
}
