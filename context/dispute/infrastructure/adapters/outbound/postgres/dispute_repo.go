package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgDisputeRepository struct{ pool *pgxpool.Pool }

func NewDisputeRepository(pool *pgxpool.Pool) *pgDisputeRepository {
	return &pgDisputeRepository{pool: pool}
}

func (r *pgDisputeRepository) Save(ctx context.Context, d *domain.Dispute) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO disputes
			(id, tenant_id, payment_id, psp_reference, amount, currency,
			 reason, status, response_note, resolved_note,
			 deadline, opened_at, responded_at, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		d.ID().String(), d.TenantID().String(),
		d.PaymentID(), nullStr(d.PSPReference()),
		d.Amount(), d.Currency(),
		d.Reason().String(), d.Status().String(),
		nullStr(d.ResponseNote()), nullStr(d.ResolvedNote()),
		d.Deadline(), d.OpenedAt(),
		d.RespondedAt(), d.ResolvedAt(),
	)
	if err != nil {
		return fmt.Errorf("dispute repo: save: %w", err)
	}
	return r.saveEvidence(ctx, conn, d)
}

func (r *pgDisputeRepository) Update(ctx context.Context, d *domain.Dispute) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE disputes SET
			status=$2, response_note=$3, resolved_note=$4,
			responded_at=$5, resolved_at=$6
		WHERE id=$1`,
		d.ID().String(), d.Status().String(),
		nullStr(d.ResponseNote()), nullStr(d.ResolvedNote()),
		d.RespondedAt(), d.ResolvedAt(),
	)
	if err != nil {
		return fmt.Errorf("dispute repo: update: %w", err)
	}
	return r.saveEvidence(ctx, conn, d)
}

func (r *pgDisputeRepository) FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String())
	return r.scanWithEvidence(ctx, conn, row)
}

func (r *pgDisputeRepository) FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseSelect+" WHERE payment_id=$1", paymentID)
	d, err := r.scanWithEvidence(ctx, conn, row)
	if errors.Is(err, domain.ErrDisputeNotFound) {
		return nil, domain.ErrDisputeNotFound
	}
	return d, err
}

func (r *pgDisputeRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Dispute, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if statusFilter != "" {
		rows, err = r.pool.Query(ctx,
			baseSelect+" WHERE tenant_id=$1 AND status=$2 ORDER BY opened_at DESC LIMIT $3",
			tenantID.String(), statusFilter, limit)
	} else {
		rows, err = r.pool.Query(ctx,
			baseSelect+" WHERE tenant_id=$1 ORDER BY opened_at DESC LIMIT $2",
			tenantID.String(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("dispute repo: list: %w", err)
	}
	defer rows.Close()

	var disputes []*domain.Dispute
	for rows.Next() {
		d, err := r.scanWithEvidence(ctx, r.pool, rows)
		if err != nil {
			return nil, err
		}
		disputes = append(disputes, d)
	}
	return disputes, rows.Err()
}

func (r *pgDisputeRepository) ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error) {
	rows, err := r.pool.Query(ctx,
		baseSelect+" WHERE status='open' AND deadline < $1 ORDER BY deadline ASC LIMIT $2",
		now, limit)
	if err != nil {
		return nil, fmt.Errorf("dispute repo: list overdue: %w", err)
	}
	defer rows.Close()

	var disputes []*domain.Dispute
	for rows.Next() {
		d, err := r.scanWithEvidence(ctx, r.pool, rows)
		if err != nil {
			return nil, err
		}
		disputes = append(disputes, d)
	}
	return disputes, rows.Err()
}

const baseSelect = `
	SELECT id, tenant_id, payment_id, COALESCE(psp_reference,''),
	       amount, currency, reason, status,
	       COALESCE(response_note,''), COALESCE(resolved_note,''),
	       deadline, opened_at, responded_at, resolved_at
	FROM disputes`

// connQuerier unifica pgxpool.Pool y pkgpostgres.Conn para las queries de evidencia.
type connQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (interface{ RowsAffected() int64 }, error)
}

func (r *pgDisputeRepository) scanWithEvidence(ctx context.Context, conn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, row interface{ Scan(...any) error }) (*domain.Dispute, error) {
	var (
		idStr, tenantIDStr, paymentID, pspRef string
		amount                                int64
		currency, reasonStr, statusStr        string
		responseNote, resolvedNote            string
		deadline, openedAt                    time.Time
		respondedAt, resolvedAt               *time.Time
	)
	if err := row.Scan(
		&idStr, &tenantIDStr, &paymentID, &pspRef,
		&amount, &currency, &reasonStr, &statusStr,
		&responseNote, &resolvedNote,
		&deadline, &openedAt, &respondedAt, &resolvedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrDisputeNotFound
		}
		return nil, fmt.Errorf("dispute repo: scan: %w", err)
	}

	// Cargar evidencia.
	evRows, err := conn.Query(ctx, `
		SELECT id, evidence_type, reference, COALESCE(description,''), submitted_at
		FROM dispute_evidence WHERE dispute_id=$1 ORDER BY submitted_at`, idStr)
	if err != nil {
		return nil, fmt.Errorf("dispute repo: load evidence: %w", err)
	}
	defer evRows.Close()

	var evidence []domain.Evidence
	for evRows.Next() {
		var eid, evType, ref, desc string
		var submittedAt time.Time
		evRows.Scan(&eid, &evType, &ref, &desc, &submittedAt)
		evidence = append(evidence, domain.ReconstituteEvidence(
			domain.EvidenceID(eid), evType, ref, desc, submittedAt.UTC(),
		))
	}

	reason, _ := domain.ParseDisputeReason(reasonStr)
	return domain.ReconstituteDispute(
		domain.DisputeID(idStr), domain.TenantID(tenantIDStr),
		paymentID, pspRef,
		amount, currency,
		reason, domain.DisputeStatus(statusStr),
		evidence,
		responseNote, resolvedNote,
		deadline.UTC(), openedAt.UTC(),
		respondedAt, resolvedAt,
	), nil
}

func (r *pgDisputeRepository) saveEvidence(ctx context.Context, conn pkgpostgres.Conn, d *domain.Dispute) error {
	for _, e := range d.Evidence() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO dispute_evidence
				(id, dispute_id, evidence_type, reference, description, submitted_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (id) DO NOTHING`,
			e.ID().String(), d.ID().String(),
			e.EvidenceType(), e.Reference(),
			nullStr(e.Description()), e.SubmittedAt(),
		); err != nil {
			return fmt.Errorf("dispute repo: save evidence: %w", err)
		}
	}
	return nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
