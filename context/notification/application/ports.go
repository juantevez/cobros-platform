package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

// NotificationRepository persiste el log de notificaciones enviadas.
type NotificationRepository interface {
	// Save inserta el registro. Retorna ErrNotificationDuplicate si ya
	// existe una fila para la misma clave natural (tenant, event, msg).
	Save(ctx context.Context, n *domain.Notification) error
	ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error)

	// ExistsByEventMsg verifica si ya se procesó (y registró) el mensaje
	// NATS identified por msgID para (tenant, eventType). Es la checación
	// rápida de dedupe ANTES de enviar el email: evita dobles envíos en
	// reentregas del mismo evento.
	ExistsByEventMsg(ctx context.Context, tenantID domain.TenantID, eventType, msgID string) (bool, error)
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

// Clock abstrae el tiempo (tests deterministas).
type Clock interface {
	Now() time.Time
}
