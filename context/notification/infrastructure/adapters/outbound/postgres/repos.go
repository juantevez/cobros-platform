package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/notification/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsTenantKey es la variable de sesión que las políticas RLS leen para
// aislar filas por tenant (ver pkg/postgres/rls_session.go).
const rlsTenantKey = "app.current_tenant"

// ── NotificationRepository ────────────────────────────────────────────────────

type pgNotificationRepository struct{ pool pkgpostgres.Conn }

func NewNotificationRepository(pool pkgpostgres.Conn) *pgNotificationRepository {
	return &pgNotificationRepository{pool: pool}
}

// withTenant ejecuta fn con app.current_tenant fijado en la sesión (RLS):
// las tablas de este contexto (y users/memberships de Auth, que lee el
// ContactReader) aíslan por tenant con políticas RLS. Fuera de una
// transacción, RunWithAppTenant abre una efímera para el SET LOCAL.
func withTenant(ctx context.Context, pool pkgpostgres.Conn, tenantID string, fn func(context.Context, pkgpostgres.Conn) error) error {
	ctx = pkgpostgres.WithAppTenant(ctx, rlsTenantKey, tenantID)
	return pkgpostgres.RunWithAppTenant(ctx, pool, func(ctx context.Context) error {
		return fn(ctx, pkgpostgres.ConnFromContext(ctx, pool))
	})
}

func (r *pgNotificationRepository) Save(ctx context.Context, n *domain.Notification) error {
	return withTenant(ctx, r.pool, n.TenantID().String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		return r.save(ctx, conn, n)
	})
}

func (r *pgNotificationRepository) save(ctx context.Context, conn pkgpostgres.Conn, n *domain.Notification) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO notifications
			(id, tenant_id, event_type, channel, recipient_email,
			 subject, status, error_msg, sent_at, created_at, nats_msg_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		n.ID().String(), n.TenantID().String(),
		n.EventType(), n.Channel().String(),
		n.RecipientEmail(), n.Subject(),
		n.Status().String(), nullStr(n.ErrorMsg()),
		n.SentAt(), n.CreatedAt(), n.NatsMsgID(),
	)
	if err != nil {
		// La UNIQUE natural (tenant_id, event_type, nats_msg_id) materializa la
		// idempotencia del consumo: una violación NO es un fallo de BD, es la
		// reentrega de un evento ya procesado. El caso de uso la trata como skip.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrNotificationDuplicate
		}
		return fmt.Errorf("notification repo: save: %w", err)
	}
	return nil
}

// ExistsByEventMsg checqueo rápido de dedupe ANTES de enviar el email.
func (r *pgNotificationRepository) ExistsByEventMsg(
	ctx context.Context, tenantID domain.TenantID, eventType, msgID string,
) (bool, error) {
	if msgID == "" {
		return false, nil
	}
	var exists bool
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM notifications
				WHERE tenant_id=$1 AND event_type=$2 AND nats_msg_id=$3
			)`,
			tenantID.String(), eventType, msgID,
		).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("notification repo: exists by msg: %w", err)
	}
	return exists, nil
}

func (r *pgNotificationRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	var notifs []*domain.Notification
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		notifs, err = r.list(ctx, conn, tenantID, limit)
		return err
	})
	return notifs, err
}

func (r *pgNotificationRepository) list(ctx context.Context, conn pkgpostgres.Conn, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	rows, err := conn.Query(ctx, `
		SELECT id, tenant_id, event_type, channel, recipient_email,
		       subject, status, COALESCE(error_msg,''), sent_at, created_at
		FROM notifications
		WHERE tenant_id=$1
		ORDER BY created_at DESC LIMIT $2`,
		tenantID.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("notification repo: list: %w", err)
	}
	defer rows.Close()

	var notifs []*domain.Notification
	for rows.Next() {
		var (
			idStr, tenantIDStr, eventType, channelStr  string
			recipientEmail, subject, statusStr, errMsg string
			sentAt                                     *time.Time
			createdAt                                  time.Time
		)
		if err := rows.Scan(&idStr, &tenantIDStr, &eventType, &channelStr,
			&recipientEmail, &subject, &statusStr, &errMsg, &sentAt, &createdAt); err != nil {
			return nil, fmt.Errorf("notification repo: scan: %w", err)
		}
		ch, err := domain.ParseChannel(channelStr)
		if err != nil {
			return nil, fmt.Errorf("notification repo: row %s: %w", idStr, err)
		}
		status, err := domain.ParseStatus(statusStr)
		if err != nil {
			return nil, fmt.Errorf("notification repo: row %s: %w", idStr, err)
		}
		notifs = append(notifs, domain.ReconstituteNotification(
			domain.NotificationID(idStr), domain.TenantID(tenantIDStr),
			eventType, ch, recipientEmail, subject,
			status, errMsg,
			sentAt, createdAt.UTC(),
		))
	}
	return notifs, rows.Err()
}

// ── PreferenceRepository ──────────────────────────────────────────────────────

type pgPreferenceRepository struct{ pool pkgpostgres.Conn }

func NewPreferenceRepository(pool pkgpostgres.Conn) *pgPreferenceRepository {
	return &pgPreferenceRepository{pool: pool}
}

// Upsert fija app.current_tenant en la sesión (RLS) antes de escribir.
func (r *pgPreferenceRepository) Upsert(ctx context.Context, p *domain.NotificationPreference) error {
	return withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, _ pkgpostgres.Conn) error {
		return r.upsert(ctx, p)
	})
}

func (r *pgPreferenceRepository) upsert(ctx context.Context, p *domain.NotificationPreference) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO notification_preferences
			(tenant_id, event_type, channel, enabled, recipient_email, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id, event_type, channel)
		DO UPDATE SET enabled=$4, recipient_email=$5, updated_at=$6`,
		p.TenantID().String(), p.EventType(), p.Channel().String(),
		p.Enabled(), nullStr(p.RecipientEmail()), p.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("preference repo: upsert: %w", err)
	}
	return nil
}

func (r *pgPreferenceRepository) FindByTenantAndEvent(
	ctx context.Context,
	tenantID domain.TenantID,
	eventType string,
) (*domain.NotificationPreference, error) {
	var (
		channelStr, recipientEmail string
		enabled                    bool
		updatedAt                  time.Time
	)
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT channel, enabled, COALESCE(recipient_email,''), updated_at
			FROM notification_preferences
			WHERE tenant_id=$1 AND event_type=$2 AND channel='email'`,
			tenantID.String(), eventType,
		).Scan(&channelStr, &enabled, &recipientEmail, &updatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPreferenceNotFound
		}
		return nil, fmt.Errorf("preference repo: find: %w", err)
	}
	ch, err := domain.ParseChannel(channelStr)
	if err != nil {
		return nil, fmt.Errorf("preference repo: %w", err)
	}
	return domain.ReconstitutePreference(
		tenantID, eventType, ch, enabled, recipientEmail, updatedAt.UTC(),
	), nil
}

func (r *pgPreferenceRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	var prefs []*domain.NotificationPreference
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn pkgpostgres.Conn) error {
		var err error
		prefs, err = r.list(ctx, conn, tenantID)
		return err
	})
	return prefs, err
}

func (r *pgPreferenceRepository) list(ctx context.Context, conn pkgpostgres.Conn, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	rows, err := conn.Query(ctx, `
		SELECT event_type, channel, enabled, COALESCE(recipient_email,''), updated_at
		FROM notification_preferences
		WHERE tenant_id=$1 ORDER BY event_type`,
		tenantID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("preference repo: list: %w", err)
	}
	defer rows.Close()

	var prefs []*domain.NotificationPreference
	for rows.Next() {
		var eventType, channelStr, recipientEmail string
		var enabled bool
		var updatedAt time.Time
		if err := rows.Scan(&eventType, &channelStr, &enabled, &recipientEmail, &updatedAt); err != nil {
			return nil, fmt.Errorf("preference repo: scan: %w", err)
		}
		ch, err := domain.ParseChannel(channelStr)
		if err != nil {
			return nil, fmt.Errorf("preference repo: %s: %w", eventType, err)
		}
		prefs = append(prefs, domain.ReconstitutePreference(
			tenantID, eventType, ch, enabled, recipientEmail, updatedAt.UTC(),
		))
	}
	return prefs, rows.Err()
}

// ── UserContactReader ─────────────────────────────────────────────────────────

// ContactReader implementa UserContactReader consultando users + memberships del contexto Auth.
type ContactReader struct{ pool pkgpostgres.Conn }

func NewContactReader(pool pkgpostgres.Conn) *ContactReader { return &ContactReader{pool: pool} }

func (r *ContactReader) GetTenantAdminEmail(ctx context.Context, tenantID string) (string, error) {
	var email string
	err := withTenant(ctx, r.pool, tenantID, func(ctx context.Context, conn pkgpostgres.Conn) error {
		return conn.QueryRow(ctx, `
			SELECT u.email
			FROM users u
			JOIN memberships m ON m.user_id = u.id
			WHERE m.tenant_id=$1
			  AND m.role='admin'
			  AND u.status='active'
			ORDER BY m.created_at ASC
			LIMIT 1`,
			tenantID,
		).Scan(&email)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrNoRecipientEmail
		}
		return "", fmt.Errorf("contact reader: %w", err)
	}
	return email, nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
