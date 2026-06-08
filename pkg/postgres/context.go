package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// contextKey es un tipo privado para las claves del contexto.
// Evita colisiones con otras librerías que usen string como clave.
type contextKey string

const (
	ctxKeyTenantID    contextKey = "pg:tenantID"
	ctxKeyActor       contextKey = "pg:actor"
	ctxKeyCorrelation contextKey = "pg:correlationID"
	ctxKeyTx          contextKey = "pg:tx"
)

// ── Tenant ──────────────────────────────────────────────────────────────────

// WithTenantID inyecta el ID del tenant activo en el contexto.
// El TxManager lo extrae automáticamente para configurar el RLS.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, tenantID)
}

// TenantIDFromContext retorna el tenantID del contexto y si estaba presente.
func TenantIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKeyTenantID).(string)
	return id, ok && id != ""
}

// ── Actor ────────────────────────────────────────────────────────────────────

// WithActor inyecta el actor (userID o "system") en el contexto.
// Se usa en auditoría para saber quién ejecutó la operación.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, ctxKeyActor, actor)
}

// ActorFromContext retorna el actor del contexto. Retorna "system" como fallback.
func ActorFromContext(ctx context.Context) string {
	if a, ok := ctx.Value(ctxKeyActor).(string); ok && a != "" {
		return a
	}
	return "system"
}

// ── Correlation ID ───────────────────────────────────────────────────────────

// WithCorrelationID inyecta el correlation ID en el contexto.
// Debe asignarse en el middleware HTTP al inicio de cada request.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyCorrelation, id)
}

// CorrelationIDFromContext retorna el correlation ID del contexto.
func CorrelationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyCorrelation).(string); ok {
		return id
	}
	return ""
}

// ── Transacción activa ───────────────────────────────────────────────────────

// WithTx inyecta una transacción activa en el contexto.
// Solo debe usarlo el TxManager; los repositorios usan ConnFromContext.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, ctxKeyTx, tx)
}

// TxFromContext extrae la transacción del contexto si existe.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(ctxKeyTx).(pgx.Tx)
	return tx, ok
}
