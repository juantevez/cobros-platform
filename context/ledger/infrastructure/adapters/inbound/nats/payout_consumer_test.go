package nats

import (
	"context"
	"fmt"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

func payoutMsg(subject, tenantID, payoutID string, amount int64) *eventbus.Message {
	return &eventbus.Message{
		Subject: subject,
		Payload: []byte(fmt.Sprintf(
			`{"payout_id":"%s","tenant_id":"%s","amount":%d,"currency":"ARS"}`,
			payoutID, tenantID, amount)),
	}
}

func TestPayout_Initiated(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypePayoutTransit, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), payoutMsg("payout.initiated.v1", tenantID.String(), validUUID(), 9700)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entryRepo.saved))
	}
}

func TestPayout_Confirmed(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypePayoutTransit, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypePayoutSent, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), payoutMsg("payout.confirmed.v1", tenantID.String(), validUUID(), 9700)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entryRepo.saved))
	}
}

func TestPayout_Failed(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypePayoutTransit, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), payoutMsg("payout.failed.v1", tenantID.String(), validUUID(), 9700)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected reversal entry, got %d", len(entryRepo.saved))
	}
}

func TestPayout_UnknownSubjectIgnored(t *testing.T) {
	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())

	if err := consumer.handle(context.Background(), payoutMsg("payout.something.v1", testTenantID(t).String(), validUUID(), 100)); err != nil {
		t.Fatalf("unknown subject should be ignored, got %v", err)
	}
	if len(entryRepo.saved) != 0 {
		t.Error("unknown subject must not post entries")
	}
}

func TestPayout_MissingAccount(t *testing.T) {
	// initiated sin cuentas sembradas → error al buscar merchant_balance.
	postEntry, _, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.handle(context.Background(), payoutMsg("payout.initiated.v1", testTenantID(t).String(), validUUID(), 100)); err == nil {
		t.Fatal("expected error when accounts are missing")
	}
}

func TestPayout_SecondAccountMissing(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("confirmed without payout_sent", func(t *testing.T) {
		accountRepo := newFakeAccountRepo()
		accountRepo.seed(t, tenantID, domain.AccountTypePayoutTransit, "ARS") // falta payout_sent
		postEntry, _, _ := newPostEntry()
		consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
		if err := consumer.handle(context.Background(), payoutMsg("payout.confirmed.v1", tenantID.String(), validUUID(), 100)); err == nil {
			t.Fatal("expected error when payout_sent is missing")
		}
	})

	t.Run("failed without merchant_balance", func(t *testing.T) {
		accountRepo := newFakeAccountRepo()
		accountRepo.seed(t, tenantID, domain.AccountTypePayoutTransit, "ARS") // falta merchant_balance
		postEntry, _, _ := newPostEntry()
		consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
		if err := consumer.handle(context.Background(), payoutMsg("payout.failed.v1", tenantID.String(), validUUID(), 100)); err == nil {
			t.Fatal("expected error when merchant_balance is missing")
		}
	})
}

func TestPayout_MalformedPayload(t *testing.T) {
	postEntry, _, _ := newPostEntry()
	consumer := NewPayoutConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.handle(context.Background(), &eventbus.Message{Subject: "payout.initiated.v1", Payload: []byte(`{bad`)}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestPayout_StartConfig(t *testing.T) {
	fc := &fakeConsumer{}
	postEntry, _, _ := newPostEntry()
	consumer := NewPayoutConsumer(fc, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	assertConfig(t, fc.gotCfg, "PAYOUT", "ledger-payout-consumer", "payout.>")
}
