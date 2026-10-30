package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/audit/application"
	"github.com/juantevez/cobros-platform/context/audit/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func init() { gin.SetMode(gin.TestMode) }

// ── Fake repo ─────────────────────────────────────────────────────────────────

type fakeRepo struct {
	recent    []*domain.AuditLogEntry
	byTenant  []*domain.AuditLogEntry
	fromID    []*domain.AuditLogEntry
	recentErr error
	tenantErr error
	fromIDErr error

	gotTenantID string
}

func (r *fakeRepo) Save(ctx context.Context, e *domain.AuditLogEntry) error     { return nil }
func (r *fakeRepo) FindLast(ctx context.Context) (*domain.AuditLogEntry, error) { return nil, nil }
func (r *fakeRepo) LockChain(ctx context.Context) error                         { return nil }
func (r *fakeRepo) FindBefore(ctx context.Context, id int64) (*domain.AuditLogEntry, error) {
	return nil, nil
}
func (r *fakeRepo) ExistsByEventID(ctx context.Context, id string) (bool, error) { return false, nil }
func (r *fakeRepo) ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error) {
	return r.recent, r.recentErr
}
func (r *fakeRepo) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error) {
	r.gotTenantID = tenantID
	return r.byTenant, r.tenantErr
}
func (r *fakeRepo) ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error) {
	return r.fromID, r.fromIDErr
}

func sha(p string) []byte { s := sha256.Sum256([]byte(p)); return s[:] }

func entry(tenantID string, prevHash []byte) *domain.AuditLogEntry {
	e, _ := domain.NewAuditLogEntry(domain.EntryInput{TenantID: tenantID, Actor: "actor", Action: domain.ActionLogin, ResourceType: domain.ResourceUser, ResourceID: "u1"}, prevHash, time.Now(), sha)
	return e
}

func newHandler(repo *fakeRepo) *AuditHandler {
	hasher := hasherFunc(sha)
	return NewAuditHandler(
		application.NewListLogsUseCase(repo),
		application.NewVerifyChainUseCase(repo, hasher),
	)
}

type hasherFunc func(string) []byte

func (h hasherFunc) Compute(p string) []byte { return h(p) }

// doRequest ejecuta el handler dado sobre una request de prueba.
func doRequest(h gin.HandlerFunc, target string, ctxSetup func(context.Context) context.Context) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if ctxSetup != nil {
		req = req.WithContext(ctxSetup(req.Context()))
	}
	c.Request = req
	h(c)
	return rec
}

// ── ListLogs ──────────────────────────────────────────────────────────────────

func withTenant(id string) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context { return postgres.WithTenantID(ctx, id) }
}

func TestListLogs(t *testing.T) {
	t.Run("returns the authenticated tenant's entries", func(t *testing.T) {
		repo := &fakeRepo{byTenant: []*domain.AuditLogEntry{entry("ctx-tenant", nil), entry("ctx-tenant", nil)}}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs", withTenant("ctx-tenant"))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body struct {
			Entries []application.LogEntryView `json:"entries"`
			Count   int                        `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad json: %v", err)
		}
		if body.Count != 2 || len(body.Entries) != 2 {
			t.Errorf("count = %d, entries = %d", body.Count, len(body.Entries))
		}
		if repo.gotTenantID != "ctx-tenant" {
			t.Errorf("tenantID = %q, want ctx-tenant", repo.gotTenantID)
		}
	})

	t.Run("tenant_id equal to own tenant is allowed", func(t *testing.T) {
		repo := &fakeRepo{}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs?tenant_id=ctx-tenant", withTenant("ctx-tenant"))
		if rec.Code != http.StatusOK || repo.gotTenantID != "ctx-tenant" {
			t.Fatalf("status = %d, tenant = %q", rec.Code, repo.gotTenantID)
		}
	})

	t.Run("another tenant's log is forbidden", func(t *testing.T) {
		repo := &fakeRepo{}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs?tenant_id=other-tenant", withTenant("ctx-tenant"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if repo.gotTenantID != "" {
			t.Error("repo must not be queried")
		}
	})

	t.Run("without tenant in context it never falls back to the whole platform", func(t *testing.T) {
		repo := &fakeRepo{recent: []*domain.AuditLogEntry{entry("t1", nil)}}
		for _, target := range []string{"/audit/logs", "/audit/logs?tenant_id=t1"} {
			rec := doRequest(newHandler(repo).ListLogs, target, nil)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", target, rec.Code)
			}
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		repo := &fakeRepo{tenantErr: errors.New("boom")}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs", withTenant("t"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestListPlatformLogs(t *testing.T) {
	t.Run("with tenant_id reads that merchant", func(t *testing.T) {
		repo := &fakeRepo{}
		rec := doRequest(newHandler(repo).ListPlatformLogs, "/audit/platform/logs?tenant_id=tenant-q", withTenant("platform"))
		if rec.Code != http.StatusOK || repo.gotTenantID != "tenant-q" {
			t.Fatalf("status = %d, tenant = %q", rec.Code, repo.gotTenantID)
		}
	})

	t.Run("without tenant_id reads the whole platform", func(t *testing.T) {
		repo := &fakeRepo{recent: []*domain.AuditLogEntry{entry("t1", nil), entry("t2", nil)}}
		rec := doRequest(newHandler(repo).ListPlatformLogs, "/audit/platform/logs", withTenant("platform"))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":2`) {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
	})
}

// ── VerifyChain ───────────────────────────────────────────────────────────────

func TestVerifyChain(t *testing.T) {
	t.Run("valid chain returns 200", func(t *testing.T) {
		first := entry("t", nil)
		second := entry("t", first.Hash())
		repo := &fakeRepo{fromID: []*domain.AuditLogEntry{first, second}}
		rec := doRequest(newHandler(repo).VerifyChain, "/audit/verify", nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var res application.VerifyChainResult
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("bad json: %v", err)
		}
		if !res.Valid {
			t.Errorf("expected valid result: %+v", res)
		}
	})

	t.Run("broken chain returns 409", func(t *testing.T) {
		// Segunda entrada con prevHash que no corresponde: enlace roto.
		bad := entry("t", []byte("not-the-real-prev-hash-32bytes!!"))
		repo := &fakeRepo{fromID: []*domain.AuditLogEntry{entry("t", nil), bad}}
		rec := doRequest(newHandler(repo).VerifyChain, "/audit/verify", nil)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		repo := &fakeRepo{fromIDErr: errors.New("boom")}
		rec := doRequest(newHandler(repo).VerifyChain, "/audit/verify", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// ── RegisterRoutes ────────────────────────────────────────────────────────────

func allow(c *gin.Context) { c.Next() }
func deny(c *gin.Context)  { c.AbortWithStatus(http.StatusForbidden) }

func TestRegisterRoutes(t *testing.T) {
	serve := func(guards RouteGuards, target string) int {
		repo := &fakeRepo{recent: []*domain.AuditLogEntry{entry("t", nil)}}
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Request = c.Request.WithContext(postgres.WithTenantID(c.Request.Context(), "t"))
		})
		RegisterRoutes(r.Group("/api/v1"), newHandler(repo), guards)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code
	}
	routes := []string{"/api/v1/audit/logs", "/api/v1/audit/platform/logs", "/api/v1/audit/verify"}

	t.Run("routes are wired", func(t *testing.T) {
		for _, target := range routes {
			if code := serve(RouteGuards{ReadTenantLogs: allow, PlatformAudit: allow}, target); code != http.StatusOK {
				t.Errorf("%s status = %d, want 200", target, code)
			}
		}
	})

	t.Run("each route enforces its guard", func(t *testing.T) {
		tenantOnly := RouteGuards{ReadTenantLogs: allow, PlatformAudit: deny}
		if code := serve(tenantOnly, "/api/v1/audit/logs"); code != http.StatusOK {
			t.Errorf("tenant logs: status = %d, want 200", code)
		}
		for _, target := range routes[1:] {
			if code := serve(tenantOnly, target); code != http.StatusForbidden {
				t.Errorf("%s without platform role: status = %d, want 403", target, code)
			}
		}
		if code := serve(RouteGuards{ReadTenantLogs: deny, PlatformAudit: allow}, "/api/v1/audit/logs"); code != http.StatusForbidden {
			t.Errorf("tenant logs without role: status = %d, want 403", code)
		}
	})

	t.Run("missing guard panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic for missing guard")
			}
		}()
		RegisterRoutes(gin.New().Group("/api/v1"), newHandler(&fakeRepo{}), RouteGuards{ReadTenantLogs: allow})
	})
}
