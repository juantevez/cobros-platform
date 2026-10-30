package postgres

import (
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

// Dos asientos con las mismas cuentas en distinto orden bloquean las filas de
// saldo en el mismo orden: sin esto, cada uno podía esperar al otro.
func TestLockOrder_SameOrderRegardlessOfInput(t *testing.T) {
	a := domain.AccountID("11111111-1111-1111-1111-111111111111")
	b := domain.AccountID("22222222-2222-2222-2222-222222222222")
	c := domain.AccountID("33333333-3333-3333-3333-333333333333")
	money := domain.MustMoney(100, "ARS")
	posting := func(acc domain.AccountID, dir domain.Direction) domain.Posting {
		return domain.ReconstitutePosting(domain.NewPostingID(), acc, dir, money)
	}

	first := []domain.Posting{posting(c, domain.DirectionDebit), posting(a, domain.DirectionCredit), posting(b, domain.DirectionCredit)}
	second := []domain.Posting{posting(b, domain.DirectionDebit), posting(c, domain.DirectionCredit), posting(a, domain.DirectionDebit)}

	for _, postings := range [][]domain.Posting{first, second} {
		got := lockOrder(postings)
		want := []domain.AccountID{a, b, c}
		for i, p := range got {
			if p.AccountID() != want[i] {
				t.Fatalf("position %d = %s, want %s", i, p.AccountID(), want[i])
			}
		}
	}
	// No reordena el slice del asiento (los postings se persisten y publican
	// en el orden original).
	if first[0].AccountID() != c {
		t.Error("lockOrder must not mutate its input")
	}
}
