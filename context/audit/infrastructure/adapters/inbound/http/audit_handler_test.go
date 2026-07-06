package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	e, _ := domain.NewAuditLogEntry(tenantID, "actor", domain.ActionLogin, domain.ResourceUser, "u1",
		nil, "", prevHash, time.Now(), sha)
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

func TestListLogs(t *testing.T) {
	t.Run("returns entries and count", func(t *testing.T) {
		repo := &fakeRepo{recent: []*domain.AuditLogEntry{entry("t1", nil), entry("t2", nil)}}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs", nil)

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
	})

	t.Run("explicit tenant_id query routes to ListByTenant", func(t *testing.T) {
		repo := &fakeRepo{byTenant: []*domain.AuditLogEntry{entry("tenant-q", nil)}}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs?tenant_id=tenant-q", nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if repo.gotTenantID != "tenant-q" {
			t.Errorf("tenantID = %q, want tenant-q", repo.gotTenantID)
		}
	})

	t.Run("falls back to tenant from context", func(t *testing.T) {
		repo := &fakeRepo{byTenant: []*domain.AuditLogEntry{entry("ctx-tenant", nil)}}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs", func(ctx context.Context) context.Context {
			return postgres.WithTenantID(ctx, "ctx-tenant")
		})

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if repo.gotTenantID != "ctx-tenant" {
			t.Errorf("tenantID = %q, want ctx-tenant", repo.gotTenantID)
		}
	})

	t.Run("repo error yields 500", func(t *testing.T) {
		repo := &fakeRepo{recentErr: errors.New("boom")}
		rec := doRequest(newHandler(repo).ListLogs, "/audit/logs", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
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

func TestRegisterRoutes(t *testing.T) {
	repo := &fakeRepo{recent: []*domain.AuditLogEntry{entry("t", nil)}}
	r := gin.New()
	RegisterRoutes(r.Group("/api/v1"), newHandler(repo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/audit/logs status = %d, want 200", rec.Code)
	}
}
