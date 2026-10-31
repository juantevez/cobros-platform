package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// paymentReader implementa application.PaymentReader consultando directamente
// la tabla `payments` del BC Payment (mismo patrón cross-context que
// reconciliation/readers: mismo esquema PostgreSQL, sin acoplamiento de código).
type paymentReader struct{ pool pkgpostgres.Conn }

func NewPaymentReader(pool pkgpostgres.Conn) application.PaymentReader {
	return &paymentReader{pool: pool}
}

// FindPayment retorna el PaymentRef del pago disputado. Corre en la sesión
// RLS del contexto: abrir una disputa es del operador de plataforma
// (WithPlatformOperator), que lee pagos de cualquier tenant con la política
// de operador de payments. Sin esa marca solo ve los del tenant del llamador.
func (r *paymentReader) FindPayment(ctx context.Context, paymentID string) (domain.PaymentRef, error) {
	var ref domain.PaymentRef
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT id, tenant_id::text, status, amount, currency
			FROM payments
			WHERE id = $1`, paymentID).
			Scan(&ref.ID, &ref.TenantID, &ref.Status, &ref.Amount, &ref.Currency)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentRef{}, fmt.Errorf("%w: %q", domain.ErrPaymentNotFound, paymentID)
	}
	if err != nil {
		return domain.PaymentRef{}, fmt.Errorf("dispute payment reader: find: %w", err)
	}
	return ref, nil
}
