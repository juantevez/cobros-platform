package nats

import (
	"context"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// Los eventos que el Ledger nunca va a aceptar se descartan sin reintentos;
// los que pueden resolverse solos (una cuenta todavía no creada) se reintentan.
func TestConsumers_PermanentErrors(t *testing.T) {
	tenantID := testTenantID(t)
	tid := tenantID.String()
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeDisputeHold, "ARS")
	postEntry, _, _ := newPostEntry(accountRepo)

	started := func(start func(context.Context) error, fc *fakeConsumer) eventbus.Handler {
		t.Helper()
		if err := start(context.Background()); err != nil {
			t.Fatalf("start: %v", err)
		}
		return fc.gotHandler
	}
	payFC, disFC := &fakeConsumer{}, &fakeConsumer{}
	payment := started(NewPaymentConsumer(payFC, postEntry, accountRepo, discardLogger()).Start, payFC)
	dispute := started(NewDisputeConsumer(disFC, postEntry, accountRepo, discardLogger()).Start, disFC)

	cases := []struct {
		name      string
		h         eventbus.Handler
		msg       *eventbus.Message
		permanent bool
	}{
		{"malformed payload", payment, &eventbus.Message{Subject: "payment.captured.v1", Payload: []byte(`{bad`)}, true},
		{"non-positive net", payment, paymentMsg(tid, validUUID(), 100, 100, 0), true},
		{"unknown dispute outcome", dispute, disputeMsg("dispute.resolved.v1", tid, validUUID(), "pending", 100), true},
		{"invalid tenant id", dispute, disputeMsg("dispute.resolved.v1", "not-a-uuid", validUUID(), "won", 100), true},
		{"account not created yet", payment, paymentMsg(tid, validUUID(), 1000, 0, 0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h(context.Background(), tc.msg)
			if err == nil {
				t.Fatal("expected error")
			}
			if eventbus.IsPermanent(err) != tc.permanent {
				t.Errorf("permanent = %v, want %v (err: %v)", !tc.permanent, tc.permanent, err)
			}
		})
	}
}
