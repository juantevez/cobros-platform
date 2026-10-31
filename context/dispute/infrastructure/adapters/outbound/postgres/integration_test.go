package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/dispute/application"
	"github.com/juantevez/cobros-platform/context/dispute/domain"
)

// Dos transiciones concurrentes desde la misma versión (contestar y aceptar,
// resolver dos veces, aceptar justo cuando el poller la vence) ganaban las dos
// y publicaban dos dispute.resolved.v1. Ahora gana una sola.
func TestDisputeRepository_Update_OptimisticLock(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewDisputeRepository(pool)
	seeded := seedDispute(t)

	// Todas las fotos se leen antes de escribir: misma versión de partida.
	const n = 8
	snapshots := make([]*domain.Dispute, n)
	for i := range snapshots {
		d, err := repo.FindByID(ctx, seeded.ID())
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if err := d.Accept("acepto", time.Now()); err != nil {
			t.Fatalf("accept: %v", err)
		}
		snapshots[i] = d
	}
	results := make(chan error, n)
	var wg sync.WaitGroup
	for _, d := range snapshots {
		wg.Add(1)
		go func(d *domain.Dispute) { defer wg.Done(); results <- repo.Update(ctx, d) }(d)
	}
	wg.Wait()
	close(results)

	won, locked := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, domain.ErrOptimisticLock):
			locked++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || locked != n-1 {
		t.Fatalf("won = %d locked = %d, want 1 and %d", won, locked, n-1)
	}

	got, err := repo.FindByID(ctx, seeded.ID())
	if err != nil || got.Status() != domain.StatusAccepted || got.Version() != 2 {
		t.Fatalf("persisted = %v (version %d), %v; want accepted v2", got.Status(), got.Version(), err)
	}

	// Con la versión recargada, la siguiente transición válida pasa; una foto
	// vieja (v1) ya no puede escribir.
	stale := domain.ReconstituteDispute(got.ID(), got.TenantID(), got.PaymentID(), got.PSPReference(), got.Amount(), got.Currency(),
		got.Reason(), domain.StatusOpen, nil, "", "", got.Deadline(), got.OpenedAt(), nil, nil, 1)
	_ = stale.Expire(time.Now())
	if err := repo.Update(ctx, stale); !errors.Is(err, domain.ErrOptimisticLock) {
		t.Errorf("stale update = %v, want ErrOptimisticLock", err)
	}
}

// Contra la constraint real: la segunda disputa del mismo pago devuelve el
// centinela de duplicado (el caso de uso responde 409).
func TestDisputeRepository_Save_DuplicatePaymentAgainstConstraint(t *testing.T) {
	first := seedDispute(t)
	now := time.Now().UTC()
	ref := domain.PaymentRef{ID: first.PaymentID(), TenantID: first.TenantID().String(), Status: "captured", Amount: 5000, Currency: "ARS"}
	second, err := domain.NewDispute(domain.NewDisputeID(), ref, "psp", 5000, "ARS", domain.ReasonGeneral, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("new dispute: %v", err)
	}
	if err := NewDisputeRepository(requireDB(t)).Save(context.Background(), second); !errors.Is(err, application.ErrDuplicatePaymentKey) {
		t.Fatalf("second save = %v, want ErrDuplicatePaymentKey", err)
	}
}
