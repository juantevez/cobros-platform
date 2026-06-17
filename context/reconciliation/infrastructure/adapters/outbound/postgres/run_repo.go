package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgRunRepository struct{ pool *pgxpool.Pool }

func NewRunRepository(pool *pgxpool.Pool) *pgRunRepository {
	return &pgRunRepository{pool: pool}
}

func (r *pgRunRepository) Save(ctx context.Context, run *domain.ReconciliationRun) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
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
}

func (r *pgRunRepository) Update(ctx context.Context, run *domain.ReconciliationRun) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE reconciliation_runs SET
			status=$2, total_records=$3, matched_count=$4,
			discrepancy_count=$5, error_msg=$6, completed_at=$7
		WHERE id=$1`,
		run.ID().String(), run.Status().String(),
		run.TotalRecords(), run.MatchedCount(), run.DiscrepancyCount(),
		nullStr(run.ErrorMsg()), run.CompletedAt(),
	)
	return wrapErr("update run", err)
}

func (r *pgRunRepository) FindByID(ctx context.Context, id domain.RunID) (*domain.ReconciliationRun, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseRunSelect+" WHERE id=$1", id.String())
	return scanRun(row)
}

func (r *pgRunRepository) List(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.ReconciliationRun, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if tenantID.String() == "" {
		rows, err = r.pool.Query(ctx, baseRunSelect+" ORDER BY created_at DESC LIMIT $1", limit)
	} else {
		rows, err = r.pool.Query(ctx, baseRunSelect+" WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2",
			tenantID.String(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("run repo: list: %w", err)
	}
	defer rows.Close()

	var runs []*domain.ReconciliationRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
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
