package nats

import (
	"context"
	"fmt"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

func disputeMsg(subject, tenantID, disputeID, outcome string, amount int64) *eventbus.Message {
	return &eventbus.Message{
		Subject: subject,
		Payload: []byte(fmt.Sprintf(
			`{"dispute_id":"%s","payment_id":"%s","tenant_id":"%s","amount":%d,"currency":"ARS","outcome":"%s"}`,
			disputeID, validUUID(), tenantID, amount, outcome)),
	}
}

func TestDispute_Opened(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeDisputeHold, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), disputeMsg("dispute.opened.v1", tenantID.String(), validUUID(), "", 5000)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entryRepo.saved))
	}
}

func TestDispute_ResolvedWon(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeDisputeHold, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), disputeMsg("dispute.resolved.v1", tenantID.String(), validUUID(), "won", 5000)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry (won → merchant_balance), got %d", len(entryRepo.saved))
	}
}

func TestDispute_ResolvedLost(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeDisputeHold, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypePlatformFees, "ARS")

	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	// outcome lost → contrapartida en platform_fees.
	if err := consumer.handle(context.Background(), disputeMsg("dispute.resolved.v1", tenantID.String(), validUUID(), "lost", 5000)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry (lost → platform_fees), got %d", len(entryRepo.saved))
	}
}

func TestDispute_UnknownSubjectIgnored(t *testing.T) {
	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())

	if err := consumer.handle(context.Background(), disputeMsg("dispute.updated.v1", testTenantID(t).String(), validUUID(), "", 100)); err != nil {
		t.Fatalf("unknown subject should be ignored, got %v", err)
	}
	if len(entryRepo.saved) != 0 {
		t.Error("unknown subject must not post entries")
	}
}

func TestDispute_MissingAccount(t *testing.T) {
	postEntry, _, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.handle(context.Background(), disputeMsg("dispute.opened.v1", testTenantID(t).String(), validUUID(), "", 100)); err == nil {
		t.Fatal("expected error when accounts are missing")
	}
}

func TestDispute_ResolvedWon_MissingMerchantAccount(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeDisputeHold, "ARS") // falta merchant_balance
	postEntry, _, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	if err := consumer.handle(context.Background(), disputeMsg("dispute.resolved.v1", tenantID.String(), validUUID(), "won", 100)); err == nil {
		t.Fatal("expected error when merchant_balance is missing on a won dispute")
	}
}

func TestDispute_MalformedPayload(t *testing.T) {
	postEntry, _, _ := newPostEntry()
	consumer := NewDisputeConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.handle(context.Background(), &eventbus.Message{Subject: "dispute.opened.v1", Payload: []byte(`{bad`)}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestDispute_StartConfig(t *testing.T) {
	fc := &fakeConsumer{}
	postEntry, _, _ := newPostEntry()
	consumer := NewDisputeConsumer(fc, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	assertConfig(t, fc.gotCfg, "DISPUTE", "ledger-dispute-consumer", "dispute.>")
}
