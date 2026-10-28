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

// rlsTenantKey es la variable de sesión que leen las políticas RLS de aml_alerts.
const rlsTenantKey = "app.current_tenant"

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

// Update dispone la alerta con concurrencia optimista: el UPDATE solo aplica
// si la fila sigue en estado 'open'. Si otra resolución concurrente ya la
// dispuso, RowsAffected == 0 y retornamos ErrAlertNotOpen para que el caso de
// uso responda 409 en lugar de publicar un segundo AlertResolvedEvent.
func (r *AlertRepository) Update(ctx context.Context, a *domain.Alert) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	tag, err := conn.Exec(ctx, `
		UPDATE aml_alerts SET status=$2, note=$3, resolved_at=$4, resolved_by=$5
		WHERE id=$1 AND status='open'`,
		a.ID().String(), a.Status().String(), nullStr(a.Note()), a.ResolvedAt(),
		nullStr(a.ResolvedBy()),
	)
	if err != nil {
		return fmt.Errorf("compliance repo: update alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrAlertNotOpen
	}
	return nil
}

const alertBaseSelect = `
	SELECT id, tenant_id, alert_type, risk_level, status, subject, score,
	       details, COALESCE(note,''), COALESCE(resolved_by,''), created_at, resolved_at
	FROM aml_alerts`

// FindByID busca por ID. Si el contexto trae tenant (JWT), lo fija como
// variable RLS para que la lectura funcione aunque corra fuera de una tx.
func (r *AlertRepository) FindByID(ctx context.Context, id domain.AlertID) (*domain.Alert, error) {
	if tenantID, ok := pkgpostgres.TenantIDFromContext(ctx); ok {
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	}
	var a *domain.Alert
	err := pkgpostgres.RunWithAppTenant(ctx, r.pool, func(ctx context.Context) error {
		conn := pkgpostgres.ConnFromContext(ctx, r.pool)
		var err error
		a, err = scanAlert(conn.QueryRow(ctx, alertBaseSelect+" WHERE id=$1", id.String()))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrAlertNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("compliance repo: find alert: %w", err)
	}
	return a, nil
}

// ListByTenant fija app.current_tenant al tenant listado: el aislamiento no
// depende solo del WHERE tenant_id explícito.
func (r *AlertRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, statusFilter domain.AlertStatus, limit int) ([]*domain.Alert, error) {
	args := []any{tenantID.String()}
	q := alertBaseSelect + " WHERE tenant_id=$1"
	if statusFilter != "" {
		args = append(args, statusFilter.String())
		q += fmt.Sprintf(" AND status=$%d", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	var alerts []*domain.Alert
	ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID.String())
	err := pkgpostgres.RunWithAppTenant(ctx, r.pool, func(ctx context.Context) error {
		rows, err := pkgpostgres.ConnFromContext(ctx, r.pool).Query(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("compliance repo: list alerts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAlert(rows)
			if err != nil {
				return err
			}
			alerts = append(alerts, a)
		}
		return rows.Err()
	})
	return alerts, err
}

// scanner abstrae pgx.Row y pgx.Rows para reusar el scan.
type scanner interface {
	Scan(dest ...any) error
}

func scanAlert(s scanner) (*domain.Alert, error) {
	var (
		idStr, tenantIDStr, alertType, riskLevel, status, subject string
		note, resolvedBy                                          string
		score                                                     int
		detailsRaw                                                []byte
		createdAt                                                 time.Time
		resolvedAt                                                *time.Time
	)
	if err := s.Scan(&idStr, &tenantIDStr, &alertType, &riskLevel, &status,
		&subject, &score, &detailsRaw, &note, &resolvedBy, &createdAt, &resolvedAt); err != nil {
		return nil, err
	}
	details := map[string]string{}
	if len(detailsRaw) > 0 {
		_ = json.Unmarshal(detailsRaw, &details)
	}
	return domain.ReconstituteAlert(
		domain.AlertID(idStr), domain.TenantID(tenantIDStr),
		domain.AlertType(alertType), domain.RiskLevel(riskLevel),
		domain.AlertStatus(status), subject, score, details, note, resolvedBy,
		createdAt.UTC(), resolvedAt,
	), nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
