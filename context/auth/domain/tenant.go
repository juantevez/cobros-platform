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

	// TenantStatusSuspended: el comercio fue suspendido por el operador o por
	// cumplimiento. No puede realizar ninguna operación.
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
	// suspendedForCompliance: la suspensión vigente es por cumplimiento y solo
	// la levanta un compliance_officer.
	suspendedForCompliance bool
	createdAt              time.Time
	updatedAt              time.Time

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
	suspendedForCompliance bool,
	createdAt, updatedAt time.Time,
) *Tenant {
	return &Tenant{
		suspendedForCompliance: suspendedForCompliance,
		id:                     id,
		legalName:              legalName,
		status:                 status,
		environment:            env,
		createdAt:              createdAt,
		updatedAt:              updatedAt,
	}
}

// Activate activa el comercio con el ambiente dado.
//
// Transiciones válidas:
//   - Pending → Active(env).
//   - Active(test) → Active(production): el pase a producción, p. ej. cuando
//     se aprueba el KYC de un tenant que un operador ya había activado en test.
//
// Activar en el ambiente que ya tiene es un no-op sin evento (el consumer de
// onboarding puede recibir la aprobación más de una vez). Volver de
// production a test o activar un Suspended (ver Reactivate) retorna
// ErrTenantCannotTransition.
func (t *Tenant) Activate(env Environment) error {
	switch {
	case t.status == TenantStatusActive && t.environment == env:
		return nil
	case t.status == TenantStatusActive && t.environment == EnvironmentTest && env == EnvironmentProduction:
	case t.status != TenantStatusPending:
		return fmt.Errorf("%w: cannot activate %q from status %q (%s)", ErrTenantCannotTransition, env, t.status, t.environment)
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

// SuspendForCompliance suspende por cumplimiento con el motivo genérico
// ComplianceReviewReason. Si ya estaba suspendido (por un operador), no emite
// otro evento pero marca la suspensión como de cumplimiento: así un
// platform_support no puede levantarla.
func (t *Tenant) SuspendForCompliance() {
	if t.status != TenantStatusSuspended {
		_ = t.Suspend(ComplianceReviewReason)
	}
	if !t.suspendedForCompliance {
		t.suspendedForCompliance = true
		t.updatedAt = time.Now().UTC()
	}
}

// Reactivate levanta la suspensión: Suspended → Active, con el ambiente que
// tenía (un tenant suspendido estando pending queda activo en test).
// byCompliance indica si lo pide un compliance_officer: la suspensión por
// cumplimiento solo la levanta cumplimiento, y la del operador solo el
// operador (ErrReactivationNotAllowed).
func (t *Tenant) Reactivate(byCompliance bool) error {
	if t.status != TenantStatusSuspended {
		return fmt.Errorf("%w: cannot reactivate from status %q", ErrTenantCannotTransition, t.status)
	}
	if t.suspendedForCompliance != byCompliance {
		return ErrReactivationNotAllowed
	}

	t.status = TenantStatusActive
	t.suspendedForCompliance = false
	t.updatedAt = time.Now().UTC()

	t.record(TenantReactivatedEvent{
		baseEvent:   newBase(t.id.String()),
		TenantID:    t.id.String(),
		Environment: t.environment.String(),
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

func (t *Tenant) ID() TenantID                 { return t.id }
func (t *Tenant) LegalName() string            { return t.legalName }
func (t *Tenant) Status() TenantStatus         { return t.status }
func (t *Tenant) Environment() Environment     { return t.environment }
func (t *Tenant) SuspendedForCompliance() bool { return t.suspendedForCompliance }
func (t *Tenant) CreatedAt() time.Time         { return t.createdAt }
func (t *Tenant) UpdatedAt() time.Time         { return t.updatedAt }

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
