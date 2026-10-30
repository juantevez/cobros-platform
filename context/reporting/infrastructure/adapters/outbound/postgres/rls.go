package postgres

import (
	"context"

	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsTenantKey es la variable de sesión de las políticas RLS por tenant.
const rlsTenantKey = "app.current_tenant"

// withTenant ejecuta fn con app.current_tenant fijado en la sesión: las
// tablas del read-model y ledger_accounts aíslan por tenant con RLS, y sin la
// variable un rol sin BYPASSRLS no puede insertar ni ver filas. Fuera de una
// transacción, RunWithAppTenant abre una efímera para el SET LOCAL.
func withTenant(ctx context.Context, pool pkgpostgres.Conn, tenantID string, fn func(context.Context, pkgpostgres.Conn) error) error {
	if tenantID != "" {
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	}
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}
