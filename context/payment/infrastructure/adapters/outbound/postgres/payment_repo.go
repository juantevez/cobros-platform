package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

const (
	pgErrUniqueViolation  = "23505"
	idempotencyConstraint = "payments_tenant_id_idempotency_key_key"
)

type pgPaymentRepository struct {
	pool postgres.Conn
}

func NewPaymentRepository(pool postgres.Conn) *pgPaymentRepository {
	return &pgPaymentRepository{pool: pool}
}

// Cada método corre en la sesión RLS del tenant que corresponde (withTenant):
// el de la entidad al escribir, el del llamador en las búsquedas por ID.

func (r *pgPaymentRepository) Save(ctx context.Context, p *domain.Payment) error {
	return withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return save(ctx, conn, p)
	})
}

func save(ctx context.Context, conn postgres.Conn, p *domain.Payment) error {
	meta, _ := json.Marshal(p.Metadata())

	_, err := conn.Exec(ctx, `
		INSERT INTO payments (
			id, tenant_id, idempotency_key, checkout_id,
			amount, currency, platform_fee, psp_fee,
			payer_name, payer_email, payer_doc_type, payer_doc_num,
			payment_method, psp_name, psp_reference,
			status, failure_reason, metadata,
			authorized_at, captured_at, failed_at,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
			$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23
		)`,
		p.ID().String(), p.TenantID().String(), p.IdempotencyKey(), nullStr(p.CheckoutID()),
		p.Amount().Amount(), p.Amount().Currency(),
		p.PlatformFee().Amount(), p.PSPFee().Amount(),
		nullStr(p.PayerInfo().Name), nullStr(p.PayerInfo().Email),
		nullStr(p.PayerInfo().DocType), nullStr(p.PayerInfo().DocNum),
		p.Method().String(), nullStr(p.PSPName()), nullStr(p.PSPReference()),
		p.Status().String(), nullStr(p.FailureReason()), meta,
		p.AuthorizedAt(), p.CapturedAt(), p.FailedAt(),
		p.CreatedAt(), p.UpdatedAt(),
	)
	if err != nil {
		// Dos requests con la misma clave: el segundo choca con
		// UNIQUE(tenant_id, idempotency_key).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation && pgErr.ConstraintName == idempotencyConstraint {
			return fmt.Errorf("payment repo: save: %w", domain.ErrPaymentAlreadyExists)
		}
		return fmt.Errorf("payment repo: save: %w", err)
	}
	return nil
}

func (r *pgPaymentRepository) Update(ctx context.Context, p *domain.Payment, from domain.PaymentStatus) error {
	return withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return update(ctx, conn, p, from)
	})
}

func update(ctx context.Context, conn postgres.Conn, p *domain.Payment, from domain.PaymentStatus) error {
	// Condicional al estado de partida: el request original y el recuperador
	// pueden resolver el mismo pago en processing; solo uno escribe.
	tag, err := conn.Exec(ctx, `
		UPDATE payments SET
			status=$2, psp_name=$3, psp_reference=$4,
			platform_fee=$5, psp_fee=$6,
			failure_reason=$7,
			authorized_at=$8, captured_at=$9, failed_at=$10,
			updated_at=$11, refund_psp_reference=$13, refunded_at=$14,
			psp_failure_detail=$15
		WHERE id=$1 AND status=$12`,
		p.ID().String(),
		p.Status().String(), nullStr(p.PSPName()), nullStr(p.PSPReference()),
		p.PlatformFee().Amount(), p.PSPFee().Amount(),
		nullStr(p.FailureReason()),
		p.AuthorizedAt(), p.CapturedAt(), p.FailedAt(),
		p.UpdatedAt(),
		from.String(),
		nullStr(p.RefundPSPReference()), p.RefundedAt(),
		nullStr(p.PSPFailureDetail()),
	)
	if err != nil {
		return fmt.Errorf("payment repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("payment repo: update %s from %s: %w", p.ID(), from, domain.ErrPaymentChanged)
	}
	return nil
}

// ClaimProcessing pasa el pago a processing solo si sigue en initiated.
// El UPDATE condicional es atómico: entre ejecuciones concurrentes del mismo
// pago, solo una afecta la fila y llega al PSP.
func (r *pgPaymentRepository) ClaimProcessing(ctx context.Context, p *domain.Payment) (bool, error) {
	var claimed bool
	err := withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		claimed, err = claimProcessing(ctx, conn, p)
		return err
	})
	return claimed, err
}

func claimProcessing(ctx context.Context, conn postgres.Conn, p *domain.Payment) (bool, error) {
	tag, err := conn.Exec(ctx, `
		UPDATE payments SET status=$2, updated_at=$3, platform_fee=$5
		WHERE id=$1 AND status=$4`,
		p.ID().String(), domain.StatusProcessing.String(), p.UpdatedAt(),
		domain.StatusInitiated.String(), p.PlatformFee().Amount(),
	)
	if err != nil {
		return false, fmt.Errorf("payment repo: claim processing: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *pgPaymentRepository) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	var p *domain.Payment
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		p, err = scanPayment(conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String()))
		return err
	})
	return p, err
}

func (r *pgPaymentRepository) FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payment, error) {
	var p *domain.Payment
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		p, err = scanPayment(conn.QueryRow(ctx, baseSelect+" WHERE tenant_id=$1 AND idempotency_key=$2",
			tenantID.String(), key))
		return err
	})
	return p, err
}

// ListStuckProcessing es cross-tenant (lo usa el recuperador del worker):
// corre con la política de operador, de solo lectura.
func (r *pgPaymentRepository) ListStuckProcessing(ctx context.Context, olderThan time.Time, limit int) ([]*domain.Payment, error) {
	var out []*domain.Payment
	err := asPlatformOperator(ctx, r.pool, func(ctx context.Context, conn postgres.Conn) error {
		var err error
		out, err = listStuckProcessing(ctx, conn, olderThan, limit)
		return err
	})
	return out, err
}

func listStuckProcessing(ctx context.Context, conn postgres.Conn, olderThan time.Time, limit int) ([]*domain.Payment, error) {
	rows, err := conn.Query(ctx,
		baseSelect+" WHERE status=$1 AND updated_at < $2 ORDER BY updated_at LIMIT $3",
		domain.StatusProcessing.String(), olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("payment repo: list stuck processing: %w", err)
	}
	defer rows.Close()
	var out []*domain.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const baseSelect = `
	SELECT id, tenant_id, idempotency_key, checkout_id,
	       amount, currency, platform_fee, psp_fee,
	       payer_name, payer_email, payer_doc_type, payer_doc_num,
	       payment_method, psp_name, psp_reference,
	       status, failure_reason, metadata,
	       authorized_at, captured_at, failed_at,
	       created_at, updated_at,
	       refund_psp_reference, refunded_at, psp_failure_detail
	FROM payments`

func scanPayment(row pgx.Row) (*domain.Payment, error) {
	var (
		idStr, tenantIDStr, idempKey, status, method string
		checkoutID, pspName, pspRef, failReason      *string
		payerName, payerEmail, payerDocType, payerDocNum *string
		amount, platformFee, pspFee                  int64
		currency                                     string
		metaJSON                                     []byte
		authorizedAt, capturedAt, failedAt           *time.Time
		createdAt, updatedAt                         time.Time
		refundRef                                    *string
		refundedAt                                   *time.Time
		pspFailureDetail                             *string
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &idempKey, &checkoutID,
		&amount, &currency, &platformFee, &pspFee,
		&payerName, &payerEmail, &payerDocType, &payerDocNum,
		&method, &pspName, &pspRef,
		&status, &failReason, &metaJSON,
		&authorizedAt, &capturedAt, &failedAt,
		&createdAt, &updatedAt,
		&refundRef, &refundedAt, &pspFailureDetail,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPaymentNotFound
		}
		return nil, fmt.Errorf("payment repo: scan: %w", err)
	}

	var metadata map[string]string
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &metadata)
	}

	m, _ := domain.ParsePaymentMethod(method)

	return domain.ReconstitutePayment(
		domain.PaymentID(idStr),
		domain.TenantID(tenantIDStr),
		idempKey,
		deref(checkoutID),
		domain.ReconstituteMoney(amount, currency),
		domain.ReconstituteMoney(platformFee, currency),
		domain.ReconstituteMoney(pspFee, currency),
		domain.PayerInfo{
			Name:    deref(payerName),
			Email:   deref(payerEmail),
			DocType: deref(payerDocType),
			DocNum:  deref(payerDocNum),
		},
		m,
		deref(pspName), deref(pspRef),
		domain.PaymentStatus(status),
		deref(failReason),
		metadata,
		authorizedAt, capturedAt, failedAt,
		createdAt.UTC(), updatedAt.UTC(), deref(refundRef), refundedAt, deref(pspFailureDetail),
	), nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
