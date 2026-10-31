package postgres

import (
	"context"
	"strings"
	"testing"

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
	if err := repo.Update(ctx, d); err != nil {
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
