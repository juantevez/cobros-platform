package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

// runMW arma un engine con el middleware dado y un handler final que responde 200.
// Devuelve el recorder y si el handler final llegó a ejecutarse.
func runMW(mw gin.HandlerFunc, setup func(*http.Request)) (*httptest.ResponseRecorder, *bool) {
	reached := false
	r := gin.New()
	r.Use(mw)
	r.GET("/probe", func(c *gin.Context) {
		reached = true
		// Exponer datos del contexto para verificar propagación.
		if claims, ok := ClaimsFromContext(c); ok {
			c.JSON(http.StatusOK, gin.H{"tenant": claims.TenantID.String(), "role": claims.Role.String()})
			return
		}
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if setup != nil {
		setup(req)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec, &reached
}

func TestJWTMiddleware(t *testing.T) {
	claims := application.AccessTokenClaims{
		UserID: domain.NewUserID(), TenantID: domain.NewTenantID(),
		Role: domain.RoleAdmin, Environment: domain.EnvironmentProduction,
	}
	issuer := &fakeTokenIssuer{claimsByToken: map[string]application.AccessTokenClaims{"good": claims}}
	mw := JWTMiddleware(issuer)

	t.Run("missing header", func(t *testing.T) {
		rec, reached := runMW(mw, nil)
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401 abort, got %d reached=%v", rec.Code, *reached)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("Authorization", "Basic xyz") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401 abort, got %d", rec.Code)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer bad") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401 abort, got %d", rec.Code)
		}
	})

	t.Run("valid token sets claims and tenant context", func(t *testing.T) {
		var ctxTenant string
		r := gin.New()
		r.Use(mw)
		r.GET("/probe", func(c *gin.Context) {
			ctxTenant, _ = postgres.TenantIDFromContext(c.Request.Context())
			got, ok := ClaimsFromContext(c)
			if !ok || got.TenantID != claims.TenantID {
				t.Error("claims not set in gin context")
			}
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set("Authorization", "Bearer good")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if ctxTenant != claims.TenantID.String() {
			t.Errorf("tenant in context = %q, want %q", ctxTenant, claims.TenantID)
		}
	})
}

func TestApiKeyMiddleware(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID) // prefix Xk3mPQrS, hash "hash:s3cr3t"
	repo := newFakeApiKeyRepo(key)

	t.Run("no header passes through", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{})
		rec, reached := runMW(mw, nil)
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected pass-through 200, got %d reached=%v", rec.Code, *reached)
		}
	})

	t.Run("malformed key aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{})
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("X-Api-Key", "garbage") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("unknown prefix aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: true})
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("X-Api-Key", "production_UNKNOWN0_secret") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("revoked key aborts 401", func(t *testing.T) {
		revoked := newApiKey(t, tenantID)
		_ = revoked.Revoke()
		revoked.PullEvents()
		mw := ApiKeyMiddleware(newFakeApiKeyRepo(revoked), &fakeHasher{verifyResult: true})
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+revoked.Prefix()+"_s3cr3t")
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("wrong secret aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: false})
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_wrong")
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("valid key sets tenant and passes", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: true})
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_s3cr3t")
		})
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected 200 pass, got %d reached=%v", rec.Code, *reached)
		}
	})
}

func TestRequireRole(t *testing.T) {
	setClaims := func(role domain.Role) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set(claimsKey, application.AccessTokenClaims{Role: role, TenantID: domain.NewTenantID()})
			c.Next()
		}
	}

	t.Run("no claims aborts 401", func(t *testing.T) {
		rec, reached := runMW(RequireRole(domain.RoleAdmin), nil)
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("wrong role aborts 403", func(t *testing.T) {
		r := gin.New()
		r.Use(setClaims(domain.RoleOperator), RequireRole(domain.RoleAdmin, domain.RolePlatformSupport))
		reached := false
		r.GET("/probe", func(c *gin.Context) { reached = true; c.Status(200) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
		if rec.Code != http.StatusForbidden || reached {
			t.Fatalf("expected 403, got %d reached=%v", rec.Code, reached)
		}
	})

	t.Run("matching role passes", func(t *testing.T) {
		r := gin.New()
		r.Use(setClaims(domain.RolePlatformSupport), RequireRole(domain.RoleAdmin, domain.RolePlatformSupport))
		reached := false
		r.GET("/probe", func(c *gin.Context) { reached = true; c.Status(200) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
		if rec.Code != http.StatusOK || !reached {
			t.Fatalf("expected 200 pass, got %d reached=%v", rec.Code, reached)
		}
	})
}

func TestCorrelationIDMiddleware(t *testing.T) {
	t.Run("generates when absent", func(t *testing.T) {
		rec, _ := runMW(CorrelationIDMiddleware(), nil)
		if rec.Header().Get("X-Correlation-ID") == "" {
			t.Error("expected a generated correlation id header")
		}
	})

	t.Run("echoes client-provided id", func(t *testing.T) {
		rec, _ := runMW(CorrelationIDMiddleware(), func(r *http.Request) {
			r.Header.Set("X-Correlation-ID", "corr-123")
		})
		if got := rec.Header().Get("X-Correlation-ID"); got != "corr-123" {
			t.Errorf("correlation id = %q, want corr-123", got)
		}
	})
}

func TestClaimsFromContext_Absent(t *testing.T) {
	c, _ := newTestCtx()
	if _, ok := ClaimsFromContext(c); ok {
		t.Error("expected no claims in a fresh context")
	}
}
