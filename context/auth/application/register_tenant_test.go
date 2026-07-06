package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

func TestRegisterTenant_Success(t *testing.T) {
	repo := newFakeTenantRepo()
	pub := &fakePublisher{}
	uc := NewRegisterTenantUseCase(repo, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), RegisterTenantCmd{LegalName: "Acme SA"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TenantID == "" {
		t.Fatal("expected a tenant id")
	}
	// El tenant persistido nace pending + test.
	id, _ := domain.ParseTenantID(res.TenantID)
	saved := repo.tenants[id]
	if saved == nil {
		t.Fatal("tenant was not saved")
	}
	if saved.Status() != domain.TenantStatusPending || !saved.Environment().IsTest() {
		t.Errorf("unexpected initial state: status=%s env=%s", saved.Status(), saved.Environment())
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.TenantCreatedEvent); !ok {
		t.Fatalf("expected TenantCreatedEvent, got %T", pub.published[0])
	}
}

func TestRegisterTenant_EmptyLegalName(t *testing.T) {
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), fakeTx{}, &fakePublisher{})
	_, err := uc.Execute(context.Background(), RegisterTenantCmd{LegalName: ""})
	if !errors.Is(err, domain.ErrEmptyLegalName) {
		t.Fatalf("expected ErrEmptyLegalName, got %v", err)
	}
}

func TestRegisterTenant_SaveErrorPropagates(t *testing.T) {
	repo := newFakeTenantRepo()
	repo.saveErr = errBoom
	uc := NewRegisterTenantUseCase(repo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), RegisterTenantCmd{LegalName: "Acme"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestRegisterTenant_PublisherErrorPropagates(t *testing.T) {
	pub := &fakePublisher{err: errBoom}
	uc := NewRegisterTenantUseCase(newFakeTenantRepo(), fakeTx{}, pub)

	_, err := uc.Execute(context.Background(), RegisterTenantCmd{LegalName: "Acme"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
