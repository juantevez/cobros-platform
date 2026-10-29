// Package postgres implementa los puertos de Compliance sobre PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/compliance/application"
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

// SaveUnlessRecent aplica el cooldown de las reglas de ventana. Dentro de la
// tx del caso de uso toma un advisory lock por (tenant, prefijo) para
// serializar pagos concurrentes del mismo tenant y regla; luego, si ya hay una
// alerta reciente del mismo tipo con ese prefijo de subject, retorna
// ErrDuplicateAlert (no-op idempotente) y si no, inserta.
func (r *AlertRepository) SaveUnlessRecent(ctx context.Context, a *domain.Alert, subjectPrefix string, since time.Time) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"aml_window:"+a.TenantID().String()+":"+subjectPrefix); err != nil {
		return fmt.Errorf("compliance repo: cooldown lock: %w", err)
	}
	var recent bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM aml_alerts
			WHERE tenant_id=$1 AND alert_type=$2 AND starts_with(subject, $3) AND created_at > $4)`,
		a.TenantID().String(), a.Type().String(), subjectPrefix, since,
	).Scan(&recent); err != nil {
		return fmt.Errorf("compliance repo: cooldown check: %w", err)
	}
	if recent {
		return domain.ErrDuplicateAlert
	}
	return r.Save(ctx, a)
}

// Update persiste la transición de revisión con concurrencia optimista: el
// UPDATE solo aplica si la fila sigue en el estado previo (`from`). Si otra
// revisión concurrente ya la movió, RowsAffected == 0 y retornamos
// ErrAlertNotOpen para que el caso de uso responda 409 sin publicar eventos.
func (r *AlertRepository) Update(ctx context.Context, a *domain.Alert, from domain.AlertStatus) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	tag, err := conn.Exec(ctx, `
		UPDATE aml_alerts
		SET status=$3, note=$4, resolved_at=$5, resolved_by=$6,
		    proposed_status=$7, proposed_by=$8, proposed_at=$9, approval_note=$10
		WHERE id=$1 AND status=$2`,
		a.ID().String(), from.String(), a.Status().String(), nullStr(a.Note()),
		a.ResolvedAt(), nullStr(a.ResolvedBy()),
		nullStr(a.ProposedStatus().String()), nullStr(a.ProposedBy()), a.ProposedAt(),
		nullStr(a.ApprovalNote()),
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
	       details, COALESCE(note,''), COALESCE(resolved_by,''), created_at, resolved_at,
	       COALESCE(proposed_status,''), COALESCE(proposed_by,''), proposed_at,
	       COALESCE(approval_note,'')
	FROM aml_alerts`

// FindByID busca por ID. La revisión de cumplimiento opera sobre alertas de
// cualquier comercio: bajo RLS efectivo solo encuentra la fila si el contexto
// trae WithPlatformOperator (o el tenant dueño de la alerta).
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

// List lista alertas cross-tenant (requiere WithPlatformOperator bajo RLS
// efectivo). Con filtro de tenant fija además app.current_tenant a ese tenant.
func (r *AlertRepository) List(ctx context.Context, filter application.AlertFilter, limit int) ([]*domain.Alert, error) {
	var (
		args  []any
		where []string
	)
	if filter.TenantID != "" {
		args = append(args, filter.TenantID.String())
		where = append(where, fmt.Sprintf("tenant_id=$%d", len(args)))
		ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, filter.TenantID.String())
	}
	if filter.Status != "" {
		args = append(args, filter.Status.String())
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	q := alertBaseSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	var alerts []*domain.Alert
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
		snap                                                       domain.AlertSnapshot
		idStr, tenantIDStr, alertType, riskLevel, status, proposed string
		detailsRaw                                                 []byte
	)
	if err := s.Scan(&idStr, &tenantIDStr, &alertType, &riskLevel, &status,
		&snap.Subject, &snap.Score, &detailsRaw, &snap.Note, &snap.ResolvedBy,
		&snap.CreatedAt, &snap.ResolvedAt,
		&proposed, &snap.ProposedBy, &snap.ProposedAt, &snap.ApprovalNote); err != nil {
		return nil, err
	}
	snap.Details = map[string]string{}
	if len(detailsRaw) > 0 {
		_ = json.Unmarshal(detailsRaw, &snap.Details)
	}
	snap.ID, snap.TenantID = domain.AlertID(idStr), domain.TenantID(tenantIDStr)
	snap.Type, snap.RiskLevel = domain.AlertType(alertType), domain.RiskLevel(riskLevel)
	snap.Status, snap.ProposedStatus = domain.AlertStatus(status), domain.AlertStatus(proposed)
	snap.CreatedAt = snap.CreatedAt.UTC()
	return domain.ReconstituteAlert(snap), nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
