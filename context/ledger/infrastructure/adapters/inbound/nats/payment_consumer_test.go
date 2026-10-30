package nats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

func paymentMsg(tenantID, paymentID string, amount, platformFee, pspFee int64) *eventbus.Message {
	return &eventbus.Message{
		Subject: "payment.captured.v1",
		Payload: []byte(fmt.Sprintf(
			`{"payment_id":"%s","tenant_id":"%s","amount":%d,"currency":"ARS","platform_fee":%d,"psp_fee":%d}`,
			paymentID, tenantID, amount, platformFee, pspFee)),
	}
}

func TestPayment_PostsBalancedEntry(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypePlatformFees, "ARS")

	postEntry, entryRepo, pub := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	// amount 10000, platform_fee 300, psp_fee 0 → net 9700; debits 9700+300 = 10000 = credit.
	if err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 10000, 300, 0)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry posted, got %d", len(entryRepo.saved))
	}
	if len(entryRepo.saved[0].Postings()) != 3 {
		t.Errorf("expected 3 postings, got %d", len(entryRepo.saved[0].Postings()))
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.EntryPostedEvent); !ok {
		t.Fatalf("expected EntryPostedEvent, got %T", pub.published[0])
	}
}

func TestPayment_NoPlatformFeeTwoPostings(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	// platform_fee 0, psp_fee 0 → net = amount → 2 postings balanceados.
	if err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 5000, 0, 0)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 || len(entryRepo.saved[0].Postings()) != 2 {
		t.Fatalf("expected a 2-posting entry, got %+v", entryRepo.saved)
	}
}

func TestPayment_NonPositiveNetAmount(t *testing.T) {
	postEntry, _, _ := newPostEntry()
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())

	// amount 100, platform_fee 100 → net 0 → error, sin buscar cuentas.
	if err := consumer.handle(context.Background(), paymentMsg(testTenantID(t).String(), validUUID(), 100, 100, 0)); err == nil {
		t.Fatal("expected error for non-positive net amount")
	}
}

func TestPayment_MissingAccounts(t *testing.T) {
	tenantID := testTenantID(t)

	t.Run("in_transit missing", func(t *testing.T) {
		postEntry, _, _ := newPostEntry()
		consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
		if err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 1000, 0, 0)); err == nil {
			t.Fatal("expected error when in_transit account is missing")
		}
	})

	t.Run("merchant_balance missing", func(t *testing.T) {
		accountRepo := newFakeAccountRepo()
		accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
		postEntry, _, _ := newPostEntry(accountRepo)
		consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())
		if err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 1000, 0, 0)); err == nil {
			t.Fatal("expected error when merchant_balance account is missing")
		}
	})
}

func TestPayment_MalformedPayload(t *testing.T) {
	postEntry, _, _ := newPostEntry()
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	for _, subject := range []string{"payment.captured.v1", "payment.refunded.v1"} {
		if err := consumer.handle(context.Background(), &eventbus.Message{Subject: subject, Payload: []byte(`{bad`)}); err == nil {
			t.Errorf("%s: expected unmarshal error", subject)
		}
	}
}

func TestPayment_StartConfig(t *testing.T) {
	fc := &fakeConsumer{}
	postEntry, _, _ := newPostEntry()
	consumer := NewPaymentConsumer(fc, postEntry, newFakeAccountRepo(), discardLogger())
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	assertConfig(t, fc.gotCfg, "PAYMENT", "ledger-payment-consumer", "payment.>")
}

// Con comisión del PSP el asiento balancea sobre lo que el PSP liquida
// (amount - psp_fee): antes acreditaba amount y no balanceaba.
func TestPayment_PSPFeeBalancesOnSettledAmount(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	inTransit := accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
	merchant := accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	fees := accountRepo.seed(t, tenantID, domain.AccountTypePlatformFees, "ARS")

	postEntry, entryRepo, _ := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	// amount 10000, platform_fee 300, psp_fee 200 → PSP liquida 9800 = 9500 + 300.
	if err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 10000, 300, 200)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry posted, got %d", len(entryRepo.saved))
	}
	got := postingAmounts(entryRepo.saved[0])
	want := map[domain.AccountID]int64{inTransit.ID(): 9800, merchant.ID(): 9500, fees.ID(): 300}
	for id, amount := range want {
		if got[id] != amount {
			t.Errorf("account %s: amount = %d, want %d", id, got[id], amount)
		}
	}
}

// Sin la cuenta platform_fees el asiento no balancea: antes se omitía la línea
// sin aviso y el asiento fallaba más adelante.
func TestPayment_MissingPlatformFeesAccountFails(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	err := consumer.handle(context.Background(), paymentMsg(tenantID.String(), validUUID(), 10000, 300, 0))
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
	if len(entryRepo.saved) != 0 {
		t.Errorf("expected no entry posted, got %d", len(entryRepo.saved))
	}
}

func refundMsg(tenantID, paymentID string, amount int64) *eventbus.Message {
	return &eventbus.Message{
		Subject: "payment.refunded.v1",
		Payload: []byte(fmt.Sprintf(
			`{"payment_id":"%s","tenant_id":"%s","refund_amount":%d,"currency":"ARS"}`,
			paymentID, tenantID, amount)),
	}
}

func TestPayment_RefundPostsEntry(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	inTransit := accountRepo.seed(t, tenantID, domain.AccountTypeInTransit, "ARS")
	merchant := accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	paymentID := validUUID()
	if err := consumer.handle(context.Background(), refundMsg(tenantID.String(), paymentID, 4000)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 1 {
		t.Fatalf("expected 1 entry posted, got %d", len(entryRepo.saved))
	}
	e := entryRepo.saved[0]
	if e.IdempotencyKey() != "payment_refunded_"+paymentID {
		t.Errorf("idempotency key = %q", e.IdempotencyKey())
	}
	for _, p := range e.Postings() {
		switch p.AccountID() {
		case merchant.ID():
			if !p.IsDebit() || p.Money().Amount() != 4000 {
				t.Errorf("merchant_balance posting = %s %d, want debit 4000", p.Direction(), p.Money().Amount())
			}
		case inTransit.ID():
			if p.IsDebit() || p.Money().Amount() != 4000 {
				t.Errorf("in_transit posting = %s %d, want credit 4000", p.Direction(), p.Money().Amount())
			}
		default:
			t.Errorf("unexpected account %s", p.AccountID())
		}
	}
}

func TestPayment_RefundMissingAccountFails(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")

	postEntry, entryRepo, _ := newPostEntry(accountRepo)
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, accountRepo, discardLogger())

	err := consumer.handle(context.Background(), refundMsg(tenantID.String(), validUUID(), 4000))
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got %v", err)
	}
	if len(entryRepo.saved) != 0 {
		t.Errorf("expected no entry posted, got %d", len(entryRepo.saved))
	}
}

// Los demás eventos de pago (failed, initiated...) no mueven dinero.
func TestPayment_OtherSubjectsIgnored(t *testing.T) {
	postEntry, entryRepo, _ := newPostEntry()
	consumer := NewPaymentConsumer(&fakeConsumer{}, postEntry, newFakeAccountRepo(), discardLogger())
	msg := &eventbus.Message{Subject: "payment.failed.v1", Payload: []byte(`{bad`)}
	if err := consumer.handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entryRepo.saved) != 0 {
		t.Errorf("expected no entry, got %d", len(entryRepo.saved))
	}
}
