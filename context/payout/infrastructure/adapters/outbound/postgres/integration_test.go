package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// savedPayout persiste un payout initiated de un tenant nuevo y registra su
// limpieza.
func savedPayout(t *testing.T) *domain.Payout {
	t.Helper()
	pool := requireDB(t)
	money, _ := domain.NewMoney(5000, "ARS")
	p, err := domain.NewPayout(domain.NewPayoutID(), domain.TenantID(uuid.NewString()), money, testBankAccount(), uuid.NewString())
	if err != nil {
		t.Fatalf("new payout: %v", err)
	}
	if err := NewPayoutRepository(pool).Save(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payouts WHERE id=$1`, p.ID().String()) })
	return p
}

// El request original y el recuperador pueden resolver el mismo payout en
// processing: el Update condicional deja escribir a uno solo.
func TestPayoutRepository_ConcurrentResolution(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPayoutRepository(pool)
	p := savedPayout(t)
	_ = p.MarkProcessing()
	if err := repo.Update(ctx, p, domain.StatusInitiated); err != nil {
		t.Fatalf("mark processing: %v", err)
	}

	a, _ := repo.FindByID(ctx, p.ID())
	b, _ := repo.FindByID(ctx, p.ID())
	_ = a.Confirm("BANK-1")
	_ = b.Fail("the bank has no record of the transfer", "")
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, x := range []*domain.Payout{a, b} {
		wg.Add(1)
		go func(x *domain.Payout) { defer wg.Done(); results <- repo.Update(ctx, x, domain.StatusProcessing) }(x)
	}
	wg.Wait()
	close(results)
	won, changed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, domain.ErrPayoutChanged):
			changed++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || changed != 1 {
		t.Fatalf("won = %d changed = %d, want 1 and 1", won, changed)
	}
}

// Solo lista los payouts en processing o unknown sin cambios desde antes del
// corte.
func TestPayoutRepository_ListUnresolved_Integration(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPayoutRepository(pool)

	stuck, unknown, fresh, initiated := savedPayout(t), savedPayout(t), savedPayout(t), savedPayout(t)
	for _, p := range []*domain.Payout{stuck, unknown, fresh} {
		_ = p.MarkProcessing()
		if err := repo.Update(ctx, p, domain.StatusInitiated); err != nil {
			t.Fatalf("mark processing: %v", err)
		}
	}
	_ = unknown.MarkUnknown("timeout")
	if err := repo.Update(ctx, unknown, domain.StatusProcessing); err != nil {
		t.Fatalf("mark unknown: %v", err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	for _, p := range []*domain.Payout{stuck, unknown} {
		if _, err := pool.Exec(ctx, `UPDATE payouts SET updated_at=$2 WHERE id=$1`, p.ID().String(), old); err != nil {
			t.Fatalf("age payout: %v", err)
		}
	}

	got, err := repo.ListUnresolved(ctx, time.Now().UTC().Add(-2*time.Minute), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[domain.PayoutID]bool{}
	for _, p := range got {
		seen[p.ID()] = true
	}
	if !seen[stuck.ID()] || !seen[unknown.ID()] || seen[fresh.ID()] || seen[initiated.ID()] {
		t.Errorf("stuck=%v unknown=%v fresh=%v initiated=%v, want only stuck and unknown",
			seen[stuck.ID()], seen[unknown.ID()], seen[fresh.ID()], seen[initiated.ID()])
	}
}

// El detalle crudo del banco se persiste aparte del motivo público.
func TestPayoutRepository_BankFailureDetail(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewPayoutRepository(pool)
	p := savedPayout(t)
	_ = p.MarkProcessing()
	_ = repo.Update(ctx, p, domain.StatusInitiated)
	_ = p.Fail(domain.FailureRejectedByBank, "CBU 0170 closed by the bank")
	if err := repo.Update(ctx, p, domain.StatusProcessing); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repo.FindByID(ctx, p.ID())
	if got.FailureReason() != domain.FailureRejectedByBank || got.BankFailureDetail() != "CBU 0170 closed by the bank" {
		t.Errorf("reason = %q detail = %q", got.FailureReason(), got.BankFailureDetail())
	}
}
