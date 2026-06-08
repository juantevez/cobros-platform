package domain

import (
	"time"

	"github.com/google/uuid"
)

// Event es la interfaz que todos los eventos de dominio del contexto Auth implementan.
//
// Los eventos representan hechos del pasado: cosas que ya ocurrieron y son inmutables.
// Se publican en NATS JetStream a través del Outbox para notificar a otros contextos.
//
// Convención de EventType: "<contexto>.<agregado>.<hecho>.<versión>"
// Ejemplo: "auth.tenant.created.v1"
type Event interface {
	// EventID es el UUID único del evento. Se usa como Nats-Msg-Id (deduplicación).
	EventID() string
	// EventType identifica el tipo de evento para routing y deserialización.
	EventType() string
	// EventTenantID retorna el tenant que originó el evento.
	// Vacío para eventos de plataforma (sin tenant asociado).
	EventTenantID() string
	// OccurredAt es el momento en que ocurrió el hecho en el dominio.
	OccurredAt() time.Time
}

// ── Base ──────────────────────────────────────────────────────────────────────

// baseEvent contiene los campos comunes a todos los eventos.
// Los eventos concretos lo embeben.
type baseEvent struct {
	id         string
	tenantID   string
	occurredAt time.Time
}

func newBase(tenantID string) baseEvent {
	return baseEvent{
		id:         uuid.NewString(),
		tenantID:   tenantID,
		occurredAt: time.Now().UTC(),
	}
}

func (e baseEvent) EventID() string       { return e.id }
func (e baseEvent) EventTenantID() string { return e.tenantID }
func (e baseEvent) OccurredAt() time.Time { return e.occurredAt }

// ── Tenant events ─────────────────────────────────────────────────────────────

// TenantCreatedEvent se emite cuando un comercio se registra en la plataforma.
// Inicia el flujo de onboarding (KYC) en la Fase 2.
type TenantCreatedEvent struct {
	baseEvent
	TenantID  string `json:"tenant_id"`
	LegalName string `json:"legal_name"`
}

func (e TenantCreatedEvent) EventType() string { return "auth.tenant.created.v1" }

// TenantActivatedEvent se emite cuando un comercio es activado (post-KYC o manualmente).
// Al recibirlo, otros contextos habilitan las capacidades del tenant.
type TenantActivatedEvent struct {
	baseEvent
	TenantID    string `json:"tenant_id"`
	Environment string `json:"environment"` // "test" | "production"
}

func (e TenantActivatedEvent) EventType() string { return "auth.tenant.activated.v1" }

// TenantSuspendedEvent se emite cuando un comercio es suspendido.
// Los demás contextos deben bloquear operaciones del tenant al recibirlo.
type TenantSuspendedEvent struct {
	baseEvent
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason"`
}

func (e TenantSuspendedEvent) EventType() string { return "auth.tenant.suspended.v1" }

// ── User events ───────────────────────────────────────────────────────────────

// UserRegisteredEvent se emite cuando un usuario se registra en un tenant.
type UserRegisteredEvent struct {
	baseEvent
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
}

func (e UserRegisteredEvent) EventType() string { return "auth.user.registered.v1" }

// UserSuspendedEvent se emite cuando un usuario es suspendido.
type UserSuspendedEvent struct {
	baseEvent
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

func (e UserSuspendedEvent) EventType() string { return "auth.user.suspended.v1" }

// ── ApiKey events ─────────────────────────────────────────────────────────────

// ApiKeyIssuedEvent se emite cuando se genera una nueva API key.
type ApiKeyIssuedEvent struct {
	baseEvent
	TenantID    string `json:"tenant_id"`
	ApiKeyID    string `json:"api_key_id"`
	Prefix      string `json:"prefix"`
	Environment string `json:"environment"`
}

func (e ApiKeyIssuedEvent) EventType() string { return "auth.apikey.issued.v1" }

// ApiKeyRevokedEvent se emite cuando una API key es revocada.
type ApiKeyRevokedEvent struct {
	baseEvent
	TenantID string `json:"tenant_id"`
	ApiKeyID string `json:"api_key_id"`
}

func (e ApiKeyRevokedEvent) EventType() string { return "auth.apikey.revoked.v1" }

// ── Membership events ─────────────────────────────────────────────────────────

// RoleAssignedEvent se emite cuando se asigna o modifica el rol de un usuario.
type RoleAssignedEvent struct {
	baseEvent
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
	Role       string `json:"role"`
	AssignedBy string `json:"assigned_by"` // UserID del actor, o "system"
}

func (e RoleAssignedEvent) EventType() string { return "auth.role.assigned.v1" }

// NewRoleAssignedEvent construye el evento para casos de uso que no operan
// sobre un agregado que lo emita internamente (Membership no es un agregado).
func NewRoleAssignedEvent(tenantID, userID, role, assignedBy string) RoleAssignedEvent {
	return RoleAssignedEvent{
		baseEvent:  newBase(tenantID),
		TenantID:   tenantID,
		UserID:     userID,
		Role:       role,
		AssignedBy: assignedBy,
	}
}
