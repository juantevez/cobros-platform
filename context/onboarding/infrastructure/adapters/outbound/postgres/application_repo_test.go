package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

func TestApplicationRepo_SaveAndFind(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)

	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}

	t.Run("find by id", func(t *testing.T) {
		got, err := repo.FindByID(ctx, app.ID())
		if err != nil {
			t.Fatalf("find by id: %v", err)
		}
		if got.ID() != app.ID() || got.Status() != domain.StatusPending {
			t.Errorf("identity/status mismatch: %+v", got)
		}
		info := got.BusinessInfo()
		if info.LegalName != "Acme Integration" || info.TaxID.String() != "20304050607" {
			t.Errorf("business info mismatch: %+v", info)
		}
		if info.BusinessCategory != domain.CategoryRetail || info.Address.Country != "AR" {
			t.Errorf("category/address mismatch: %+v", info)
		}
		timesClose(t, got.CreatedAt(), app.CreatedAt())
	})

	t.Run("find by tenant", func(t *testing.T) {
		got, err := repo.FindByTenantID(ctx, tenantID)
		if err != nil {
			t.Fatalf("find by tenant: %v", err)
		}
		if got.ID() != app.ID() {
			t.Errorf("wrong application: %s", got.ID())
		}
	})
}

func TestApplicationRepo_NotFound(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	if _, err := repo.FindByID(ctx, domain.NewApplicationID()); !errors.Is(err, domain.ErrApplicationNotFound) {
		t.Fatalf("find by id: expected ErrApplicationNotFound, got %v", err)
	}
	if _, err := repo.FindByTenantID(ctx, testTenantID(t)); !errors.Is(err, domain.ErrApplicationNotFound) {
		t.Fatalf("find by tenant: expected ErrApplicationNotFound, got %v", err)
	}
}

func TestApplicationRepo_UpdateWithCollections(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Agregar documento, persona y cuenta bancaria, luego Update.
	addCompleteness(t, app)
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.FindByID(ctx, app.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got.Documents()) != 1 {
		t.Errorf("documents = %d, want 1", len(got.Documents()))
	}
	if len(got.Persons()) != 1 {
		t.Errorf("persons = %d, want 1", len(got.Persons()))
	}
	if got.BankAccount() == nil {
		t.Fatal("bank account not persisted")
	}
	if got.BankAccount().AccountType() != domain.BankAccountCBU || got.BankAccount().Currency() != "ARS" {
		t.Errorf("bank account mismatch: %+v", got.BankAccount())
	}
}

func TestApplicationRepo_UpdateStatusTransition(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Completar, guardar colecciones, enviar a revisión y persistir el estado.
	addCompleteness(t, app)
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update collections: %v", err)
	}
	if err := app.SubmitForReview(); err != nil {
		t.Fatalf("submit for review: %v", err)
	}
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, _ := repo.FindByID(ctx, app.ID())
	if got.Status() != domain.StatusInReview {
		t.Errorf("status = %q, want in_review", got.Status())
	}
	if got.SubmittedAt() == nil {
		t.Error("submitted_at not persisted")
	}
}

func TestApplicationRepo_PersistsReviewNotes(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}
	addCompleteness(t, app)
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update collections: %v", err)
	}
	// pending → in_review → approved (con notas de revisión).
	if err := app.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := app.Approve("todo verificado"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update approve: %v", err)
	}

	got, _ := repo.FindByID(ctx, app.ID())
	if got.Status() != domain.StatusApproved {
		t.Errorf("status = %q, want approved", got.Status())
	}
	if got.ReviewNotes() != "todo verificado" {
		t.Errorf("review notes = %q, want 'todo verificado'", got.ReviewNotes())
	}
	if got.ReviewedAt() == nil {
		t.Error("reviewed_at not persisted")
	}
}

func TestApplicationRepo_BankAccountUpsert(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Primera cuenta (ARS).
	_ = app.SetBankAccount(domain.NewBankAccount(domain.NewBankAccountID(), domain.BankAccountCBU, "111", "B1", "H1", "ARS"))
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update 1: %v", err)
	}

	// Reemplazar por otra (USD) → ON CONFLICT (application_id) DO UPDATE.
	_ = app.SetBankAccount(domain.NewBankAccount(domain.NewBankAccountID(), domain.BankAccountIBAN, "222", "B2", "H2", "USD"))
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	got, _ := repo.FindByID(ctx, app.ID())
	if got.BankAccount() == nil || got.BankAccount().Currency() != "USD" {
		t.Errorf("bank account not upserted to USD: %+v", got.BankAccount())
	}
}

func TestApplicationRepo_DocumentsAppendOnly(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	app := newPendingApp(t, tenantID)
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}

	_ = app.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypeIDCard, "ref-1"))
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	// Segundo Update con el mismo documento (ON CONFLICT DO NOTHING) + uno nuevo.
	_ = app.AddDocument(domain.NewBusinessDocument(domain.NewDocumentID(), domain.DocTypePassport, "ref-2"))
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	got, _ := repo.FindByID(ctx, app.ID())
	if len(got.Documents()) != 2 {
		t.Errorf("documents = %d, want 2 (no duplicates from re-upsert)", len(got.Documents()))
	}
}

func TestApplicationRepo_DuplicateTenant(t *testing.T) {
	pool := requireDB(t)
	repo := NewApplicationRepository(pool)
	ctx := context.Background()

	tenantID := testTenantID(t)
	cleanupTenant(t, pool, tenantID)
	if err := repo.Save(ctx, newPendingApp(t, tenantID)); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// UNIQUE(tenant_id): una sola solicitud por tenant.
	if err := repo.Save(ctx, newPendingApp(t, tenantID)); err == nil {
		t.Fatal("expected unique-violation error for a second application in the same tenant")
	}
}
