package application

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/notification/domain"
)

var errBoom = errors.New("boom")

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── fakeNotifRepo ───────────────────────────────────────────────────────────

type fakeNotifRepo struct {
	saved    []*domain.Notification
	listed   []*domain.Notification
	saveErr  error
	listErr  error
	gotLimit int
}

func (r *fakeNotifRepo) Save(ctx context.Context, n *domain.Notification) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, n)
	return nil
}

func (r *fakeNotifRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	r.gotLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listed, nil
}

// ── fakePrefRepo ────────────────────────────────────────────────────────────

type fakePrefRepo struct {
	upserted []*domain.NotificationPreference
	byEvent  map[string]*domain.NotificationPreference
	listed   []*domain.NotificationPreference
	upsertErr error
	findErr   error
	listErr   error
}

func newPrefRepo() *fakePrefRepo {
	return &fakePrefRepo{byEvent: map[string]*domain.NotificationPreference{}}
}

func (r *fakePrefRepo) Upsert(ctx context.Context, p *domain.NotificationPreference) error {
	if r.upsertErr != nil {
		return r.upsertErr
	}
	r.upserted = append(r.upserted, p)
	r.byEvent[p.EventType()] = p
	return nil
}

func (r *fakePrefRepo) FindByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) (*domain.NotificationPreference, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	p, ok := r.byEvent[eventType]
	if !ok {
		return nil, domain.ErrPreferenceNotFound
	}
	return p, nil
}

func (r *fakePrefRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listed, nil
}

// ── fakeEmailSender ─────────────────────────────────────────────────────────

type fakeEmailSender struct {
	sent []sentEmail
	err  error
}

type sentEmail struct {
	to, subject, body string
}

func (s *fakeEmailSender) Send(ctx context.Context, to, subject, body string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, sentEmail{to, subject, body})
	return nil
}

// ── fakeContactReader ───────────────────────────────────────────────────────

type fakeContactReader struct {
	email string
	err   error
}

func (c fakeContactReader) GetTenantAdminEmail(ctx context.Context, tenantID string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return c.email, nil
}
