package domain

import "time"

// NotificationPreference configura qué notificaciones recibe un tenant
// y a qué dirección de email se envían.
//
// Si no existe una preferencia para un event type, el comportamiento
// por defecto es HABILITADO para todos los eventos con template registrado.
type NotificationPreference struct {
	tenantID       TenantID
	eventType      string  // event type normalizado, ej: "payment.captured"
	channel        Channel
	enabled        bool
	recipientEmail string  // email destino; vacío = usar email del admin del tenant
	updatedAt      time.Time
}

// NewNotificationPreference crea una preferencia.
func NewNotificationPreference(
	tenantID TenantID,
	eventType string,
	channel Channel,
	enabled bool,
	recipientEmail string,
) *NotificationPreference {
	return &NotificationPreference{
		tenantID:       tenantID,
		eventType:      eventType,
		channel:        channel,
		enabled:        enabled,
		recipientEmail: recipientEmail,
		updatedAt:      time.Now().UTC(),
	}
}

// ReconstitutePreference reconstruye desde el repositorio.
func ReconstitutePreference(
	tenantID TenantID, eventType string,
	channel Channel, enabled bool,
	recipientEmail string, updatedAt time.Time,
) *NotificationPreference {
	return &NotificationPreference{
		tenantID: tenantID, eventType: eventType,
		channel: channel, enabled: enabled,
		recipientEmail: recipientEmail, updatedAt: updatedAt,
	}
}

func (p *NotificationPreference) TenantID() TenantID       { return p.tenantID }
func (p *NotificationPreference) EventType() string        { return p.eventType }
func (p *NotificationPreference) Channel() Channel         { return p.channel }
func (p *NotificationPreference) Enabled() bool            { return p.enabled }
func (p *NotificationPreference) RecipientEmail() string   { return p.recipientEmail }
func (p *NotificationPreference) UpdatedAt() time.Time     { return p.updatedAt }
