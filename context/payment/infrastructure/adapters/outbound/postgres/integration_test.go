package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// savedPayment persiste un pago initiated de un tenant nuevo y registra su
// limpieza.
func savedPayment(t *testing.T) *domain.Payment {
	t.Helper()
	pool := requireDB(t)
	amount, _ := domain.NewMoney(10_000, "ARS")
	p, err := domain.NewPayment(domain.NewPaymentID(), domain.TenantID(uuid.NewString()), uuid.NewString(), amount,
		domain.PayerInfo{Name: "Ana"}, domain.MethodCard, nil)
	if err != nil {
		t.Fatalf("new payment: %v", err)
	}
	if err := NewPaymentRepository(pool).Save(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, p.ID().String()) })
	return p
}

// El request original y el recuperador pueden resolver el mismo pago en
// processing: el Update condicional deja escribir a uno solo, y el claim
// persiste la comisión con la que el recuperador captura.
func TestPaymentRepository_ProcessingResolution(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPaymentRepository(pool)
	p := savedPayment(t)

	if err := p.MarkProcessing(domain.ReconstituteMoney(300, "ARS")); err != nil {
		t.Fatalf("mark processing: %v", err)
	}
	if ok, err := repo.ClaimProcessing(ctx, p); !ok || err != nil {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	claimed, _ := repo.FindByID(ctx, p.ID())
	if claimed.Status() != domain.StatusProcessing || claimed.PlatformFee().Amount() != 300 {
		t.Fatalf("after claim: status %q fee %d, want processing with fee 300", claimed.Status(), claimed.PlatformFee().Amount())
	}

	// Dos resoluciones concurrentes desde la misma foto: captura y fallo.
	a, _ := repo.FindByID(ctx, p.ID())
	b, _ := repo.FindByID(ctx, p.ID())
	_ = a.Capture("mock", "PSP-1", a.PlatformFee(), domain.ReconstituteMoney(150, "ARS"))
	_ = b.Fail("mock", "no record")
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, x := range []*domain.Payment{a, b} {
		wg.Add(1)
		go func(x *domain.Payment) { defer wg.Done(); results <- repo.Update(ctx, x, domain.StatusProcessing) }(x)
	}
	wg.Wait()
	close(results)
	won, changed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, domain.ErrPaymentChanged):
			changed++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || changed != 1 {
		t.Fatalf("won = %d changed = %d, want 1 and 1", won, changed)
	}
	final, _ := repo.FindByID(ctx, p.ID())
	if final.Status() != domain.StatusCaptured && final.Status() != domain.StatusFailed {
		t.Errorf("final status = %q", final.Status())
	}
}

// Solo lista los pagos en processing sin cambios desde antes del corte.
func TestPaymentRepository_ListStuckProcessing_Integration(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPaymentRepository(pool)

	stuck, fresh, initiated := savedPayment(t), savedPayment(t), savedPayment(t)
	for _, p := range []*domain.Payment{stuck, fresh} {
		_ = p.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))
		if ok, err := repo.ClaimProcessing(ctx, p); !ok || err != nil {
			t.Fatalf("claim: %v %v", ok, err)
		}
	}
	old := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE payments SET updated_at=$2 WHERE id=$1`, stuck.ID().String(), old); err != nil {
		t.Fatalf("age payment: %v", err)
	}

	got, err := repo.ListStuckProcessing(ctx, time.Now().UTC().Add(-2*time.Minute), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[domain.PaymentID]bool{}
	for _, p := range got {
		seen[p.ID()] = true
	}
	if !seen[stuck.ID()] || seen[fresh.ID()] || seen[initiated.ID()] {
		t.Errorf("stuck=%v fresh=%v initiated=%v, want only the stuck one", seen[stuck.ID()], seen[fresh.ID()], seen[initiated.ID()])
	}
}

// Contra la constraint real: una segunda fila con la misma clave del mismo
// tenant devuelve ErrPaymentAlreadyExists.
func TestPaymentRepository_Save_DuplicateKeyAgainstConstraint(t *testing.T) {
	first := savedPayment(t)
	amount, _ := domain.NewMoney(10_000, "ARS")
	second, _ := domain.NewPayment(domain.NewPaymentID(), first.TenantID(), first.IdempotencyKey(), amount,
		domain.PayerInfo{}, domain.MethodCard, nil)
	if err := NewPaymentRepository(requireDB(t)).Save(context.Background(), second); !errors.Is(err, domain.ErrPaymentAlreadyExists) {
		t.Fatalf("second save = %v, want ErrPaymentAlreadyExists", err)
	}
}

// Dos reembolsos concurrentes: el Update condicional a captured deja escribir
// a uno solo, y la referencia del reembolso queda persistida.
func TestPaymentRepository_ConcurrentRefund(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPaymentRepository(pool)
	p := savedPayment(t)
	_ = p.MarkProcessing(domain.ReconstituteMoney(300, "ARS"))
	_, _ = repo.ClaimProcessing(ctx, p)
	_ = p.Capture("mock", "PSP-1", p.PlatformFee(), domain.ReconstituteMoney(150, "ARS"))
	if err := repo.Update(ctx, p, domain.StatusProcessing); err != nil {
		t.Fatalf("capture: %v", err)
	}

	a, _ := repo.FindByID(ctx, p.ID())
	b, _ := repo.FindByID(ctx, p.ID())
	_ = a.Refund("REF-A")
	_ = b.Refund("REF-B")
	errA, errB := repo.Update(ctx, a, domain.StatusCaptured), repo.Update(ctx, b, domain.StatusCaptured)
	if errA != nil || !errors.Is(errB, domain.ErrPaymentChanged) {
		t.Fatalf("errA = %v errB = %v, want nil and ErrPaymentChanged", errA, errB)
	}
	got, _ := repo.FindByID(ctx, p.ID())
	if got.Status() != domain.StatusRefunded || got.RefundPSPReference() != "REF-A" || got.RefundedAt() == nil {
		t.Errorf("status = %q ref = %q at = %v", got.Status(), got.RefundPSPReference(), got.RefundedAt())
	}
}
