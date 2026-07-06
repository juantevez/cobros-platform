package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestTenantRepo_SaveAndFindByID(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantRepository(pool)
	ctx := context.Background()

	tenant, _ := domain.NewTenant(domain.NewTenantID(), "Acme Round Trip")
	tenant.PullEvents()
	cleanupTenant(t, pool, tenant.ID())

	if err := repo.Save(ctx, tenant); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.FindByID(ctx, tenant.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID() != tenant.ID() || got.LegalName() != "Acme Round Trip" {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Status() != domain.TenantStatusPending || !got.Environment().IsTest() {
		t.Errorf("initial state mismatch: status=%s env=%s", got.Status(), got.Environment())
	}
	timesClose(t, got.CreatedAt(), tenant.CreatedAt())
	timesClose(t, got.UpdatedAt(), tenant.UpdatedAt())
}

func TestTenantRepo_FindByID_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantRepository(pool)

	_, err := repo.FindByID(context.Background(), domain.NewTenantID())
	if !errors.Is(err, domain.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestTenantRepo_Update(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantRepository(pool)
	ctx := context.Background()

	tenant := seedTenant(t, pool)
	if err := tenant.Activate(domain.EnvironmentProduction); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if err := repo.Update(ctx, tenant); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.FindByID(ctx, tenant.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Status() != domain.TenantStatusActive || !got.Environment().IsProd() {
		t.Errorf("update not persisted: status=%s env=%s", got.Status(), got.Environment())
	}
}

func TestTenantRepo_Update_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewTenantRepository(pool)

	tenant, _ := domain.NewTenant(domain.NewTenantID(), "Ghost")
	tenant.PullEvents()
	// No se guardó → Update debe devolver ErrTenantNotFound.
	if err := repo.Update(context.Background(), tenant); !errors.Is(err, domain.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}
