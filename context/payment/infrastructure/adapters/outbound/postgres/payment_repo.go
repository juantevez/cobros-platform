package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgPaymentRepository struct {
	pool *pgxpool.Pool
}

func NewPaymentRepository(pool *pgxpool.Pool) *pgPaymentRepository {
	return &pgPaymentRepository{pool: pool}
}

func (r *pgPaymentRepository) Save(ctx context.Context, p *domain.Payment) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
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
		return fmt.Errorf("payment repo: save: %w", err)
	}
	return nil
}

func (r *pgPaymentRepository) Update(ctx context.Context, p *domain.Payment) error {
	conn := postgres.ConnFromContext(ctx, r.pool)

	_, err := conn.Exec(ctx, `
		UPDATE payments SET
			status=$2, psp_name=$3, psp_reference=$4,
			platform_fee=$5, psp_fee=$6,
			failure_reason=$7,
			authorized_at=$8, captured_at=$9, failed_at=$10,
			updated_at=$11
		WHERE id=$1`,
		p.ID().String(),
		p.Status().String(), nullStr(p.PSPName()), nullStr(p.PSPReference()),
		p.PlatformFee().Amount(), p.PSPFee().Amount(),
		nullStr(p.FailureReason()),
		p.AuthorizedAt(), p.CapturedAt(), p.FailedAt(),
		p.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("payment repo: update: %w", err)
	}
	return nil
}

func (r *pgPaymentRepository) FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String())
	return scanPayment(row)
}

func (r *pgPaymentRepository) FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payment, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseSelect+" WHERE tenant_id=$1 AND idempotency_key=$2",
		tenantID.String(), key)
	return scanPayment(row)
}

const baseSelect = `
	SELECT id, tenant_id, idempotency_key, checkout_id,
	       amount, currency, platform_fee, psp_fee,
	       payer_name, payer_email, payer_doc_type, payer_doc_num,
	       payment_method, psp_name, psp_reference,
	       status, failure_reason, metadata,
	       authorized_at, captured_at, failed_at,
	       created_at, updated_at
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
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &idempKey, &checkoutID,
		&amount, &currency, &platformFee, &pspFee,
		&payerName, &payerEmail, &payerDocType, &payerDocNum,
		&method, &pspName, &pspRef,
		&status, &failReason, &metaJSON,
		&authorizedAt, &capturedAt, &failedAt,
		&createdAt, &updatedAt,
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
		createdAt.UTC(), updatedAt.UTC(),
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
