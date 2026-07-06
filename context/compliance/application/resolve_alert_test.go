package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// seedOpenAlert crea una alerta abierta y la carga en el repo fake.
func seedOpenAlert(r *fakeAlertRepo, tenantID domain.TenantID) *domain.Alert {
	a := domain.NewAlert(domain.NewAlertID(), tenantID, domain.AlertSanctionsMatch,
		domain.RiskHigh, "subject", 95, nil, testNow)
	a.PullEvents()
	r.byID[a.ID()] = a
	return a
}

func TestResolveAlertUseCase_Execute(t *testing.T) {
	newUC := func(r *fakeAlertRepo, p *fakePublisher) *ResolveAlertUseCase {
		return NewResolveAlertUseCase(r, fakeTx{}, p, newClock())
	}

	t.Run("resolves open alert and publishes", func(t *testing.T) {
		tid, _ := domain.ParseTenantID(validTenant())
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		p := &fakePublisher{}
		uc := newUC(r, p)

		cmd := ResolveAlertCmd{
			TenantID:    tid.String(),
			AlertID:     a.ID().String(),
			Disposition: "cleared",
			Note:        "falso positivo",
		}
		if err := uc.Execute(context.Background(), cmd); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(r.updated) != 1 {
			t.Fatalf("expected 1 update, got %d", len(r.updated))
		}
		if r.updated[0].Status() != domain.StatusCleared || r.updated[0].Note() != "falso positivo" {
			t.Errorf("alert not resolved as expected: %+v", r.updated[0])
		}
		if len(p.published) != 1 {
			t.Errorf("expected 1 published event, got %d", len(p.published))
		}
	})

	t.Run("invalid tenant rejected", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: "bad", AlertID: uuid.NewString(), Disposition: "cleared"}
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid alert id rejected", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: validTenant(), AlertID: "bad", Disposition: "cleared"}
		if err := uc.Execute(context.Background(), cmd); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid disposition rejected", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: validTenant(), AlertID: uuid.NewString(), Disposition: "maybe"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrInvalidDisposition) {
			t.Fatalf("expected ErrInvalidDisposition, got %v", err)
		}
	})

	t.Run("alert not found", func(t *testing.T) {
		uc := newUC(newAlertRepo(), &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: validTenant(), AlertID: uuid.NewString(), Disposition: "cleared"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound, got %v", err)
		}
	})

	t.Run("cross-tenant access denied", func(t *testing.T) {
		tid, _ := domain.ParseTenantID(validTenant())
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		uc := newUC(r, &fakePublisher{})

		cmd := ResolveAlertCmd{
			TenantID:    validTenant(), // otro tenant
			AlertID:     a.ID().String(),
			Disposition: "cleared",
		}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotFound) {
			t.Fatalf("expected ErrAlertNotFound for cross-tenant, got %v", err)
		}
	})

	t.Run("already resolved returns ErrAlertNotOpen", func(t *testing.T) {
		tid, _ := domain.ParseTenantID(validTenant())
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		_ = a.Resolve(domain.StatusConfirmed, "", testNow)
		a.PullEvents()
		uc := newUC(r, &fakePublisher{})

		cmd := ResolveAlertCmd{TenantID: tid.String(), AlertID: a.ID().String(), Disposition: "cleared"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, domain.ErrAlertNotOpen) {
			t.Fatalf("expected ErrAlertNotOpen, got %v", err)
		}
	})

	t.Run("find error propagated", func(t *testing.T) {
		r := newAlertRepo()
		r.findErr = errBoom
		uc := newUC(r, &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: validTenant(), AlertID: uuid.NewString(), Disposition: "cleared"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	t.Run("update error propagated", func(t *testing.T) {
		tid, _ := domain.ParseTenantID(validTenant())
		r := newAlertRepo()
		a := seedOpenAlert(r, tid)
		r.updateErr = errBoom
		uc := newUC(r, &fakePublisher{})
		cmd := ResolveAlertCmd{TenantID: tid.String(), AlertID: a.ID().String(), Disposition: "confirmed"}
		if err := uc.Execute(context.Background(), cmd); !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
