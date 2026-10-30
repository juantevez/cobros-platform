package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

// ── Errores de aplicación (semántica HTTP en el handler) ─────────────────────
var (
	// ErrInvalidPreferenceRequest: entrada inválida del cliente → 400.
	ErrInvalidPreferenceRequest = errors.New("invalid preference request")
	// ErrEventTypeUnknown: el event type no tiene template registrado → 404.
	ErrEventTypeUnknown = errors.New("event type not found")
	// ErrPreferenceStore: fallo de infraestructura/BD → 500 (sin detalles).
	ErrPreferenceStore = errors.New("preference storage failure")
)

// UpdatePreferenceUseCase actualiza o crea una preferencia de notificación.
type UpdatePreferenceUseCase struct {
	prefRepo PreferenceRepository
	clock    Clock
}

func NewUpdatePreferenceUseCase(repo PreferenceRepository, clock Clock) *UpdatePreferenceUseCase {
	return &UpdatePreferenceUseCase{prefRepo: repo, clock: clock}
}

// Execute valida, canonicaliza y hace merge de la preferencia antes del upsert.
//
// Reglas:
//   - El event type se normaliza al tipo canónico del catálogo ("payment.
//     captured.v1" → "payment.captured"): la clave que guarda el PUT es la
//     misma que muestra el GET y busca el consumer.
//   - recipient_email se valida (formato RFC 5322 + longitud) y se
//     canonicaliza (trim + lowercase) SIEMPRE que venga presente.
//   - PUT parcial (recipient_email omitido) → merge: se conserva el override
//     existente; "" explícito → limpiar el override (volver al admin).
func (uc *UpdatePreferenceUseCase) Execute(ctx context.Context, cmd UpdatePreferenceCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPreferenceRequest, err)
	}

	eventType := domain.NormalizeEventType(cmd.EventType)
	if _, ok := domain.FindTemplate(eventType); !ok {
		return fmt.Errorf("%w: %q", ErrEventTypeUnknown, cmd.EventType)
	}

	// Validación + canonicalización del email (bug 5).
	var newEmail string
	if cmd.RecipientEmail != nil {
		newEmail, err = domain.ValidateRecipientEmail(*cmd.RecipientEmail)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPreferenceRequest, err)
		}
	}

	existing, err := uc.prefRepo.FindByTenantAndEvent(ctx, tenantID, eventType)
	switch {
	case err == nil:
		// Merge (bug 6): campo omitido ⇒ mantener el override previo.
		if cmd.RecipientEmail == nil {
			newEmail = existing.RecipientEmail()
		}
	case errors.Is(err, domain.ErrPreferenceNotFound):
		// Sin fila previa: arranca vacío (o con el valor del body).
	default:
		return fmt.Errorf("%w: %v", ErrPreferenceStore, err)
	}

	pref := domain.NewNotificationPreference(
		tenantID,
		eventType,
		domain.ChannelEmail,
		cmd.Enabled,
		newEmail,
		uc.clock.Now(),
	)

	// RLS (bug 9): el adapter fija app.current_tenant en la sesión antes del upsert.
	if err := uc.prefRepo.Upsert(ctx, pref); err != nil {
		return fmt.Errorf("%w: %v", ErrPreferenceStore, err)
	}
	return nil
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
