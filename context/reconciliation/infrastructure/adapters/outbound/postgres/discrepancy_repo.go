package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgDiscrepancyRepository struct{ pool pkgpostgres.Conn }

func NewDiscrepancyRepository(pool pkgpostgres.Conn) *pgDiscrepancyRepository {
	return &pgDiscrepancyRepository{pool: pool}
}

// SaveAll inserta las discrepancias de un run (todas del mismo tenant).
func (r *pgDiscrepancyRepository) SaveAll(ctx context.Context, ds []*domain.Discrepancy) error {
	if len(ds) == 0 {
		return nil
	}
	return withTenant(ctx, r.pool, ds[0].TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		for _, d := range ds {
			if _, err := conn.Exec(ctx, `
				INSERT INTO reconciliation_discrepancies
					(id, run_id, tenant_id, type, record_id,
					 system_value, external_value, status, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				d.ID().String(), d.RunID().String(), nullStr(d.TenantID().String()),
				d.Type().String(), d.RecordID(),
				d.SystemValue(), d.ExternalValue(),
				d.Status().String(), d.CreatedAt(),
			); err != nil {
				return fmt.Errorf("discrepancy repo: save %s: %w", d.ID(), err)
			}
		}
		return nil
	})
}

func (r *pgDiscrepancyRepository) Update(ctx context.Context, d *domain.Discrepancy) error {
	return withTenant(ctx, r.pool, d.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			UPDATE reconciliation_discrepancies SET
				status=$2, notes=$3, resolved_by=$4, resolved_at=$5
			WHERE id=$1`,
			d.ID().String(), d.Status().String(),
			nullStr(d.Notes()), nullStr(d.ResolvedBy()), d.ResolvedAt(),
		)
		return wrapErr("update discrepancy", err)
	})
}

// FindByID busca con el tenant del caller: RLS oculta las discrepancias ajenas.
func (r *pgDiscrepancyRepository) FindByID(ctx context.Context, id domain.DiscrepancyID) (*domain.Discrepancy, error) {
	var d *domain.Discrepancy
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		d, err = scanDiscrepancy(conn.QueryRow(ctx, baseDiscSelect+" WHERE id=$1", id.String()))
		return err
	})
	return d, err
}

func (r *pgDiscrepancyRepository) ListByRun(
	ctx context.Context,
	runID domain.RunID,
	statusFilter string,
	limit int,
) ([]*domain.Discrepancy, error) {
	var result []*domain.Discrepancy
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var (
			rows pgx.Rows
			err  error
		)
		if statusFilter != "" {
			rows, err = conn.Query(ctx,
				baseDiscSelect+" WHERE run_id=$1 AND status=$2 ORDER BY created_at LIMIT $3",
				runID.String(), statusFilter, limit)
		} else {
			rows, err = conn.Query(ctx,
				baseDiscSelect+" WHERE run_id=$1 ORDER BY created_at LIMIT $2",
				runID.String(), limit)
		}
		if err != nil {
			return fmt.Errorf("discrepancy repo: list: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			d, err := scanDiscrepancy(rows)
			if err != nil {
				return err
			}
			result = append(result, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

const baseDiscSelect = `
	SELECT id, run_id, COALESCE(tenant_id::text,''), type, record_id,
	       COALESCE(system_value,''), COALESCE(external_value,''),
	       status, COALESCE(notes,''), COALESCE(resolved_by,''),
	       resolved_at, created_at
	FROM reconciliation_discrepancies`

func scanDiscrepancy(row interface{ Scan(...any) error }) (*domain.Discrepancy, error) {
	var (
		idStr, runIDStr, tenantIDStr      string
		typeStr, recordID, sysVal, extVal string
		statusStr, notes, resolvedBy      string
		resolvedAt                        *time.Time
		createdAt                         time.Time
	)
	if err := row.Scan(
		&idStr, &runIDStr, &tenantIDStr,
		&typeStr, &recordID, &sysVal, &extVal,
		&statusStr, &notes, &resolvedBy,
		&resolvedAt, &createdAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrDiscrepancyNotFound
		}
		return nil, fmt.Errorf("discrepancy repo: scan: %w", err)
	}

	return domain.ReconstituteDiscrepancy(
		domain.DiscrepancyID(idStr),
		domain.RunID(runIDStr),
		domain.TenantID(tenantIDStr),
		domain.DiscrepancyType(typeStr),
		recordID, sysVal, extVal,
		domain.DiscrepancyStatus(statusStr),
		notes, resolvedBy, resolvedAt, createdAt.UTC(),
	), nil
}
