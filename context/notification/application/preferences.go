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

	// Validar que el event type tiene template registrado.
	if _, ok := domain.FindTemplate(cmd.EventType); !ok {
		return fmt.Errorf("no template registered for event type %q", cmd.EventType)
	}

	pref := domain.NewNotificationPreference(
		tenantID,
		cmd.EventType,
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

	savedIndex := make(map[string]*domain.NotificationPreference)
	for _, p := range saved {
		savedIndex[p.EventType()] = p
	}

	// Construir la vista completa: para cada event type con template,
	// mostrar la preferencia guardada o el default.
	allEventTypes := domain.EventTypesToTemplates()
	views := make([]PreferenceView, 0, len(allEventTypes))
	for _, et := range allEventTypes {
		if pref, ok := savedIndex[et]; ok {
			views = append(views, PreferenceView{
				EventType:      pref.EventType(),
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
