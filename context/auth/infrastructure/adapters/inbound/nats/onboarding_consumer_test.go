package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// ── Fakes de puertos de application ───────────────────────────────────────────

type fakeTx struct{}

func (fakeTx) RunInTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeTenantRepo struct {
	tenants map[domain.TenantID]*domain.Tenant
	findErr error
}

func newFakeTenantRepo(ts ...*domain.Tenant) *fakeTenantRepo {
	m := map[domain.TenantID]*domain.Tenant{}
	for _, t := range ts {
		m[t.ID()] = t
	}
	return &fakeTenantRepo{tenants: m}
}

func (r *fakeTenantRepo) Save(ctx context.Context, t *domain.Tenant) error { r.tenants[t.ID()] = t; return nil }
func (r *fakeTenantRepo) Update(ctx context.Context, t *domain.Tenant) error {
	r.tenants[t.ID()] = t
	return nil
}
func (r *fakeTenantRepo) FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	t, ok := r.tenants[id]
	if !ok {
		return nil, domain.ErrTenantNotFound
	}
	return t, nil
}

type fakePublisher struct{ published []domain.Event }

func (p *fakePublisher) Publish(ctx context.Context, events ...domain.Event) error {
	p.published = append(p.published, events...)
	return nil
}

// ── Fake eventbus.Consumer ────────────────────────────────────────────────────

type fakeConsumer struct {
	gotCfg     eventbus.ConsumerConfig
	gotHandler eventbus.Handler
	startErr   error
}

func (c *fakeConsumer) Start(ctx context.Context, cfg eventbus.ConsumerConfig, h eventbus.Handler) error {
	c.gotCfg = cfg
	c.gotHandler = h
	return c.startErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newPendingTenant(t *testing.T) *domain.Tenant {
	t.Helper()
	tenant, err := domain.NewTenant(domain.NewTenantID(), "Acme SA")
	if err != nil {
		t.Fatalf("build tenant: %v", err)
	}
	tenant.PullEvents()
	return tenant
}

func newConsumerWith(repo *fakeTenantRepo) (*OnboardingConsumer, *fakePublisher) {
	pub := &fakePublisher{}
	activate := application.NewActivateTenantUseCase(repo, fakeTx{}, pub)
	return NewOnboardingConsumer(&fakeConsumer{}, activate, discardLogger()), pub
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestHandle_ActivatesTenant(t *testing.T) {
	tenant := newPendingTenant(t)
	repo := newFakeTenantRepo(tenant)
	consumer, pub := newConsumerWith(repo)

	msg := &eventbus.Message{
		Subject: "onboarding.application.approved.v1",
		Payload: []byte(`{"tenant_id":"` + tenant.ID().String() + `","business_category":"retail","currency":"ARS"}`),
	}
	if err := consumer.handle(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// El tenant queda activo en producción.
	updated := repo.tenants[tenant.ID()]
	if !updated.IsActive() || !updated.Environment().IsProd() {
		t.Errorf("tenant not activated in production: status=%s env=%s", updated.Status(), updated.Environment())
	}
	// Se publicó TenantActivatedEvent.
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.TenantActivatedEvent); !ok {
		t.Fatalf("expected TenantActivatedEvent, got %T", pub.published[0])
	}
}

func TestHandle_MalformedPayload(t *testing.T) {
	consumer, _ := newConsumerWith(newFakeTenantRepo())
	msg := &eventbus.Message{
		Subject: "onboarding.application.approved.v1",
		Payload: []byte(`{not valid json`),
	}
	err := consumer.handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected an unmarshal error")
	}
}

func TestHandle_InvalidTenantIDPropagates(t *testing.T) {
	consumer, _ := newConsumerWith(newFakeTenantRepo())
	msg := &eventbus.Message{
		Payload: []byte(`{"tenant_id":"not-a-uuid"}`),
	}
	err := consumer.handle(context.Background(), msg)
	if !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestHandle_TenantNotFoundPropagates(t *testing.T) {
	consumer, _ := newConsumerWith(newFakeTenantRepo()) // repo vacío
	msg := &eventbus.Message{
		Payload: []byte(`{"tenant_id":"` + domain.NewTenantID().String() + `"}`),
	}
	err := consumer.handle(context.Background(), msg)
	if !errors.Is(err, domain.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestHandle_AlreadyActiveIsError(t *testing.T) {
	tenant := newPendingTenant(t)
	_ = tenant.Activate(domain.EnvironmentProduction)
	tenant.PullEvents()
	repo := newFakeTenantRepo(tenant)
	consumer, _ := newConsumerWith(repo)

	msg := &eventbus.Message{
		Payload: []byte(`{"tenant_id":"` + tenant.ID().String() + `"}`),
	}
	err := consumer.handle(context.Background(), msg)
	if !errors.Is(err, domain.ErrTenantCannotTransition) {
		t.Fatalf("expected ErrTenantCannotTransition, got %v", err)
	}
}

func TestStart_RegistersDurableConsumer(t *testing.T) {
	fc := &fakeConsumer{}
	activate := application.NewActivateTenantUseCase(newFakeTenantRepo(), fakeTx{}, &fakePublisher{})
	consumer := NewOnboardingConsumer(fc, activate, discardLogger())

	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.gotCfg.Stream != "ONBOARDING" {
		t.Errorf("stream = %q, want ONBOARDING", fc.gotCfg.Stream)
	}
	if fc.gotCfg.Name != "auth-onboarding-consumer" {
		t.Errorf("name = %q, want auth-onboarding-consumer", fc.gotCfg.Name)
	}
	if fc.gotCfg.FilterSubject != "onboarding.application.approved.v1" {
		t.Errorf("filter = %q", fc.gotCfg.FilterSubject)
	}
	if fc.gotCfg.MaxDeliver != 5 {
		t.Errorf("maxDeliver = %d, want 5", fc.gotCfg.MaxDeliver)
	}
	if fc.gotHandler == nil {
		t.Error("handler was not registered")
	}
}

func TestStart_PropagatesConsumerError(t *testing.T) {
	fc := &fakeConsumer{startErr: errors.New("stream missing")}
	activate := application.NewActivateTenantUseCase(newFakeTenantRepo(), fakeTx{}, &fakePublisher{})
	consumer := NewOnboardingConsumer(fc, activate, discardLogger())

	if err := consumer.Start(context.Background()); err == nil {
		t.Fatal("expected the consumer.Start error to propagate")
	}
}
