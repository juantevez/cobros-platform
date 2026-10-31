package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgDeliveryRepository struct {
	pool pkgpostgres.Conn
}

func NewDeliveryRepository(pool pkgpostgres.Conn) *pgDeliveryRepository {
	return &pgDeliveryRepository{pool: pool}
}

func (r *pgDeliveryRepository) Save(ctx context.Context, d *domain.WebhookDelivery) error {
	err := withTenant(ctx, r.pool, d.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		_, err := conn.Exec(ctx, `
			INSERT INTO webhook_deliveries
				(id, endpoint_id, tenant_id, event_type, event_id,
				 payload, status, attempt_count, next_retry_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			d.ID().String(), d.EndpointID().String(), d.TenantID().String(),
			d.EventType(), d.EventID(),
			[]byte(d.Payload()),
			d.Status().String(), d.AttemptCount(), d.NextRetryAt(),
			d.CreatedAt(), d.UpdatedAt(),
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("delivery repo: save: %w", err)
	}
	return nil
}

func (r *pgDeliveryRepository) Claim(ctx context.Context, id domain.DeliveryID, attemptCount int, now, until time.Time) (bool, error) {
	var claimed bool
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		tag, err := conn.Exec(ctx, `
			UPDATE webhook_deliveries SET claimed_until=$4
			WHERE id=$1
			  AND status IN ('pending','failed')
			  AND attempt_count=$2
			  AND (claimed_until IS NULL OR claimed_until < $3)`,
			id.String(), attemptCount, now, until,
		)
		claimed = err == nil && tag.RowsAffected() == 1
		return err
	})
	if err != nil {
		return false, fmt.Errorf("delivery repo: claim: %w", err)
	}
	return claimed, nil
}

func (r *pgDeliveryRepository) Update(ctx context.Context, d *domain.WebhookDelivery, prevAttemptCount int) error {
	return withTenant(ctx, r.pool, d.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		// Bloqueo optimista por attempt_count: si otro proceso registró un
		// intento mientras tanto (reclamo vencido), este resultado no lo pisa.
		tag, err := conn.Exec(ctx, `
			UPDATE webhook_deliveries SET
				status=$2, attempt_count=$3, next_retry_at=$4, updated_at=$5, claimed_until=NULL
			WHERE id=$1 AND attempt_count=$6`,
			d.ID().String(),
			d.Status().String(), d.AttemptCount(), d.NextRetryAt(), d.UpdatedAt(),
			prevAttemptCount,
		)
		if err != nil {
			return fmt.Errorf("delivery repo: update: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("delivery repo: update %s: %w", d.ID(), domain.ErrDeliveryInProgress)
		}

		// Persistir el último attempt.
		attempts := d.Attempts()
		if len(attempts) > 0 {
			last := attempts[len(attempts)-1]
			_, err = conn.Exec(ctx, `
				INSERT INTO webhook_delivery_attempts
					(id, delivery_id, tenant_id, attempt_num, http_status, response_body, error_msg, duration_ms, attempted_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (id) DO NOTHING`,
				last.ID().String(), d.ID().String(), d.TenantID().String(),
				last.AttemptNum(), last.HTTPStatus(),
				nullStr(last.ResponseBody()), nullStr(last.ErrMsg()),
				last.DurationMs(), last.AttemptedAt(),
			)
			if err != nil {
				return fmt.Errorf("delivery repo: save attempt: %w", err)
			}
		}
		return nil
	})
}

func (r *pgDeliveryRepository) FindByID(ctx context.Context, id domain.DeliveryID) (*domain.WebhookDelivery, error) {
	var d *domain.WebhookDelivery
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		d, err = r.scanWithAttempts(ctx, conn, conn.QueryRow(ctx, baseDeliverySelect+" WHERE d.id=$1", id.String()))
		return err
	})
	return d, err
}

func (r *pgDeliveryRepository) FindByEventAndEndpoint(
	ctx context.Context,
	endpointID domain.EndpointID,
	eventID string,
) (*domain.WebhookDelivery, error) {
	var d *domain.WebhookDelivery
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		d, err = r.scanWithAttempts(ctx, conn, conn.QueryRow(ctx,
			baseDeliverySelect+" WHERE d.endpoint_id=$1 AND d.event_id=$2",
			endpointID.String(), eventID,
		))
		return err
	})
	return d, err
}

func (r *pgDeliveryRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.WebhookDelivery, error) {
	var result []*domain.WebhookDelivery
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx,
			baseDeliverySelect+" WHERE d.tenant_id=$1 ORDER BY d.created_at DESC LIMIT $2",
			tenantID.String(), limit,
		)
		if err != nil {
			return fmt.Errorf("delivery repo: list: %w", err)
		}
		defer rows.Close()
		result, err = scanDeliveries(rows)
		return err
	})
	return result, err
}

func (r *pgDeliveryRepository) ListDueForRetry(ctx context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error) {
	// Cross-tenant: corre con la política de operador (solo lectura). Cada
	// despacho fija después el tenant de su delivery.
	// El JOIN excluye endpoints desactivados: sus deliveries no deben ocupar
	// lugar en el batch (quedarían re-seleccionadas en cada ciclo). Las
	// reclamadas por otro proceso tampoco: se están despachando.
	// Para el poller no cargamos los attempts (no los necesita para dispatch).
	var result []*domain.WebhookDelivery
	err := asPlatformOperator(ctx, r.pool, func(ctx context.Context, conn pkgpostgres.Conn) error {
		rows, err := conn.Query(ctx, `
			SELECT d.id, d.endpoint_id, d.tenant_id, d.event_type, d.event_id,
			       d.payload, d.status, d.attempt_count, d.next_retry_at,
			       d.created_at, d.updated_at
			FROM webhook_deliveries d
			JOIN webhook_endpoints e ON e.id = d.endpoint_id AND e.active = true
			WHERE d.status IN ('pending','failed')
			  AND d.next_retry_at <= $1
			  AND (d.claimed_until IS NULL OR d.claimed_until < $1)
			ORDER BY d.next_retry_at ASC
			LIMIT $2`,
			now, limit,
		)
		if err != nil {
			return fmt.Errorf("delivery repo: list due: %w", err)
		}
		defer rows.Close()
		result, err = scanDeliveries(rows)
		return err
	})
	return result, err
}

const baseDeliverySelect = `
	SELECT d.id, d.endpoint_id, d.tenant_id, d.event_type, d.event_id,
	       d.payload, d.status, d.attempt_count, d.next_retry_at,
	       d.created_at, d.updated_at
	FROM webhook_deliveries d`

func (r *pgDeliveryRepository) scanWithAttempts(ctx context.Context, conn pkgpostgres.Conn, row pgx.Row) (*domain.WebhookDelivery, error) {
	d, err := scanDeliveryRow(row)
	if err != nil {
		return nil, err
	}

	// Cargar attempts.
	aRows, err := conn.Query(ctx, `
		SELECT id, attempt_num, http_status, COALESCE(response_body,''),
		       COALESCE(error_msg,''), duration_ms, attempted_at
		FROM webhook_delivery_attempts
		WHERE delivery_id=$1 ORDER BY attempt_num`,
		d.ID().String(),
	)
	if err != nil {
		return nil, fmt.Errorf("delivery repo: load attempts: %w", err)
	}
	defer aRows.Close()

	var attempts []domain.DeliveryAttempt
	for aRows.Next() {
		var aid, body, errMsg string
		var num, httpStatus int
		var durationMs int64
		var at time.Time
		if err := aRows.Scan(&aid, &num, &httpStatus, &body, &errMsg, &durationMs, &at); err != nil {
			return nil, fmt.Errorf("delivery repo: scan attempt: %w", err)
		}
		attempts = append(attempts, domain.ReconstituteAttempt(
			domain.AttemptID(aid), num, httpStatus, body, errMsg, durationMs, at.UTC(),
		))
	}

	return domain.ReconstituteDelivery(
		d.ID(), d.EndpointID(), d.TenantID(),
		d.EventType(), d.EventID(), d.Payload(),
		d.Status(), d.AttemptCount(), d.NextRetryAt(),
		attempts, d.CreatedAt(), d.UpdatedAt(),
	), nil
}

// scanDeliveries escanea un listado sin cargar los intentos.
func scanDeliveries(rows pgx.Rows) ([]*domain.WebhookDelivery, error) {
	var result []*domain.WebhookDelivery
	for rows.Next() {
		d, err := scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func scanDeliveryRow(row interface{ Scan(...any) error }) (*domain.WebhookDelivery, error) {
	var (
		idStr, endpointIDStr, tenantIDStr string
		eventType, eventID                string
		payloadBytes                      []byte
		status                            string
		attemptCount                      int
		nextRetryAt                       *time.Time
		createdAt, updatedAt              time.Time
	)
	if err := row.Scan(
		&idStr, &endpointIDStr, &tenantIDStr,
		&eventType, &eventID,
		&payloadBytes, &status, &attemptCount, &nextRetryAt,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrDeliveryNotFound
		}
		return nil, fmt.Errorf("delivery repo: scan: %w", err)
	}

	return domain.ReconstituteDelivery(
		domain.DeliveryID(idStr),
		domain.EndpointID(endpointIDStr),
		domain.TenantID(tenantIDStr),
		eventType, eventID,
		json.RawMessage(payloadBytes),
		domain.DeliveryStatus(status),
		attemptCount, nextRetryAt,
		nil, // attempts se cargan por separado
		createdAt.UTC(), updatedAt.UTC(),
	), nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
