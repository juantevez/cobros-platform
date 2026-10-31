package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

// Una respuesta de 501 bytes terminada en "ñ" se cortaba en 500 partiendo el
// carácter: Postgres rechazaba el INSERT del intento (SQLSTATE 22021) y la
// delivery quedaba delivered sin intento registrado.
func TestDeliveryRepository_Update_PersistsAttemptWithMultibyteBody(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewDeliveryRepository(pool)
	d := seedDelivery(t, seedEndpoint(t))

	body := strings.Repeat("a", 499) + "ñ" + "\x00"
	attempt := domain.NewDeliveryAttempt(1, 200, body, "", 12)
	if err := d.RecordAttempt(attempt, attempt.AttemptedAt()); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if err := repo.Update(ctx, d, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.FindByID(ctx, d.ID())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Status() != domain.StatusDelivered || len(got.Attempts()) != 1 {
		t.Fatalf("status = %q attempts = %d, want delivered with 1 attempt", got.Status(), len(got.Attempts()))
	}
	if got.Attempts()[0].ResponseBody() != strings.Repeat("a", 499) {
		t.Errorf("stored body = %q", got.Attempts()[0].ResponseBody())
	}
}

// Poller, reintento manual y réplicas del worker: con la misma foto de la
// delivery, solo uno la reclama; el reclamo vencido se puede retomar.
func TestDeliveryRepository_Claim_OnlyOneWins(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewDeliveryRepository(pool)
	d := seedDelivery(t, seedEndpoint(t))
	now := time.Now().UTC()

	results := make(chan bool, 8)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.Claim(ctx, d.ID(), 0, now, now.Add(2*time.Minute))
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			results <- ok
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for ok := range results {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("claims won = %d, want 1", won)
	}

	// Mientras dura el reclamo no se vuelve a reclamar ni aparece en el poller.
	if ok, _ := repo.Claim(ctx, d.ID(), 0, now.Add(time.Minute), now.Add(3*time.Minute)); ok {
		t.Error("claim while leased should fail")
	}
	due, err := repo.ListDueForRetry(ctx, now.Add(time.Minute), 1000)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	for _, x := range due {
		if x.ID() == d.ID() {
			t.Error("claimed delivery must not be listed as due")
		}
	}

	// Reclamo vencido (el proceso murió a mitad del envío): se retoma.
	if ok, _ := repo.Claim(ctx, d.ID(), 0, now.Add(3*time.Minute), now.Add(5*time.Minute)); !ok {
		t.Error("expired claim should be claimable again")
	}
	// Con otro attempt_count (alguien ya registró un intento) no se reclama.
	if ok, _ := repo.Claim(ctx, d.ID(), 1, now.Add(10*time.Minute), now.Add(12*time.Minute)); ok {
		t.Error("claim with stale attempt_count should fail")
	}
}

// Update libera el reclamo y no pisa un resultado ya registrado por otro.
func TestDeliveryRepository_Update_ReleasesClaimAndRejectsStale(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	repo := NewDeliveryRepository(pool)
	d := seedDelivery(t, seedEndpoint(t))
	now := time.Now().UTC()

	if ok, err := repo.Claim(ctx, d.ID(), 0, now, now.Add(2*time.Minute)); !ok || err != nil {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	a := domain.NewDeliveryAttempt(1, 500, "boom", "", 3)
	_ = d.RecordAttempt(a, a.AttemptedAt())
	if err := repo.Update(ctx, d, 0); err != nil {
		t.Fatalf("update: %v", err)
	}
	var claimed *time.Time
	if err := pool.QueryRow(ctx, `SELECT claimed_until FROM webhook_deliveries WHERE id=$1`, d.ID().String()).Scan(&claimed); err != nil || claimed != nil {
		t.Errorf("claimed_until = %v (%v), want NULL", claimed, err)
	}

	// Un segundo despachador con la foto vieja (attempt_count=0) no pisa.
	stale, _ := repo.FindByID(ctx, d.ID())
	stale = domain.ReconstituteDelivery(stale.ID(), stale.EndpointID(), stale.TenantID(), stale.EventType(), stale.EventID(),
		stale.Payload(), domain.StatusPending, 0, stale.NextRetryAt(), nil, stale.CreatedAt(), stale.UpdatedAt())
	b := domain.NewDeliveryAttempt(1, 200, "ok", "", 3)
	_ = stale.RecordAttempt(b, b.AttemptedAt())
	if err := repo.Update(ctx, stale, 0); !errors.Is(err, domain.ErrDeliveryInProgress) {
		t.Fatalf("stale update = %v, want ErrDeliveryInProgress", err)
	}
	got, _ := repo.FindByID(ctx, d.ID())
	if got.Status() != domain.StatusFailed || got.AttemptCount() != 1 || len(got.Attempts()) != 1 {
		t.Errorf("status = %q attempts = %d/%d, want failed with 1", got.Status(), got.AttemptCount(), len(got.Attempts()))
	}
}
