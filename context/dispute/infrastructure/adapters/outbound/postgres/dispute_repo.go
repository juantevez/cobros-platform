package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

const (
	pgErrUniqueViolation    = "23505"
	paymentUniqueConstraint = "disputes_payment_id_key"
)

type pgDisputeRepository struct{ pool pkgpostgres.Conn }

func NewDisputeRepository(pool pkgpostgres.Conn) *pgDisputeRepository {
	return &pgDisputeRepository{pool: pool}
}

func (r *pgDisputeRepository) Save(ctx context.Context, d *domain.Dispute) error {
	return withTenant(ctx, r.pool, d.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO disputes
				(id, tenant_id, payment_id, psp_reference, amount, currency,
				 reason, status, response_note, resolved_note,
				 deadline, opened_at, responded_at, resolved_at, version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			d.ID().String(), d.TenantID().String(),
			d.PaymentID(), nullStr(d.PSPReference()),
			d.Amount(), d.Currency(),
			d.Reason().String(), d.Status().String(),
			nullStr(d.ResponseNote()), nullStr(d.ResolvedNote()),
			d.Deadline(), d.OpenedAt(),
			d.RespondedAt(), d.ResolvedAt(),
			d.Version(),
		)
		if err != nil {
			// Dos aperturas concurrentes del mismo pago: la segunda choca con
			// UNIQUE(payment_id). El caso de uso lo traduce a 409.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation && pgErr.ConstraintName == paymentUniqueConstraint {
				return fmt.Errorf("dispute repo: save: %w", application.ErrDuplicatePaymentKey)
			}
			return fmt.Errorf("dispute repo: save: %w", err)
		}
		return r.saveEvidence(ctx, conn, d)
	})
}

func (r *pgDisputeRepository) Update(ctx context.Context, d *domain.Dispute) error {
	return withTenant(ctx, r.pool, d.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		// Cada transición incrementa la versión una vez: el UPDATE exige la que
		// se leyó. Si otra transición ganó la carrera, 0 filas → ErrOptimisticLock.
		tag, err := conn.Exec(ctx, `
			UPDATE disputes SET
				status=$2, response_note=$3, resolved_note=$4,
				responded_at=$5, resolved_at=$6, version=$7
			WHERE id=$1 AND version=$8`,
			d.ID().String(), d.Status().String(),
			nullStr(d.ResponseNote()), nullStr(d.ResolvedNote()),
			d.RespondedAt(), d.ResolvedAt(),
			d.Version(), d.Version()-1,
		)
		if err != nil {
			return fmt.Errorf("dispute repo: update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("dispute repo: update %s: %w", d.ID(), domain.ErrOptimisticLock)
		}
		return r.saveEvidence(ctx, conn, d)
	})
}

// FindByID y FindByPaymentID corren con el tenant del llamador; el operador
// de plataforma (WithPlatformOperator en el contexto) ve las de cualquiera.
func (r *pgDisputeRepository) FindByID(ctx context.Context, id domain.DisputeID) (*domain.Dispute, error) {
	var d *domain.Dispute
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		d, err = r.scanWithEvidence(ctx, conn, conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String()))
		return err
	})
	return d, err
}

func (r *pgDisputeRepository) FindByPaymentID(ctx context.Context, paymentID string) (*domain.Dispute, error) {
	var d *domain.Dispute
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		d, err = r.scanWithEvidence(ctx, conn, conn.QueryRow(ctx, baseSelect+" WHERE payment_id=$1", paymentID))
		return err
	})
	if errors.Is(err, domain.ErrDisputeNotFound) {
		return nil, domain.ErrDisputeNotFound
	}
	return d, err
}

func (r *pgDisputeRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Dispute, error) {
	var disputes []*domain.Dispute
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var (
			rows pgx.Rows
			err  error
		)
		if statusFilter != "" {
			rows, err = conn.Query(ctx,
				baseSelect+" WHERE tenant_id=$1 AND status=$2 ORDER BY opened_at DESC LIMIT $3",
				tenantID.String(), statusFilter, limit)
		} else {
			rows, err = conn.Query(ctx,
				baseSelect+" WHERE tenant_id=$1 ORDER BY opened_at DESC LIMIT $2",
				tenantID.String(), limit)
		}
		if err != nil {
			return fmt.Errorf("dispute repo: list: %w", err)
		}
		disputes, err = r.scanAll(ctx, conn, rows)
		return err
	})
	return disputes, err
}

// ListAll retorna disputas de TODOS los tenants (vista operatoria global del
// back-office): corre con la política de operador (solo lectura).
func (r *pgDisputeRepository) ListAll(ctx context.Context, statusFilter string, limit int) ([]*domain.Dispute, error) {
	var disputes []*domain.Dispute
	err := asPlatformOperator(ctx, r.pool, func(ctx context.Context, conn pkgpostgres.Conn) error {
		var (
			rows pgx.Rows
			err  error
		)
		if statusFilter != "" {
			rows, err = conn.Query(ctx,
				baseSelect+" WHERE status=$1 ORDER BY opened_at DESC LIMIT $2",
				statusFilter, limit)
		} else {
			rows, err = conn.Query(ctx,
				baseSelect+" ORDER BY opened_at DESC LIMIT $1",
				limit)
		}
		if err != nil {
			return fmt.Errorf("dispute repo: list all: %w", err)
		}
		disputes, err = r.scanAll(ctx, conn, rows)
		return err
	})
	return disputes, err
}

// ListOverdue es cross-tenant (ExpiryPoller): corre con la política de
// operador. Cada vencimiento se escribe después con el tenant de la disputa.
func (r *pgDisputeRepository) ListOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.Dispute, error) {
	var disputes []*domain.Dispute
	err := asPlatformOperator(ctx, r.pool, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx,
			baseSelect+" WHERE status='open' AND deadline < $1 ORDER BY deadline ASC LIMIT $2",
			now, limit)
		if err != nil {
			return fmt.Errorf("dispute repo: list overdue: %w", err)
		}
		disputes, err = r.scanAll(ctx, conn, rows)
		return err
	})
	return disputes, err
}

const baseSelect = `
	SELECT id, tenant_id, payment_id, COALESCE(psp_reference,''),
	       amount, currency, reason, status,
	       COALESCE(response_note,''), COALESCE(resolved_note,''),
	       deadline, opened_at, responded_at, resolved_at, version
	FROM disputes`

// disputeRecord son las columnas de baseSelect, antes de cargar la evidencia.
type disputeRecord struct {
	idStr, tenantIDStr, paymentID, pspRef string
	amount                                int64
	currency, reasonStr, statusStr        string
	responseNote, resolvedNote            string
	deadline, openedAt                    time.Time
	respondedAt, resolvedAt               *time.Time
	version                               int64
}

func scanDisputeRecord(row interface{ Scan(...any) error }) (disputeRecord, error) {
	var rec disputeRecord
	if err := row.Scan(
		&rec.idStr, &rec.tenantIDStr, &rec.paymentID, &rec.pspRef,
		&rec.amount, &rec.currency, &rec.reasonStr, &rec.statusStr,
		&rec.responseNote, &rec.resolvedNote,
		&rec.deadline, &rec.openedAt, &rec.respondedAt, &rec.resolvedAt,
		&rec.version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rec, domain.ErrDisputeNotFound
		}
		return rec, fmt.Errorf("dispute repo: scan: %w", err)
	}
	return rec, nil
}

func (r *pgDisputeRepository) scanWithEvidence(ctx context.Context, conn pkgpostgres.Conn, row interface{ Scan(...any) error }) (*domain.Dispute, error) {
	rec, err := scanDisputeRecord(row)
	if err != nil {
		return nil, err
	}
	evidence, err := loadEvidence(ctx, conn, []string{rec.idStr})
	if err != nil {
		return nil, err
	}
	return rec.toDispute(evidence[rec.idStr]), nil
}

// scanAll lee todas las filas y después carga la evidencia de todas en una
// sola query (antes, una por disputa: N+1). Además, con una sola conexión (la
// de la sesión RLS) no se puede consultar mientras el cursor sigue abierto.
func (r *pgDisputeRepository) scanAll(ctx context.Context, conn pkgpostgres.Conn, rows pgx.Rows) ([]*domain.Dispute, error) {
	var recs []disputeRecord
	for rows.Next() {
		rec, err := scanDisputeRecord(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(recs))
	for i, rec := range recs {
		ids[i] = rec.idStr
	}
	evidence, err := loadEvidence(ctx, conn, ids)
	if err != nil {
		return nil, err
	}
	disputes := make([]*domain.Dispute, len(recs))
	for i, rec := range recs {
		disputes[i] = rec.toDispute(evidence[rec.idStr])
	}
	return disputes, nil
}

// loadEvidence trae la evidencia de las disputas dadas, agrupada por disputa.
func loadEvidence(ctx context.Context, conn pkgpostgres.Conn, disputeIDs []string) (map[string][]domain.Evidence, error) {
	rows, err := conn.Query(ctx, `
		SELECT dispute_id, id, evidence_type, reference, COALESCE(description,''), submitted_at
		FROM dispute_evidence WHERE dispute_id = ANY($1::uuid[]) ORDER BY submitted_at`, disputeIDs)
	if err != nil {
		return nil, fmt.Errorf("dispute repo: load evidence: %w", err)
	}
	defer rows.Close()

	byDispute := make(map[string][]domain.Evidence, len(disputeIDs))
	for rows.Next() {
		var disputeID, eid, evType, ref, desc string
		var submittedAt time.Time
		if err := rows.Scan(&disputeID, &eid, &evType, &ref, &desc, &submittedAt); err != nil {
			return nil, fmt.Errorf("dispute repo: scan evidence: %w", err)
		}
		byDispute[disputeID] = append(byDispute[disputeID], domain.ReconstituteEvidence(
			domain.EvidenceID(eid), evType, ref, desc, submittedAt.UTC(),
		))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dispute repo: load evidence: %w", err)
	}
	return byDispute, nil
}

func (rec disputeRecord) toDispute(evidence []domain.Evidence) *domain.Dispute {
	reason, _ := domain.ParseDisputeReason(rec.reasonStr)
	return domain.ReconstituteDispute(
		domain.DisputeID(rec.idStr), domain.TenantID(rec.tenantIDStr),
		rec.paymentID, rec.pspRef,
		rec.amount, rec.currency,
		reason, domain.DisputeStatus(rec.statusStr),
		evidence,
		rec.responseNote, rec.resolvedNote,
		rec.deadline.UTC(), rec.openedAt.UTC(),
		rec.respondedAt, rec.resolvedAt,
		rec.version,
	)
}

func (r *pgDisputeRepository) saveEvidence(ctx context.Context, conn pkgpostgres.Conn, d *domain.Dispute) error {
	for _, e := range d.Evidence() {
		if _, err := conn.Exec(ctx, `
			INSERT INTO dispute_evidence
				(id, dispute_id, tenant_id, evidence_type, reference, description, submitted_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (id) DO NOTHING`,
			e.ID().String(), d.ID().String(), d.TenantID().String(),
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
