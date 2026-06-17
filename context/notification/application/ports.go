package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

// NotificationRepository persiste el log de notificaciones enviadas.
type NotificationRepository interface {
	Save(ctx context.Context, n *domain.Notification) error
	ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error)
}

// PreferenceRepository persiste las preferencias de notificación por tenant.
type PreferenceRepository interface {
	Upsert(ctx context.Context, p *domain.NotificationPreference) error
	FindByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) (*domain.NotificationPreference, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error)
}

// EmailSender envía emails. Implementaciones: SMTPSender (producción), LogSender (dev).
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// UserContactReader obtiene el email del administrador del tenant.
// Consulta directamente la tabla users + memberships (misma BD).
type UserContactReader interface {
	GetTenantAdminEmail(ctx context.Context, tenantID string) (string, error)
}
