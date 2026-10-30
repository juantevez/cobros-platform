package readers

import (
	"context"

	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// withTenant ejecuta fn con app.current_tenant fijado en la sesión: payments,
// journal_entries y postings aíslan por tenant con RLS, y sin la variable un
// rol sin BYPASSRLS no vería ninguna fila (conciliaría contra la nada). Sin
// tenant (runs globales) no fija nada.
func withTenant(ctx context.Context, pool pkgpostgres.Conn, tenantID string, fn func(context.Context, pkgpostgres.Conn) error) error {
	if tenantID != "" {
		ctx = pkgpostgres.WithAppTenant(ctx, "app.current_tenant", tenantID)
	}
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}
