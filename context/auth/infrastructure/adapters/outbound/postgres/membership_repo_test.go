package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestMembershipRepo_SaveAndFind(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "member@example.com")
	admin := seedUser(t, pool, tenant.ID(), "admin@example.com")

	m := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, admin.ID())
	if err := repo.Save(ctx, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByUserAndTenant(ctx, user.ID(), tenant.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.UserID() != user.ID() || got.TenantID() != tenant.ID() {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Role() != domain.RoleOperator {
		t.Errorf("role = %q, want operator", got.Role())
	}
	if got.AssignedBy() != admin.ID() {
		t.Errorf("assignedBy = %q, want %q", got.AssignedBy(), admin.ID())
	}
	timesClose(t, got.CreatedAt(), m.CreatedAt())
}

func TestMembershipRepo_SaveWithoutAssignedBy(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "firstadmin@example.com")

	// Primer admin: assigned_by vacío → se persiste como NULL.
	m := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleAdmin, domain.UserID(""))
	if err := repo.Save(ctx, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByUserAndTenant(ctx, user.ID(), tenant.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !got.AssignedBy().IsZero() {
		t.Errorf("assignedBy should be zero (NULL), got %q", got.AssignedBy())
	}
}

func TestMembershipRepo_SaveIdempotent(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "dup@example.com")

	first := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleOperator, domain.UserID(""))
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// ON CONFLICT (user_id, tenant_id) DO NOTHING: no error y no sobrescribe.
	second := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleAdmin, domain.UserID(""))
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("second save should be a no-op, got %v", err)
	}

	got, _ := repo.FindByUserAndTenant(ctx, user.ID(), tenant.ID())
	if got.Role() != domain.RoleOperator {
		t.Errorf("role = %q, want operator (Save must not overwrite)", got.Role())
	}
}

func TestMembershipRepo_FindNotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)

	_, err := repo.FindByUserAndTenant(context.Background(), domain.NewUserID(), domain.NewTenantID())
	if !errors.Is(err, domain.ErrMembershipNotFound) {
		t.Fatalf("expected ErrMembershipNotFound, got %v", err)
	}
}

func TestMembershipRepo_Update(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "target@example.com")
	admin1 := seedUser(t, pool, tenant.ID(), "admin1@example.com")
	admin2 := seedUser(t, pool, tenant.ID(), "admin2@example.com")

	m := domain.NewMembership(user.ID(), tenant.ID(), domain.RoleReadOnly, admin1.ID())
	if err := repo.Save(ctx, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	m.UpdateRole(domain.RoleAccountant, admin2.ID())
	if err := repo.Update(ctx, m); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := repo.FindByUserAndTenant(ctx, user.ID(), tenant.ID())
	if got.Role() != domain.RoleAccountant {
		t.Errorf("role = %q, want accountant", got.Role())
	}
	if got.AssignedBy() != admin2.ID() {
		t.Errorf("assignedBy = %q, want %q", got.AssignedBy(), admin2.ID())
	}
}

func TestMembershipRepo_Update_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)

	m := domain.NewMembership(domain.NewUserID(), domain.NewTenantID(), domain.RoleOperator, domain.UserID(""))
	if err := repo.Update(context.Background(), m); !errors.Is(err, domain.ErrMembershipNotFound) {
		t.Fatalf("expected ErrMembershipNotFound, got %v", err)
	}
}

func TestMembershipRepo_ListByTenant(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	u1 := seedUser(t, pool, tenant.ID(), "u1@example.com")
	u2 := seedUser(t, pool, tenant.ID(), "u2@example.com")
	_ = repo.Save(ctx, domain.NewMembership(u1.ID(), tenant.ID(), domain.RoleOperator, domain.UserID("")))
	_ = repo.Save(ctx, domain.NewMembership(u2.ID(), tenant.ID(), domain.RoleAccountant, domain.UserID("")))

	list, err := repo.ListByTenant(ctx, tenant.ID())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 memberships, got %d", len(list))
	}
	// Todas pertenecen al tenant consultado.
	for _, m := range list {
		if m.TenantID() != tenant.ID() {
			t.Errorf("membership from wrong tenant: %s", m.TenantID())
		}
	}
}

func TestMembershipRepo_ListByTenant_Empty(t *testing.T) {
	pool := requireDB(t)
	repo := NewMembershipRepository(pool)

	list, err := repo.ListByTenant(context.Background(), domain.NewTenantID())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}
