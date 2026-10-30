package postgres

import (
	"context"

	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsTenantKey es la variable de sesión de las políticas RLS por tenant.
const rlsTenantKey = "app.current_tenant"

// withTenant ejecuta fn con app.current_tenant fijado en la sesión (RLS).
// Fuera de una transacción, RunWithAppTenant abre una efímera para el SET
// LOCAL. Sin tenant (runs globales) no fija nada: solo ve filas quien llegue
// con app.platform_operator.
func withTenant(ctx context.Context, pool pkgpostgres.Conn, tenantID string, fn func(context.Context, pkgpostgres.Conn) error) error {
	if tenantID != "" {
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	}
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}

// callerTenant es el tenant autenticado del contexto ("" si no hay).
func callerTenant(ctx context.Context) string {
	tid, _ := pkgpostgres.TenantIDFromContext(ctx)
	return tid
}
