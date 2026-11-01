package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/notification/application"
	"github.com/juantevez/cobros-platform/context/notification/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

var errBoom = errors.New("boom")

// ── Fakes de puertos ──────────────────────────────────────────────────────────

type fakeNotifRepo struct {
	listed  []*domain.Notification
	listErr error
}

func (r *fakeNotifRepo) Save(ctx context.Context, n *domain.Notification) error { return nil }
func (r *fakeNotifRepo) ExistsByEventMsg(ctx context.Context, tenantID domain.TenantID, eventType, msgID string) (bool, error) {
	return false, nil
}
func (r *fakeNotifRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Notification, error) {
	return r.listed, r.listErr
}

type fakePrefRepo struct {
	listed    []*domain.NotificationPreference
	listErr   error
	upsertErr error
}

func (r *fakePrefRepo) Upsert(ctx context.Context, p *domain.NotificationPreference) error {
	return r.upsertErr
}
func (r *fakePrefRepo) FindByTenantAndEvent(ctx context.Context, tenantID domain.TenantID, eventType string) (*domain.NotificationPreference, error) {
	return nil, domain.ErrPreferenceNotFound
}
func (r *fakePrefRepo) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.NotificationPreference, error) {
	return r.listed, r.listErr
}

func newHandler(nr *fakeNotifRepo, pr *fakePrefRepo) *NotificationHandler {
	return NewNotificationHandler(
		application.NewListNotificationsUseCase(nr),
		application.NewGetPreferencesUseCase(pr),
		application.NewUpdatePreferenceUseCase(pr, realClock{}),
	)
}

func doRequest(h gin.HandlerFunc, method, target, body, tenantID string, params gin.Params) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if tenantID != "" {
		req = req.WithContext(postgres.WithTenantID(req.Context(), tenantID))
	}
	c.Request = req
	c.Params = params
	h(c)
	c.Writer.WriteHeaderNow()
	return rec
}

// ── ListNotifications ─────────────────────────────────────────────────────────

func TestListNotifications(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns notifications and count", func(t *testing.T) {
		n := domain.NewNotification(domain.NewNotificationID(), domain.TenantID(tid),
			"payment.captured", domain.ChannelEmail, "u@x.com", "Pago", time.Now())
		nr := &fakeNotifRepo{listed: []*domain.Notification{n}}
		rec := doRequest(newHandler(nr, &fakePrefRepo{}).ListNotifications,
			http.MethodGet, "/notifications?limit=10", "", tid, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body struct {
			Count int `json:"count"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Count != 1 {
			t.Errorf("count = %d, want 1", body.Count)
		}
	})

	t.Run("invalid tenant yields 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).ListNotifications,
			http.MethodGet, "/notifications", "", "bad-tenant", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("missing tenant yields 403 on every endpoint", func(t *testing.T) {
		h := newHandler(&fakeNotifRepo{}, &fakePrefRepo{})
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"list":      doRequest(h.ListNotifications, http.MethodGet, "/notifications", "", "", nil),
			"get prefs": doRequest(h.GetPreferences, http.MethodGet, "/notifications/preferences", "", "", nil),
			"update pref": doRequest(h.UpdatePreference, http.MethodPut, "/x", `{"enabled":false}`, "",
				gin.Params{{Key: "eventType", Value: "payment.captured"}}),
		} {
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", name, rec.Code)
			}
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{listErr: errBoom}, &fakePrefRepo{}).ListNotifications,
			http.MethodGet, "/notifications", "", tid, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── GetPreferences ────────────────────────────────────────────────────────────

func TestGetPreferences(t *testing.T) {
	tid := uuid.NewString()

	t.Run("returns preferences", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).GetPreferences,
			http.MethodGet, "/notifications/preferences", "", tid, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body struct {
			Preferences []application.PreferenceView `json:"preferences"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Preferences) == 0 {
			t.Error("expected default preferences")
		}
	})

	t.Run("invalid tenant yields 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).GetPreferences,
			http.MethodGet, "/notifications/preferences", "", "bad-tenant", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{listErr: errBoom}).GetPreferences,
			http.MethodGet, "/notifications/preferences", "", tid, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── UpdatePreference ──────────────────────────────────────────────────────────

func TestUpdatePreference(t *testing.T) {
	tid := uuid.NewString()

	eventParam := func(et string) gin.Params { return gin.Params{{Key: "eventType", Value: et}} }

	t.Run("valid update returns 204", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).UpdatePreference,
			http.MethodPut, "/x", `{"enabled":false,"recipient_email":"ops@x.com"}`,
			tid, eventParam("payment.captured"))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("invalid body yields 400", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).UpdatePreference,
			http.MethodPut, "/x", `{not json`, tid, eventParam("payment.captured"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("unknown event type yields 404", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{}).UpdatePreference,
			http.MethodPut, "/x", `{"enabled":true}`, tid, eventParam("unknown.event"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("upsert error yields 500", func(t *testing.T) {
		rec := doRequest(newHandler(&fakeNotifRepo{}, &fakePrefRepo{upsertErr: errBoom}).UpdatePreference,
			http.MethodPut, "/x", `{"enabled":true}`, tid, eventParam("payment.captured"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func TestRegisterRoutes(t *testing.T) {
	tid := uuid.NewString()
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(postgres.WithTenantID(c.Request.Context(), tid))
		c.Next()
	})
	deny := func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
	prefs := &fakePrefRepo{}
	RegisterRoutes(engine.Group("/api/v1"), newHandler(&fakeNotifRepo{}, prefs), RouteGuards{Manage: deny})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/preferences", nil)
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET preferences status = %d, want 200", rec.Code)
	}

	// Las preferencias son del tenant: sin el guard, un read_only podía
	// apagar los emails de todo el comercio.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/notifications/preferences/payout.failed", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT preference status = %d, want 403", rec.Code)
	}
}

func TestRegisterRoutes_PanicsWithoutGuard(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic without Manage guard")
		}
	}()
	RegisterRoutes(gin.New().Group("/api/v1"), &NotificationHandler{}, RouteGuards{})
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
