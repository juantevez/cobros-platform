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

type pgRunRepository struct{ pool pkgpostgres.Conn }

func NewRunRepository(pool pkgpostgres.Conn) *pgRunRepository {
	return &pgRunRepository{pool: pool}
}

func (r *pgRunRepository) Save(ctx context.Context, run *domain.ReconciliationRun) error {
	return withTenant(ctx, r.pool, run.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO reconciliation_runs
				(id, tenant_id, type, status, period_from, period_to,
				 total_records, matched_count, discrepancy_count,
				 error_msg, created_at, completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			run.ID().String(), nullStr(run.TenantID().String()),
			run.Type().String(), run.Status().String(),
			run.PeriodFrom(), run.PeriodTo(),
			run.TotalRecords(), run.MatchedCount(), run.DiscrepancyCount(),
			nullStr(run.ErrorMsg()), run.CreatedAt(), run.CompletedAt(),
		)
		return wrapErr("save run", err)
	})
}

// Update guarda el run condicionado a que en la base siga en el estado from.
// Cero filas afectadas = otro proceso lo cambió antes (o no existe).
func (r *pgRunRepository) Update(ctx context.Context, run *domain.ReconciliationRun, from domain.RunStatus) error {
	return withTenant(ctx, r.pool, run.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		tag, err := conn.Exec(ctx, `
			UPDATE reconciliation_runs SET
				status=$2, total_records=$3, matched_count=$4,
				discrepancy_count=$5, error_msg=$6, completed_at=$7
			WHERE id=$1 AND status=$8`,
			run.ID().String(), run.Status().String(),
			run.TotalRecords(), run.MatchedCount(), run.DiscrepancyCount(),
			nullStr(run.ErrorMsg()), run.CompletedAt(), from.String(),
		)
		if err != nil {
			return wrapErr("update run", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: run %s is no longer %q", domain.ErrRunAlreadyRunning, run.ID(), from)
		}
		return nil
	})
}

// FindByID busca con el tenant del caller: RLS oculta los runs ajenos.
func (r *pgRunRepository) FindByID(ctx context.Context, id domain.RunID) (*domain.ReconciliationRun, error) {
	var run *domain.ReconciliationRun
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		run, err = scanRun(conn.QueryRow(ctx, baseRunSelect+" WHERE id=$1", id.String()))
		return err
	})
	return run, err
}

func (r *pgRunRepository) List(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.ReconciliationRun, error) {
	var runs []*domain.ReconciliationRun
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var (
			rows pgx.Rows
			err  error
		)
		if tenantID.String() == "" {
			rows, err = conn.Query(ctx, baseRunSelect+" ORDER BY created_at DESC LIMIT $1", limit)
		} else {
			rows, err = conn.Query(ctx, baseRunSelect+" WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2",
				tenantID.String(), limit)
		}
		if err != nil {
			return fmt.Errorf("run repo: list: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			run, err := scanRun(rows)
			if err != nil {
				return err
			}
			runs = append(runs, run)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return runs, nil
}

const baseRunSelect = `
	SELECT id, COALESCE(tenant_id::text,''), type, status,
	       period_from, period_to,
	       total_records, matched_count, discrepancy_count,
	       COALESCE(error_msg,''), created_at, completed_at
	FROM reconciliation_runs`

func scanRun(row interface{ Scan(...any) error }) (*domain.ReconciliationRun, error) {
	var (
		idStr, tenantIDStr, typeStr, statusStr string
		periodFrom, periodTo, createdAt        time.Time
		total, matched, discrepancyCount       int
		errorMsg                               string
		completedAt                            *time.Time
	)
	if err := row.Scan(
		&idStr, &tenantIDStr, &typeStr, &statusStr,
		&periodFrom, &periodTo,
		&total, &matched, &discrepancyCount,
		&errorMsg, &createdAt, &completedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrRunNotFound
		}
		return nil, fmt.Errorf("run repo: scan: %w", err)
	}

	reconcType, _ := domain.ParseReconciliationType(typeStr)
	return domain.ReconstituteRun(
		domain.RunID(idStr), domain.TenantID(tenantIDStr),
		reconcType, domain.RunStatus(statusStr),
		periodFrom.UTC(), periodTo.UTC(),
		total, matched, discrepancyCount,
		errorMsg, createdAt.UTC(), completedAt,
	), nil
}

func wrapErr(op string, err error) error {
	if err != nil {
		return fmt.Errorf("reconciliation %s: %w", op, err)
	}
	return nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
