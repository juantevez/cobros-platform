// Package postgres implementa los puertos de Compliance sobre PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type AlertRepository struct{ pool pkgpostgres.Conn }

func NewAlertRepository(pool pkgpostgres.Conn) *AlertRepository {
	return &AlertRepository{pool: pool}
}

func (r *AlertRepository) Save(ctx context.Context, a *domain.Alert) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	details, err := json.Marshal(a.Details())
	if err != nil {
		return fmt.Errorf("compliance repo: marshal details: %w", err)
	}
	_, err = conn.Exec(ctx, `
		INSERT INTO aml_alerts
			(id, tenant_id, alert_type, risk_level, status, subject, score, details, note, created_at, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		a.ID().String(), a.TenantID().String(),
		a.Type().String(), a.RiskLevel().String(), a.Status().String(),
		a.Subject(), a.Score(), details, nullStr(a.Note()),
		a.CreatedAt(), a.ResolvedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateAlert // unicidad (tenant, tipo, subject)
		}
		return fmt.Errorf("compliance repo: save alert: %w", err)
	}
	return nil
}

func (r *AlertRepository) Update(ctx context.Context, a *domain.Alert) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE aml_alerts SET status=$2, note=$3, resolved_at=$4 WHERE id=$1`,
		a.ID().String(), a.Status().String(), nullStr(a.Note()), a.ResolvedAt(),
	)
	if err != nil {
		return fmt.Errorf("compliance repo: update alert: %w", err)
	}
	return nil
}

const alertBaseSelect = `
	SELECT id, tenant_id, alert_type, risk_level, status, subject, score,
	       details, COALESCE(note,''), created_at, resolved_at
	FROM aml_alerts`

func (r *AlertRepository) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, alertBaseSelect+" WHERE id=$1", id.String())
	a, err := scanAlert(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAlertNotFound
	}
	return a, err
}

func (r *AlertRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter string, limit int) ([]*domain.Alert, error) {
	args := []any{tenantID.String()}
	q := alertBaseSelect + " WHERE tenant_id=$1"
	if statusFilter != "" {
		args = append(args, statusFilter)
		q += fmt.Sprintf(" AND status=$%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("compliance repo: list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []*domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// scanner abstrae pgx.Row y pgx.Rows para reusar el scan.
type scanner interface {
	Scan(dest ...any) error
}

func scanAlert(s scanner) (*domain.Alert, error) {
	var (
		idStr, tenantIDStr, alertType, riskLevel, status, subject, note string
		score                                                           int
		detailsRaw                                                      []byte
		createdAt                                                       time.Time
		resolvedAt                                                      *time.Time
	)
	if err := s.Scan(&idStr, &tenantIDStr, &alertType, &riskLevel, &status,
		&subject, &score, &detailsRaw, &note, &createdAt, &resolvedAt); err != nil {
		return nil, err
	}
	details := map[string]string{}
	if len(detailsRaw) > 0 {
		_ = json.Unmarshal(detailsRaw, &details)
	}
	return domain.ReconstituteAlert(
		domain.AlertID(idStr), domain.TenantID(tenantIDStr),
		domain.AlertType(alertType), domain.RiskLevel(riskLevel),
		domain.AlertStatus(status), subject, score, details, note,
		createdAt.UTC(), resolvedAt,
	), nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
