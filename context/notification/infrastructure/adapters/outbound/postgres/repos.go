package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/notification/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// ── NotificationRepository ────────────────────────────────────────────────────

type pgNotificationRepository struct{ pool *pgxpool.Pool }

func NewNotificationRepository(pool *pgxpool.Pool) *pgNotificationRepository {
	return &pgNotificationRepository{pool: pool}
}

func (r *pgNotificationRepository) Save(ctx context.Context, n *domain.Notification) error {
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO notifications
			(id, tenant_id, event_type, channel, recipient_email,
			 subject, status, error_msg, sent_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		n.ID().String(), n.TenantID().String(),
		n.EventType(), n.Channel().String(),
		n.RecipientEmail(), n.Subject(),
		n.Status().String(), nullStr(n.ErrorMsg()),
		n.SentAt(), n.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("notification repo: save: %w", err)
	}
	return nil
}

func (r *pgNotificationRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	rows, err := r.pool.Query(ctx, `
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
			idStr, tenantIDStr, eventType, channelStr string
			recipientEmail, subject, statusStr, errMsg string
			sentAt                                     *time.Time
			createdAt                                  time.Time
		)
		if err := rows.Scan(&idStr, &tenantIDStr, &eventType, &channelStr,
			&recipientEmail, &subject, &statusStr, &errMsg, &sentAt, &createdAt); err != nil {
			return nil, fmt.Errorf("notification repo: scan: %w", err)
		}
		ch, _ := domain.ParseChannel(channelStr)
		notifs = append(notifs, domain.ReconstituteNotification(
			domain.NotificationID(idStr), domain.TenantID(tenantIDStr),
			eventType, ch, recipientEmail, subject,
			domain.NotificationStatus(statusStr), errMsg,
			sentAt, createdAt.UTC(),
		))
	}
	return notifs, rows.Err()
}

// ── PreferenceRepository ──────────────────────────────────────────────────────

type pgPreferenceRepository struct{ pool *pgxpool.Pool }

func NewPreferenceRepository(pool *pgxpool.Pool) *pgPreferenceRepository {
	return &pgPreferenceRepository{pool: pool}
}

func (r *pgPreferenceRepository) Upsert(ctx context.Context, p *domain.NotificationPreference) error {
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
	conn := pkgpostgres.ConnFromContext(ctx, r.pool)
	var (
		channelStr, recipientEmail string
		enabled                    bool
		updatedAt                  time.Time
	)
	err := conn.QueryRow(ctx, `
		SELECT channel, enabled, COALESCE(recipient_email,''), updated_at
		FROM notification_preferences
		WHERE tenant_id=$1 AND event_type=$2 AND channel='email'`,
		tenantID.String(), eventType,
	).Scan(&channelStr, &enabled, &recipientEmail, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPreferenceNotFound
		}
		return nil, fmt.Errorf("preference repo: find: %w", err)
	}
	ch, _ := domain.ParseChannel(channelStr)
	return domain.ReconstitutePreference(
		tenantID, eventType, ch, enabled, recipientEmail, updatedAt.UTC(),
	), nil
}

func (r *pgPreferenceRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	rows, err := r.pool.Query(ctx, `
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
		ch, _ := domain.ParseChannel(channelStr)
		prefs = append(prefs, domain.ReconstitutePreference(
			tenantID, eventType, ch, enabled, recipientEmail, updatedAt.UTC(),
		))
	}
	return prefs, rows.Err()
}

// ── UserContactReader ─────────────────────────────────────────────────────────

// ContactReader implementa UserContactReader consultando users + memberships del contexto Auth.
type ContactReader struct{ pool *pgxpool.Pool }

func NewContactReader(pool *pgxpool.Pool) *ContactReader { return &ContactReader{pool: pool} }

func (r *ContactReader) GetTenantAdminEmail(ctx context.Context, tenantID string) (string, error) {
	var email string
	err := r.pool.QueryRow(ctx, `
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
