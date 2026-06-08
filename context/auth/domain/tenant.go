package domain

import (
	"fmt"
	"time"
)

// TenantStatus representa el estado en el ciclo de vida de un comercio.
type TenantStatus string

const (
	// TenantStatusPending: el comercio se registró pero aún no completó el KYC.
	// Solo puede operar en modo test.
	TenantStatusPending TenantStatus = "pending"

	// TenantStatusActive: el comercio está habilitado para operar.
	// El ambiente (test/production) determina si puede mover dinero real.
	TenantStatusActive TenantStatus = "active"

	// TenantStatusSuspended: el comercio fue suspendido por el operador.
	// No puede realizar ninguna operación.
	TenantStatusSuspended TenantStatus = "suspended"
)

// Tenant es el agregado raíz del contexto Auth.
//
// Representa a un comercio (cliente de la plataforma) y es la raíz de todo
// el aislamiento multi-tenant: cada entidad del sistema pertenece a un Tenant.
//
// Invariantes que garantiza este agregado:
//  1. Un Tenant siempre tiene un nombre legal no vacío.
//  2. Las transiciones de estado siguen la máquina de estados definida.
//  3. Solo un Tenant activo en modo producción puede mover dinero real.
type Tenant struct {
	id          TenantID
	legalName   string
	status      TenantStatus
	environment Environment
	createdAt   time.Time
	updatedAt   time.Time

	events []Event
}

// NewTenant crea un Tenant nuevo en estado Pending y ambiente Test.
// Emite TenantCreatedEvent.
func NewTenant(id TenantID, legalName string) (*Tenant, error) {
	if legalName == "" {
		return nil, ErrEmptyLegalName
	}

	now := time.Now().UTC()
	t := &Tenant{
		id:          id,
		legalName:   legalName,
		status:      TenantStatusPending,
		environment: EnvironmentTest,
		createdAt:   now,
		updatedAt:   now,
	}

	t.record(TenantCreatedEvent{
		baseEvent: newBase(id.String()),
		TenantID:  id.String(),
		LegalName: legalName,
	})

	return t, nil
}

// ReconstituteTenant reconstruye un Tenant desde los datos del repositorio.
// No emite eventos — solo restaura el estado.
func ReconstituteTenant(
	id TenantID,
	legalName string,
	status TenantStatus,
	env Environment,
	createdAt, updatedAt time.Time,
) *Tenant {
	return &Tenant{
		id:          id,
		legalName:   legalName,
		status:      status,
		environment: env,
		createdAt:   createdAt,
		updatedAt:   updatedAt,
	}
}

// Activate activa el comercio con el ambiente dado.
//
// Transición válida: Pending → Active.
// Si ya está Active o Suspended, retorna ErrTenantCannotTransition.
func (t *Tenant) Activate(env Environment) error {
	if t.status != TenantStatusPending {
		return fmt.Errorf("%w: cannot activate from status %q", ErrTenantCannotTransition, t.status)
	}

	t.status = TenantStatusActive
	t.environment = env
	t.updatedAt = time.Now().UTC()

	t.record(TenantActivatedEvent{
		baseEvent:   newBase(t.id.String()),
		TenantID:    t.id.String(),
		Environment: env.String(),
	})

	return nil
}

// Suspend suspende el comercio con el motivo dado.
//
// Transición válida: Active → Suspended.
// Un tenant Pending también puede suspenderse (por rechazo de KYC).
func (t *Tenant) Suspend(reason string) error {
	if t.status == TenantStatusSuspended {
		return fmt.Errorf("%w: tenant is already suspended", ErrTenantCannotTransition)
	}

	t.status = TenantStatusSuspended
	t.updatedAt = time.Now().UTC()

	t.record(TenantSuspendedEvent{
		baseEvent: newBase(t.id.String()),
		TenantID:  t.id.String(),
		Reason:    reason,
	})

	return nil
}

// ── Consultas ─────────────────────────────────────────────────────────────────

// IsActive retorna true si el tenant puede realizar operaciones.
func (t *Tenant) IsActive() bool { return t.status == TenantStatusActive }

// CanProcessRealPayments retorna true si el tenant puede mover dinero real.
// Requiere estar activo Y en modo producción.
func (t *Tenant) CanProcessRealPayments() bool {
	return t.status == TenantStatusActive && t.environment == EnvironmentProduction
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (t *Tenant) ID() TenantID           { return t.id }
func (t *Tenant) LegalName() string      { return t.legalName }
func (t *Tenant) Status() TenantStatus   { return t.status }
func (t *Tenant) Environment() Environment { return t.environment }
func (t *Tenant) CreatedAt() time.Time   { return t.createdAt }
func (t *Tenant) UpdatedAt() time.Time   { return t.updatedAt }

// PullEvents retorna los eventos de dominio pendientes y limpia la lista interna.
//
// El caso de uso debe llamar PullEvents() luego de operar sobre el agregado,
// antes de persistir, para obtener los eventos que se deben publicar vía Outbox.
func (t *Tenant) PullEvents() []Event {
	evs := t.events
	t.events = nil
	return evs
}

func (t *Tenant) record(e Event) {
	t.events = append(t.events, e)
}
