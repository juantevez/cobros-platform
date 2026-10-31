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

// FindPayment retorna el PaymentRef del pago disputado. El tenant_id se lee
// desde el contexto (RLS): si el pago pertenece a otro tenant, la consulta no
// lo devuelve y tratamos el caso como "no encontrado".
func (r *paymentReader) FindPayment(ctx context.Context, paymentID string) (domain.PaymentRef, error) {
	var ref domain.PaymentRef
	err := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id::text, status, amount, currency
		FROM payments
		WHERE id = $1`, paymentID).
		Scan(&ref.ID, &ref.TenantID, &ref.Status, &ref.Amount, &ref.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentRef{}, fmt.Errorf("%w: %q", domain.ErrPaymentNotFound, paymentID)
	}
	if err != nil {
		return domain.PaymentRef{}, fmt.Errorf("dispute payment reader: find: %w", err)
	}
	return ref, nil
}
