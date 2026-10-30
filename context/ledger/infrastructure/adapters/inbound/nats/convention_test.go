package nats

import (
	"context"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// Estos tests fijan la convención de signo entre los consumers y Apply
// (crédito suma, débito resta): merchant_balance positivo es lo que la
// plataforma le debe al comercio, y Payout exige que sea > 0.

// ledgerFixture arma los tres consumers sobre las 7 cuentas de un tenant.
type ledgerFixture struct {
	t        *testing.T
	tenantID domain.TenantID
	accounts map[domain.AccountType]*domain.Account
	entries  *fakeEntryRepo
	payment  *PaymentConsumer
	payout   *PayoutConsumer
	dispute  *DisputeConsumer
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	f := &ledgerFixture{t: t, tenantID: testTenantID(t), accounts: map[domain.AccountType]*domain.Account{}}
	accountRepo := newFakeAccountRepo()
	for _, at := range []domain.AccountType{
		domain.AccountTypeMerchantBalance, domain.AccountTypePlatformFees, domain.AccountTypeReserve,
		domain.AccountTypeInTransit, domain.AccountTypeDisputeHold,
		domain.AccountTypePayoutTransit, domain.AccountTypePayoutSent,
	} {
		f.accounts[at] = accountRepo.seed(t, f.tenantID, at, "ARS")
	}
	postEntry, entries, _ := newPostEntry(accountRepo)
	f.entries = entries
	f.payment = NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
	f.payout = NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
	f.dispute = NewDisputeConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
	return f
}

func (f *ledgerFixture) handle(h func(context.Context, *eventbus.Message) error, msg *eventbus.Message) {
	f.t.Helper()
	if err := h(context.Background(), msg); err != nil {
		f.t.Fatalf("%s: %v", msg.Subject, err)
	}
}

// assertBalances aplica todos los asientos como Apply y compara los saldos.
// Las cuentas no listadas deben quedar en 0.
func (f *ledgerFixture) assertBalances(step string, want map[domain.AccountType]int64) {
	f.t.Helper()
	got := map[domain.AccountID]int64{}
	for _, e := range f.entries.saved {
		for _, p := range e.Postings() {
			delta := p.Money().Amount()
			if p.IsDebit() {
				delta = -delta
			}
			got[p.AccountID()] += delta
		}
	}
	for at, acc := range f.accounts {
		if got[acc.ID()] != want[at] {
			f.t.Errorf("%s: %s = %d, want %d", step, at, got[acc.ID()], want[at])
		}
	}
}

func TestConvention_PaymentDisputeLostAndPayout(t *testing.T) {
	f := newLedgerFixture(t)
	tid := f.tenantID.String()

	// amount 10000, platform_fee 300, psp_fee 200: el PSP liquida 9800.
	f.handle(f.payment.handle, paymentMsg(tid, validUUID(), 10000, 300, 200))
	f.assertBalances("captured", map[domain.AccountType]int64{
		domain.AccountTypeMerchantBalance: 9500,
		domain.AccountTypePlatformFees:    300,
		domain.AccountTypeInTransit:       -9800,
	})

	disputeID := validUUID()
	f.handle(f.dispute.handle, disputeMsg("dispute.opened.v1", tid, disputeID, "", 1000))
	f.assertBalances("dispute opened", map[domain.AccountType]int64{
		domain.AccountTypeMerchantBalance: 8500,
		domain.AccountTypePlatformFees:    300,
		domain.AccountTypeInTransit:       -9800,
		domain.AccountTypeDisputeHold:     1000,
	})

	// Perdida: el contracargo sale de la liquidación del PSP, no de las comisiones.
	f.handle(f.dispute.handle, disputeMsg("dispute.resolved.v1", tid, disputeID, "lost", 1000))
	f.assertBalances("dispute lost", map[domain.AccountType]int64{
		domain.AccountTypeMerchantBalance: 8500,
		domain.AccountTypePlatformFees:    300,
		domain.AccountTypeInTransit:       -8800,
	})

	payoutID := validUUID()
	f.handle(f.payout.handle, payoutMsg("payout.initiated.v1", tid, payoutID, 8500))
	f.assertBalances("payout initiated", map[domain.AccountType]int64{
		domain.AccountTypePlatformFees:  300,
		domain.AccountTypeInTransit:     -8800,
		domain.AccountTypePayoutTransit: 8500,
	})

	f.handle(f.payout.handle, payoutMsg("payout.confirmed.v1", tid, payoutID, 8500))
	f.assertBalances("payout confirmed", map[domain.AccountType]int64{
		domain.AccountTypePlatformFees: 300,
		domain.AccountTypeInTransit:    -8800,
		domain.AccountTypePayoutSent:   8500,
	})
}

func TestConvention_DisputeWonAndPayoutFailedRestoreMerchantBalance(t *testing.T) {
	f := newLedgerFixture(t)
	tid := f.tenantID.String()

	f.handle(f.payment.handle, paymentMsg(tid, validUUID(), 5000, 0, 0))

	disputeID := validUUID()
	f.handle(f.dispute.handle, disputeMsg("dispute.opened.v1", tid, disputeID, "", 2000))
	f.handle(f.dispute.handle, disputeMsg("dispute.resolved.v1", tid, disputeID, "won", 2000))

	payoutID := validUUID()
	f.handle(f.payout.handle, payoutMsg("payout.initiated.v1", tid, payoutID, 5000))
	f.handle(f.payout.handle, payoutMsg("payout.failed.v1", tid, payoutID, 5000))

	f.assertBalances("won + payout failed", map[domain.AccountType]int64{
		domain.AccountTypeMerchantBalance: 5000,
		domain.AccountTypeInTransit:       -5000,
	})
}
