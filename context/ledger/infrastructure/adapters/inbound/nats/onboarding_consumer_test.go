package nats

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

func TestOnboarding_CreatesFiveAccounts(t *testing.T) {
	accountRepo := newFakeAccountRepo()
	createAccount, pub := newCreateAccount(accountRepo)
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())

	tenantID := testTenantID(t)
	msg := &eventbus.Message{
		Subject: "onboarding.application.approved.v1",
		Payload: []byte(`{"tenant_id":"` + tenantID.String() + `","currency":"USD"}`),
	}
	if err := consumer.handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accountRepo.saveCount != 5 {
		t.Errorf("expected 5 accounts created, got %d", accountRepo.saveCount)
	}
	if len(pub.published) != 5 {
		t.Errorf("expected 5 AccountCreated events, got %d", len(pub.published))
	}
	// La moneda del evento se respeta.
	if _, err := accountRepo.FindByTenantAndType(context.Background(), tenantID, "merchant_balance", "USD"); err != nil {
		t.Errorf("merchant_balance USD account not created: %v", err)
	}
}

func TestOnboarding_DefaultsCurrencyToARS(t *testing.T) {
	accountRepo := newFakeAccountRepo()
	createAccount, _ := newCreateAccount(accountRepo)
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())

	tenantID := testTenantID(t)
	// Sin currency en el payload → default ARS.
	msg := &eventbus.Message{Payload: []byte(`{"tenant_id":"` + tenantID.String() + `"}`)}
	if err := consumer.handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := accountRepo.FindByTenantAndType(context.Background(), tenantID, "reserve", "ARS"); err != nil {
		t.Errorf("expected ARS accounts by default: %v", err)
	}
}

// Redelivery tras un éxito (p. ej. falló el ack): no debe chocar contra el
// UNIQUE de cuentas ni crear duplicados.
func TestOnboarding_RedeliveryIsIdempotent(t *testing.T) {
	accountRepo := newFakeAccountRepo()
	createAccount, pub := newCreateAccount(accountRepo)
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())

	msg := &eventbus.Message{Payload: []byte(`{"tenant_id":"` + testTenantID(t).String() + `","currency":"ARS"}`)}
	for i := 0; i < 2; i++ {
		if err := consumer.handle(context.Background(), msg); err != nil {
			t.Fatalf("delivery %d: unexpected error: %v", i+1, err)
		}
	}
	if accountRepo.saveCount != 5 {
		t.Errorf("expected 5 accounts total, got %d", accountRepo.saveCount)
	}
	if len(pub.published) != 5 {
		t.Errorf("expected 5 AccountCreated events total, got %d", len(pub.published))
	}
}

// Reintento tras un fallo parcial: las cuentas ya creadas se reutilizan y se
// completan las faltantes (antes, el reintento fallaba para siempre en la primera).
func TestOnboarding_RetryAfterPartialFailureCompletesAccounts(t *testing.T) {
	tenantID := testTenantID(t)
	accountRepo := newFakeAccountRepo()
	accountRepo.seed(t, tenantID, domain.AccountTypeMerchantBalance, "ARS")
	accountRepo.seed(t, tenantID, domain.AccountTypeReserve, "ARS")
	createAccount, pub := newCreateAccount(accountRepo)
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())

	msg := &eventbus.Message{Payload: []byte(`{"tenant_id":"` + tenantID.String() + `","currency":"ARS"}`)}
	if err := consumer.handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accountRepo.saveCount != 3 {
		t.Errorf("expected only the 3 missing accounts to be created, got %d", accountRepo.saveCount)
	}
	if len(pub.published) != 3 {
		t.Errorf("expected 3 AccountCreated events, got %d", len(pub.published))
	}
}

func TestOnboarding_MalformedPayload(t *testing.T) {
	createAccount, _ := newCreateAccount(newFakeAccountRepo())
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())
	if err := consumer.handle(context.Background(), &eventbus.Message{Payload: []byte(`{bad`)}); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestOnboarding_CreateAccountErrorPropagates(t *testing.T) {
	accountRepo := newFakeAccountRepo()
	accountRepo.saveErr = errBoom
	createAccount, _ := newCreateAccount(accountRepo)
	consumer := NewOnboardingConsumer(&fakeConsumer{}, createAccount, discardLogger())

	msg := &eventbus.Message{Payload: []byte(`{"tenant_id":"` + testTenantID(t).String() + `"}`)}
	if err := consumer.handle(context.Background(), msg); err == nil {
		t.Fatal("expected error when account creation fails")
	}
}

func TestOnboarding_StartConfig(t *testing.T) {
	fc := &fakeConsumer{}
	createAccount, _ := newCreateAccount(newFakeAccountRepo())
	consumer := NewOnboardingConsumer(fc, createAccount, discardLogger())

	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	assertConfig(t, fc.gotCfg, "ONBOARDING", "ledger-onboarding-consumer", "onboarding.application.approved.v1")
}

// assertConfig verifica los campos comunes de un ConsumerConfig.
func assertConfig(t *testing.T, cfg eventbus.ConsumerConfig, stream, name, filter string) {
	t.Helper()
	if cfg.Stream != stream {
		t.Errorf("stream = %q, want %q", cfg.Stream, stream)
	}
	if cfg.Name != name {
		t.Errorf("name = %q, want %q", cfg.Name, name)
	}
	if cfg.FilterSubject != filter {
		t.Errorf("filter = %q, want %q", cfg.FilterSubject, filter)
	}
	if cfg.MaxDeliver != 5 {
		t.Errorf("maxDeliver = %d, want 5", cfg.MaxDeliver)
	}
}

// errBoom es un error genérico para inyección en los fakes.
var errBoom = errors.New("boom")
