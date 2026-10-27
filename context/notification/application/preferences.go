package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

// UpdatePreferenceUseCase actualiza o crea una preferencia de notificación.
type UpdatePreferenceUseCase struct {
	prefRepo PreferenceRepository
}

func NewUpdatePreferenceUseCase(repo PreferenceRepository) *UpdatePreferenceUseCase {
	return &UpdatePreferenceUseCase{prefRepo: repo}
}

func (uc *UpdatePreferenceUseCase) Execute(ctx context.Context, cmd UpdatePreferenceCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}

	// Normalizar al event type canónico del catálogo: la preferencia SIEMPRE
	// se guarda bajo la clave sin versión ("payment.captured.v1" →
	// "payment.captured"). Así la clave que acepta el PUT es exactamente la
	// que muestra el GET y la que busca el consumer al resolver el
	// destinatario.
	eventType := domain.NormalizeEventType(cmd.EventType)

	// Validar que el event type tiene template registrado.
	if _, ok := domain.FindTemplate(eventType); !ok {
		return fmt.Errorf("no template registered for event type %q", cmd.EventType)
	}

	pref := domain.NewNotificationPreference(
		tenantID,
		eventType,
		domain.ChannelEmail,
		cmd.Enabled,
		cmd.RecipientEmail,
	)

	return uc.prefRepo.Upsert(ctx, pref)
}

// GetPreferencesUseCase retorna las preferencias del tenant con defaults.
type GetPreferencesUseCase struct {
	prefRepo PreferenceRepository
}

func NewGetPreferencesUseCase(repo PreferenceRepository) *GetPreferencesUseCase {
	return &GetPreferencesUseCase{prefRepo: repo}
}

// Execute retorna todas las preferencias del tenant.
// Para event types sin preferencia guardada, retorna el default (enabled=true).
func (uc *GetPreferencesUseCase) Execute(ctx context.Context, q GetPreferencesQuery) ([]PreferenceView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, err
	}

	// Cargar preferencias guardadas.
	saved, err := uc.prefRepo.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list preferences: %w", err)
	}

	// Indexar por event type NORMALIZADO: tolera filas legacy escritas con
	// sufijo de versión ("payment.captured.v1") y las muestra bajo la misma
	// clave canónica que acepta el PUT.
	savedIndex := make(map[string]*domain.NotificationPreference)
	for _, p := range saved {
		key := domain.NormalizeEventType(p.EventType())
		if prev, ok := savedIndex[key]; !ok || prev.UpdatedAt().Before(p.UpdatedAt()) {
			savedIndex[key] = p
		}
	}

	// Construir la vista completa: para cada event type con template,
	// mostrar la preferencia guardada o el default.
	allEventTypes := domain.EventTypesToTemplates()
	views := make([]PreferenceView, 0, len(allEventTypes))
	for _, et := range allEventTypes {
		if pref, ok := savedIndex[et]; ok {
			views = append(views, PreferenceView{
				// Mostrar SIEMPRE la clave canónica (normalizada), que es la
				// misma que el cliente debe usar en el PUT.
				EventType:      et,
				Channel:        pref.Channel().String(),
				Enabled:        pref.Enabled(),
				RecipientEmail: pref.RecipientEmail(),
			})
		} else {
			// Default: habilitado, sin email override.
			views = append(views, PreferenceView{
				EventType: et,
				Channel:   domain.ChannelEmail.String(),
				Enabled:   true,
			})
		}
	}
	return views, nil
}
