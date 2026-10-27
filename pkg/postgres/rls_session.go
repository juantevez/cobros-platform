package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ── Sesión con variable de aplicación para RLS ────────────────────────────────
//
// Las políticas de RLS de las tablas multi-tenant dependen de la variable
// de sesión 'app.current_tenant'. TxManager.RunInTx ya la fija con
// set_config(..., true) dentro de transacciones, pero las consultas que se
// ejecutan DIRECTAMENTE contra el pool (lecturas simples fuera de tx) nunca
// la establecen: el policy siempre evalúa NULL y, si el rol de conexión no es
// superuser/BYPASSRLS, filtraría TODAS las filas.
//
// WithAppTenant envuelve un contexto con la variable deseada y los repos
// deben ejecutar sus queries a través de RunWithAppTenant para que la
// variable quede fijada (SET LOCAL dentro de una tx efímera) ANTES de la
// consulta. Así el aislamiento deja de depender exclusivamente del WHERE
// tenant_id=$1 escrito a mano en cada query.

type appVarsKey struct{}

// WithAppTenant inyecta variables de aplicación (p. ej. "app.current_tenant")
// que RunWithAppTenant fijará en la sesión antes de ejecutar las queries del
// fn. Es un no-op observable si el contexto ya contiene valores para alguna
// de las claves: los valores externos tienen prioridad.
func WithAppTenant(ctx context.Context, key, value string) context.Context {
	vars, _ := ctx.Value(appVarsKey{}).(map[string]string)
	if _, ok := vars[key]; ok {
		return ctx // no pisar un valor ya provisto por un nivel superior
	}
	next := make(map[string]string, len(vars)+1)
	for k, v := range vars {
		next[k] = v
	}
	next[key] = value
	return context.WithValue(ctx, appVarsKey{}, next)
}

// PlatformOperatorKey es la variable de sesión que habilita las políticas RLS
// de operador de plataforma (lectura/escritura cross-tenant, p. ej. revisión
// de KYC). Solo debe fijarla código de servidor detrás de un guard de rol.
const PlatformOperatorKey = "app.platform_operator"

// WithPlatformOperator marca el contexto como acción de operador de
// plataforma: RunInTx y RunWithAppTenant fijan app.platform_operator='on'.
func WithPlatformOperator(ctx context.Context) context.Context {
	return WithAppTenant(ctx, PlatformOperatorKey, "on")
}

func appVarsFromContext(ctx context.Context) map[string]string {
	vars, _ := ctx.Value(appVarsKey{}).(map[string]string)
	return vars
}

// RunWithAppTenant ejecuta fn sobre conn garantizando que las variables de
// aplicación presentes en el contexto (WithAppTenant) estén fijadas en la
// sesión antes de cualquier query de fn.
//
//   - Si ya hay una transacción activa en el contexto (RunInTx), asume que el
//     TxManager fijó las variables y delega directamente en fn.
//   - Caso contrario abre una tx READ COMMITTED efímera, aplica
//     set_config(..., is_local=true) por cada variable, ejecuta fn con esa tx
//     (los repos la descubren vía ConnFromContext) y hace commit/rollback.
//
// Nota: si fn realiza un commit interno, el scope LOCAL de set_config puede
// terminar; por eso los casos de uso que escriben bajo RLS deben seguir
// usando RunInTx. RunWithAppTenant está pensado para reads y writes de una
// sola sentencia.
func RunWithAppTenant(ctx context.Context, pool Conn, fn func(context.Context) error) error {
	vars := appVarsFromContext(ctx)

	// Transacción activa: el TxManager ya configuró app.current_tenant.
	if _, ok := TxFromContext(ctx); ok || len(vars) == 0 {
		return fn(ctx)
	}

	txxer, ok := pool.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	})
	if !ok {
		// El pool no soporta transacciones (mock mínimo): ejecutar sin setup.
		return fn(ctx)
	}

	tx, err := txxer.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("rls session: begin: %w", err)
	}
	for key, value := range vars {
		if _, err := tx.Exec(ctx, `SELECT set_config($1, $2, true)`, key, value); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("rls session: set %s: %w", key, err)
		}
	}
	if err := fn(WithTx(ctx, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rls session: commit: %w", err)
	}
	return nil
}
