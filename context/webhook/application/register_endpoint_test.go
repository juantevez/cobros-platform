package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/webhook/domain"
)

func TestRegisterEndpointUseCase_Execute(t *testing.T) {
	ctx := context.Background()
	validCmd := RegisterEndpointCmd{
		TenantID:    tenantA,
		URL:         "https://merchant.example.com/hooks",
		Events:      []string{"payment.captured.v1", "payout.confirmed", "payment.captured"},
		Description: "prod",
	}

	type deps struct {
		repo  *fakeEndpointRepo
		guard *fakeGuard
		gen   *fakeSecretGen
		tx    *fakeTx
		pub   *fakePublisher
	}
	newDeps := func() *deps {
		return &deps{repo: newEndpointRepo(), guard: &fakeGuard{}, gen: &fakeSecretGen{secret: "whsec_0123456789abcdef"}, tx: &fakeTx{}, pub: &fakePublisher{}}
	}
	exec := func(d *deps, cmd RegisterEndpointCmd) (RegisterEndpointResult, error) {
		return NewRegisterEndpointUseCase(d.repo, d.guard, d.gen, d.tx, d.pub).Execute(ctx, cmd)
	}

	t.Run("internal destination is rejected and nothing is saved", func(t *testing.T) {
		d := newDeps()
		d.guard.err = fmt.Errorf("%w: 127.0.0.1", domain.ErrDestinationNotAllowed)

		_, err := exec(d, validCmd)
		if !errors.Is(err, domain.ErrDestinationNotAllowed) {
			t.Fatalf("expected ErrDestinationNotAllowed, got %v", err)
		}
		if len(d.guard.urls) != 1 || d.guard.urls[0] != validCmd.URL {
			t.Errorf("guard checked %v", d.guard.urls)
		}
		if d.tx.calls != 0 || len(d.pub.events) != 0 {
			t.Errorf("tx calls = %d published = %d, want nothing", d.tx.calls, len(d.pub.events))
		}
	})

	t.Run("registers endpoint and returns secret once", func(t *testing.T) {
		d := newDeps()

		res, err := exec(d, validCmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Secret != "whsec_0123456789abcdef" || res.SecretHint != "...cdef" {
			t.Errorf("secret=%q hint=%q", res.Secret, res.SecretHint)
		}
		if len(d.repo.saved) != 1 {
			t.Fatalf("expected 1 saved endpoint, got %d", len(d.repo.saved))
		}
		e := d.repo.saved[0]
		if e.ID().String() != res.EndpointID || e.TenantID() != domain.TenantID(tenantA) || !e.Active() {
			t.Errorf("endpoint = id %q tenant %q active %v", e.ID(), e.TenantID(), e.Active())
		}
		if e.URL() != validCmd.URL || e.Description() != "prod" || e.Secret() != res.Secret {
			t.Errorf("fields not propagated: url=%q desc=%q", e.URL(), e.Description())
		}
		// Los eventos se normalizan: sin versión y sin duplicados.
		if want := []string{"payment.captured", "payout.confirmed"}; !reflect.DeepEqual(e.Events(), want) {
			t.Errorf("events = %v, want %v", e.Events(), want)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"webhook.endpoint.registered.v1"}) {
			t.Errorf("published = %v", got)
		}
		if d.tx.calls != 1 {
			t.Errorf("tx calls = %d, want 1", d.tx.calls)
		}
	})

	t.Run("invalid tenant rejected before generating secret", func(t *testing.T) {
		d := newDeps()
		cmd := validCmd
		cmd.TenantID = "nope"

		_, err := exec(d, cmd)
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
		if d.gen.calls != 0 {
			t.Error("secret should not be generated")
		}
	})

	t.Run("secret generation error is wrapped", func(t *testing.T) {
		d := newDeps()
		d.gen.err = errBoom

		_, err := exec(d, validCmd)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "generate secret") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if len(d.repo.saved) != 0 {
			t.Error("nothing should be saved")
		}
	})

	validation := []struct {
		name    string
		mutate  func(*RegisterEndpointCmd)
		wantErr error
		wantMsg string
	}{
		{"empty url", func(c *RegisterEndpointCmd) { c.URL = "" }, domain.ErrEndpointURLEmpty, ""},
		{"relative url", func(c *RegisterEndpointCmd) { c.URL = "merchant/hooks" }, nil, "invalid webhook URL"},
		{"no events", func(c *RegisterEndpointCmd) { c.Events = nil }, domain.ErrNoEventsSubscribed, ""},
	}
	for _, tc := range validation {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			d := newDeps()
			cmd := validCmd
			tc.mutate(&cmd)

			_, err := exec(d, cmd)
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
			if tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			}
			if d.tx.calls != 0 {
				t.Error("nothing should be persisted")
			}
		})
	}

	t.Run("save error is wrapped and nothing is published", func(t *testing.T) {
		d := newDeps()
		d.repo.saveErr = errBoom

		_, err := exec(d, validCmd)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "save endpoint") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("event must not be published")
		}
	})

	t.Run("publish error is returned", func(t *testing.T) {
		d := newDeps()
		d.pub.err = errBoom

		res, err := exec(d, validCmd)
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if res.Secret != "" {
			t.Error("secret must not be returned when registration fails")
		}
	})
}

func TestDeactivateEndpointUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("deactivates endpoint and publishes event", func(t *testing.T) {
		e := newEndpoint(tenantA)
		repo, tx, pub := newEndpointRepo(e), &fakeTx{}, &fakePublisher{}

		err := NewDeactivateEndpointUseCase(repo, tx, pub).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: e.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.Active() {
			t.Error("endpoint should be inactive")
		}
		if len(repo.updated) != 1 || repo.updated[0] != e {
			t.Error("endpoint should be updated once")
		}
		if got := pub.eventTypes(); !reflect.DeepEqual(got, []string{"webhook.endpoint.deactivated.v1"}) {
			t.Errorf("published = %v", got)
		}
	})

	// Un segundo DELETE volvía a emitir el evento y pisaba updated_at.
	t.Run("already inactive endpoint is a no-op", func(t *testing.T) {
		e := newEndpoint(tenantA)
		e.Deactivate()
		e.PullEvents()
		repo, tx, pub := newEndpointRepo(e), &fakeTx{}, &fakePublisher{}

		err := NewDeactivateEndpointUseCase(repo, tx, pub).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: e.ID().String()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repo.updated) != 0 || tx.calls != 0 || pub.calls != 0 {
			t.Errorf("updates = %d tx = %d publish = %d, want none", len(repo.updated), tx.calls, pub.calls)
		}
	})

	t.Run("invalid tenant id", func(t *testing.T) {
		err := NewDeactivateEndpointUseCase(newEndpointRepo(), &fakeTx{}, &fakePublisher{}).Execute(ctx, DeactivateEndpointCmd{TenantID: "x", EndpointID: domain.NewEndpointID().String()})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant error, got %v", err)
		}
	})

	t.Run("invalid endpoint id", func(t *testing.T) {
		err := NewDeactivateEndpointUseCase(newEndpointRepo(), &fakeTx{}, &fakePublisher{}).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid endpoint id") {
			t.Fatalf("expected invalid endpoint error, got %v", err)
		}
	})

	t.Run("unknown endpoint returns ErrEndpointNotFound", func(t *testing.T) {
		err := NewDeactivateEndpointUseCase(newEndpointRepo(), &fakeTx{}, &fakePublisher{}).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: domain.NewEndpointID().String()})
		if !errors.Is(err, domain.ErrEndpointNotFound) {
			t.Fatalf("expected ErrEndpointNotFound, got %v", err)
		}
	})

	t.Run("endpoint of another tenant is reported as not found", func(t *testing.T) {
		e := newEndpoint(tenantB)
		repo, tx := newEndpointRepo(e), &fakeTx{}

		err := NewDeactivateEndpointUseCase(repo, tx, &fakePublisher{}).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: e.ID().String()})
		if !errors.Is(err, domain.ErrEndpointNotFound) {
			t.Fatalf("expected ErrEndpointNotFound, got %v", err)
		}
		if !e.Active() || tx.calls != 0 {
			t.Error("foreign endpoint must not be touched")
		}
	})

	t.Run("update error is wrapped and nothing is published", func(t *testing.T) {
		e := newEndpoint(tenantA)
		repo, pub := newEndpointRepo(e), &fakePublisher{}
		repo.updateErr = errBoom

		err := NewDeactivateEndpointUseCase(repo, &fakeTx{}, pub).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: e.ID().String()})
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "update endpoint") {
			t.Fatalf("expected wrapped errBoom, got %v", err)
		}
		if pub.calls != 0 {
			t.Error("event must not be published")
		}
	})

	t.Run("publish error is returned", func(t *testing.T) {
		e := newEndpoint(tenantA)
		err := NewDeactivateEndpointUseCase(newEndpointRepo(e), &fakeTx{}, &fakePublisher{err: errBoom}).Execute(ctx, DeactivateEndpointCmd{TenantID: tenantA, EndpointID: e.ID().String()})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
