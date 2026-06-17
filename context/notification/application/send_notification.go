package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

// SendNotificationUseCase envía una notificación de email a un tenant.
//
// Flujo:
//  1. Buscar el template para el event type.
//  2. Verificar preferencias del tenant — si está desactivado, skip.
//  3. Resolver el email destinatario (preferencia o admin del tenant).
//  4. Renderizar el template con los datos del evento.
//  5. Enviar el email.
//  6. Persistir el registro de la notificación (enviada o fallida).
//
// El envío no es crítico: si falla, se loguea y la plataforma continúa.
// No hay reintentos automáticos en Fase 3 (diferente a Webhooks).
type SendNotificationUseCase struct {
	notifRepo    NotificationRepository
	prefRepo     PreferenceRepository
	emailSender  EmailSender
	contactReader UserContactReader
	logger       *slog.Logger
}

func NewSendNotificationUseCase(
	notifRepo NotificationRepository,
	prefRepo PreferenceRepository,
	emailSender EmailSender,
	contactReader UserContactReader,
	logger *slog.Logger,
) *SendNotificationUseCase {
	return &SendNotificationUseCase{
		notifRepo:     notifRepo,
		prefRepo:      prefRepo,
		emailSender:   emailSender,
		contactReader: contactReader,
		logger:        logger,
	}
}

func (uc *SendNotificationUseCase) Execute(ctx context.Context, cmd SendNotificationCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	// 1. Buscar template para este evento.
	tmpl, ok := domain.FindTemplate(cmd.EventType)
	if !ok {
		// Sin template → no hay notificación configurada, skip silencioso.
		return nil
	}

	// 2. Verificar preferencias del tenant.
	recipientEmail, err := uc.resolveRecipient(ctx, tenantID, cmd.EventType)
	if err != nil {
		uc.logger.Warn("notification: skip — no recipient",
			"tenant_id", cmd.TenantID,
			"event_type", cmd.EventType,
			"error", err,
		)
		return nil
	}
	if recipientEmail == "" {
		return nil // silencioso: tenant eligió no recibir notificaciones
	}

	// 3. Renderizar el template.
	subject, body := tmpl.Render(cmd.TemplateData)

	// 4. Crear registro de notificación.
	notif := domain.NewNotification(
		domain.NewNotificationID(),
		tenantID,
		cmd.EventType,
		domain.ChannelEmail,
		recipientEmail,
		subject,
	)

	// 5. Enviar email.
	sendErr := uc.emailSender.Send(ctx, recipientEmail, subject, body)
	if sendErr != nil {
		notif.MarkFailed(sendErr.Error())
		uc.logger.Error("notification: email send failed",
			"tenant_id", cmd.TenantID,
			"event_type", cmd.EventType,
			"to", recipientEmail,
			"error", sendErr,
		)
	} else {
		notif.MarkSent()
		uc.logger.Info("notification: email sent",
			"tenant_id", cmd.TenantID,
			"event_type", cmd.EventType,
			"to", recipientEmail,
		)
	}

	// 6. Persistir el registro (éxito o fallo).
	if err := uc.notifRepo.Save(ctx, notif); err != nil {
		uc.logger.Error("notification: save record failed", "error", err)
		// No propagamos: persistencia del log no es crítica.
	}

	return nil
}

// resolveRecipient determina el email destinatario:
// 1. Preferencia del tenant con recipientEmail explícito.
// 2. Email del admin del tenant.
// 3. Vacío si el tenant desactivó la notificación.
func (uc *SendNotificationUseCase) resolveRecipient(
	ctx context.Context,
	tenantID domain.TenantID,
	eventType string,
) (string, error) {
	pref, err := uc.prefRepo.FindByTenantAndEvent(ctx, tenantID, eventType)
	if err == nil {
		// Preferencia encontrada.
		if !pref.Enabled() {
			return "", nil // desactivado explícitamente
		}
		if pref.RecipientEmail() != "" {
			return pref.RecipientEmail(), nil
		}
	}
	// Sin preferencia (o sin email explícito): usar email del admin.
	email, err := uc.contactReader.GetTenantAdminEmail(ctx, tenantID.String())
	if err != nil {
		return "", fmt.Errorf("get admin email: %w", err)
	}
	return email, nil
}

// ── ListNotifications ─────────────────────────────────────────────────────────

// ListNotificationsUseCase retorna el historial de notificaciones del tenant.
type ListNotificationsUseCase struct {
	notifRepo NotificationRepository
}

func NewListNotificationsUseCase(repo NotificationRepository) *ListNotificationsUseCase {
	return &ListNotificationsUseCase{notifRepo: repo}
}

func (uc *ListNotificationsUseCase) Execute(ctx context.Context, tenantID string, limit int) ([]NotificationView, error) {
	tid, err := domain.ParseTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	notifs, err := uc.notifRepo.ListByTenant(ctx, tid, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}

	views := make([]NotificationView, len(notifs))
	for i, n := range notifs {
		v := NotificationView{
			ID:             n.ID().String(),
			EventType:      n.EventType(),
			Channel:        n.Channel().String(),
			RecipientEmail: n.RecipientEmail(),
			Subject:        n.Subject(),
			Status:         n.Status().String(),
			ErrorMsg:       n.ErrorMsg(),
			CreatedAt:      n.CreatedAt().Format(time.RFC3339),
		}
		if n.SentAt() != nil {
			v.SentAt = n.SentAt().Format(time.RFC3339)
		}
		views[i] = v
	}
	return views, nil
}
