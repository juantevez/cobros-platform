package domain

import "time"

// Notification registra una notificación enviada o intentada.
// Es un log de lo que se envió a cada destinatario.
type Notification struct {
	id             NotificationID
	tenantID       TenantID
	eventType      string
	channel        Channel
	recipientEmail string
	subject        string
	status         NotificationStatus
	errorMsg       string
	sentAt         *time.Time
	createdAt      time.Time
}

// NewNotification crea una notificación en estado pending.
func NewNotification(
	id NotificationID,
	tenantID TenantID,
	eventType string,
	channel Channel,
	recipientEmail, subject string,
) *Notification {
	return &Notification{
		id:             id,
		tenantID:       tenantID,
		eventType:      eventType,
		channel:        channel,
		recipientEmail: recipientEmail,
		subject:        subject,
		status:         StatusPending,
		createdAt:      time.Now().UTC(),
	}
}

// ReconstituteNotification reconstruye desde el repositorio.
func ReconstituteNotification(
	id NotificationID, tenantID TenantID,
	eventType string, channel Channel,
	recipientEmail, subject string,
	status NotificationStatus, errorMsg string,
	sentAt *time.Time, createdAt time.Time,
) *Notification {
	return &Notification{
		id: id, tenantID: tenantID,
		eventType: eventType, channel: channel,
		recipientEmail: recipientEmail, subject: subject,
		status: status, errorMsg: errorMsg,
		sentAt: sentAt, createdAt: createdAt,
	}
}

// MarkSent registra el envío exitoso.
func (n *Notification) MarkSent() {
	now := time.Now().UTC()
	n.status = StatusSent
	n.sentAt = &now
}

// MarkFailed registra el fallo de envío.
func (n *Notification) MarkFailed(reason string) {
	n.status = StatusFailed
	n.errorMsg = reason
}

func (n *Notification) ID() NotificationID          { return n.id }
func (n *Notification) TenantID() TenantID          { return n.tenantID }
func (n *Notification) EventType() string           { return n.eventType }
func (n *Notification) Channel() Channel            { return n.channel }
func (n *Notification) RecipientEmail() string      { return n.recipientEmail }
func (n *Notification) Subject() string             { return n.subject }
func (n *Notification) Status() NotificationStatus  { return n.status }
func (n *Notification) ErrorMsg() string            { return n.errorMsg }
func (n *Notification) SentAt() *time.Time          { return n.sentAt }
func (n *Notification) CreatedAt() time.Time        { return n.createdAt }
