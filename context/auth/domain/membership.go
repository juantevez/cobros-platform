package domain

import "time"

// Membership representa el vínculo entre un User y un Tenant con un rol asignado.
//
// No es un agregado independiente: vive dentro del contexto del User o del Tenant
// según la operación. Se persiste como tabla separada pero se consulta siempre
// en relación a uno de sus dos extremos.
//
// Un mismo User puede tener Memberships en múltiples Tenants, con roles distintos.
type Membership struct {
	userID     UserID
	tenantID   TenantID
	role       Role
	assignedBy UserID    // quién asignó el rol (puede ser vacío en el primer admin)
	createdAt  time.Time
	updatedAt  time.Time
}

// NewMembership crea un nuevo vínculo user-tenant-role.
// assignedBy puede ser el UserID del admin que asignó, o vacío si es el primer admin.
func NewMembership(userID UserID, tenantID TenantID, role Role, assignedBy UserID) Membership {
	now := time.Now().UTC()
	return Membership{
		userID:     userID,
		tenantID:   tenantID,
		role:       role,
		assignedBy: assignedBy,
		createdAt:  now,
		updatedAt:  now,
	}
}

// ReconstituteMembership reconstruye una Membership desde el repositorio.
func ReconstituteMembership(
	userID UserID,
	tenantID TenantID,
	role Role,
	assignedBy UserID,
	createdAt, updatedAt time.Time,
) Membership {
	return Membership{
		userID:     userID,
		tenantID:   tenantID,
		role:       role,
		assignedBy: assignedBy,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}
}

// UpdateRole cambia el rol asignado al usuario en el tenant.
func (m *Membership) UpdateRole(newRole Role, assignedBy UserID) {
	m.role = newRole
	m.assignedBy = assignedBy
	m.updatedAt = time.Now().UTC()
}

// HasPermission es un helper que permite verificar si el rol tiene
// capacidades sobre un recurso dado. En Fase 1 usa lógica simple por rol;
// en Fase 2 puede evolucionar a permisos granulares.
func (m *Membership) HasPermission(action Action) bool {
	return rolePermissions[m.role].contains(action)
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (m *Membership) UserID() UserID     { return m.userID }
func (m *Membership) TenantID() TenantID { return m.tenantID }
func (m *Membership) Role() Role         { return m.role }
func (m *Membership) AssignedBy() UserID { return m.assignedBy }
func (m *Membership) CreatedAt() time.Time { return m.createdAt }
func (m *Membership) UpdatedAt() time.Time { return m.updatedAt }

// ── Permisos ─────────────────────────────────────────────────────────────────

// Action representa una operación sobre un recurso del sistema.
type Action string

const (
	ActionManageUsers    Action = "users:manage"
	ActionManageApiKeys  Action = "apikeys:manage"
	ActionCreatePayment  Action = "payments:create"
	ActionReadPayments   Action = "payments:read"
	ActionReadReports    Action = "reports:read"
	ActionManageWebhooks Action = "webhooks:manage"
)

type permissionSet []Action

func (ps permissionSet) contains(a Action) bool {
	for _, p := range ps {
		if p == a {
			return true
		}
	}
	return false
}

// rolePermissions define qué acciones puede realizar cada rol.
// Centralizado aquí para que sea la única fuente de verdad de autorización.
var rolePermissions = map[Role]permissionSet{
	RoleAdmin: {
		ActionManageUsers,
		ActionManageApiKeys,
		ActionCreatePayment,
		ActionReadPayments,
		ActionReadReports,
		ActionManageWebhooks,
	},
	RoleOperator: {
		ActionCreatePayment,
		ActionReadPayments,
	},
	RoleAccountant: {
		ActionReadPayments,
		ActionReadReports,
	},
	RoleReadOnly: {
		ActionReadPayments,
		ActionReadReports,
	},
	RolePlatformSupport: {
		ActionManageUsers,
		ActionReadPayments,
		ActionReadReports,
	},
}
