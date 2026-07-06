package domain

import (
	"testing"
	"time"
)

func TestNewMembership(t *testing.T) {
	uid := NewUserID()
	tid := NewTenantID()
	admin := NewUserID()
	m := NewMembership(uid, tid, RoleOperator, admin)

	if m.UserID() != uid || m.TenantID() != tid {
		t.Error("ids not set correctly")
	}
	if m.Role() != RoleOperator {
		t.Errorf("role = %q, want operator", m.Role())
	}
	if m.AssignedBy() != admin {
		t.Errorf("assignedBy = %q, want %q", m.AssignedBy(), admin)
	}
}

func TestMembershipUpdateRole(t *testing.T) {
	m := NewMembership(NewUserID(), NewTenantID(), RoleReadOnly, UserID(""))
	newAdmin := NewUserID()
	m.UpdateRole(RoleAdmin, newAdmin)

	if m.Role() != RoleAdmin {
		t.Errorf("role = %q, want admin", m.Role())
	}
	if m.AssignedBy() != newAdmin {
		t.Errorf("assignedBy = %q, want %q", m.AssignedBy(), newAdmin)
	}
}

func TestReconstituteMembership(t *testing.T) {
	uid := NewUserID()
	tid := NewTenantID()
	admin := NewUserID()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	m := ReconstituteMembership(uid, tid, RoleAccountant, admin, created, updated)

	if m.UserID() != uid || m.TenantID() != tid || m.Role() != RoleAccountant || m.AssignedBy() != admin {
		t.Error("fields not restored")
	}
	if !m.CreatedAt().Equal(created) || !m.UpdatedAt().Equal(updated) {
		t.Error("timestamps not restored")
	}
}

func TestMembershipHasPermission(t *testing.T) {
	tests := []struct {
		role   Role
		action Action
		want   bool
	}{
		// admin: acceso total
		{RoleAdmin, ActionManageUsers, true},
		{RoleAdmin, ActionManageApiKeys, true},
		{RoleAdmin, ActionManageWebhooks, true},
		// operator: crear/leer pagos, nada de gestión
		{RoleOperator, ActionCreatePayment, true},
		{RoleOperator, ActionReadPayments, true},
		{RoleOperator, ActionManageUsers, false},
		{RoleOperator, ActionReadReports, false},
		// accountant: solo lectura de pagos y reportes
		{RoleAccountant, ActionReadReports, true},
		{RoleAccountant, ActionReadPayments, true},
		{RoleAccountant, ActionCreatePayment, false},
		// read_only: lectura
		{RoleReadOnly, ActionReadPayments, true},
		{RoleReadOnly, ActionCreatePayment, false},
		// platform_support: gestión de usuarios + lecturas, sin crear pagos
		{RolePlatformSupport, ActionManageUsers, true},
		{RolePlatformSupport, ActionReadReports, true},
		{RolePlatformSupport, ActionCreatePayment, false},
		{RolePlatformSupport, ActionManageApiKeys, false},
	}
	for _, tt := range tests {
		m := NewMembership(NewUserID(), NewTenantID(), tt.role, UserID(""))
		if got := m.HasPermission(tt.action); got != tt.want {
			t.Errorf("role %q action %q: got %v, want %v", tt.role, tt.action, got, tt.want)
		}
	}
}

func TestUnknownRoleHasNoPermissions(t *testing.T) {
	m := NewMembership(NewUserID(), NewTenantID(), Role("ghost"), UserID(""))
	if m.HasPermission(ActionReadPayments) {
		t.Error("unknown role should have no permissions")
	}
}
