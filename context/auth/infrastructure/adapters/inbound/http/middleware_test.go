package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	mw := JWTMiddleware(issuer, openTenants)

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
		mw := ApiKeyMiddleware(repo, &fakeHasher{}, openTenants)
		rec, reached := runMW(mw, nil)
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected pass-through 200, got %d reached=%v", rec.Code, *reached)
		}
	})

	t.Run("malformed key aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("X-Api-Key", "garbage") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("unknown prefix aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: true}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("X-Api-Key", "production_UNKNOWN0_secret") })
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("revoked key aborts 401", func(t *testing.T) {
		revoked := newApiKey(t, tenantID)
		_ = revoked.Revoke()
		revoked.PullEvents()
		mw := ApiKeyMiddleware(newFakeApiKeyRepo(revoked), &fakeHasher{verifyResult: true}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+revoked.Prefix()+"_s3cr3t")
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("wrong secret aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: false}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_wrong")
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("environment mismatch aborts 401", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: true}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "test_"+key.Prefix()+"_s3cr3t") // la key es production
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("valid key sets tenant and passes", func(t *testing.T) {
		mw := ApiKeyMiddleware(repo, &fakeHasher{verifyResult: true}, openTenants)
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_s3cr3t")
		})
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected 200 pass, got %d reached=%v", rec.Code, *reached)
		}
	})
}

// Una suspensión corta las credenciales vigentes: antes el JWT seguía
// valiendo hasta 15 minutos y la API key sin límite.
func TestMiddlewares_RejectSuspendedTenant(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID)
	claims := application.AccessTokenClaims{UserID: domain.NewUserID(), TenantID: tenantID, Role: domain.RoleAdmin}
	issuer := &fakeTokenIssuer{claimsByToken: map[string]application.AccessTokenClaims{"good": claims}}
	withKey := func(r *http.Request) { r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_s3cr3t") }
	withJWT := func(r *http.Request) { r.Header.Set("Authorization", "Bearer good") }

	cases := []struct {
		name   string
		status fakeTenantStatus
		want   int
	}{
		{"suspended", fakeTenantStatus{err: domain.ErrTenantSuspended}, http.StatusForbidden},
		{"deleted", fakeTenantStatus{err: domain.ErrTenantNotFound}, http.StatusUnauthorized},
		{"lookup error", fakeTenantStatus{err: errors.New("db down")}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		mws := map[string]struct {
			mw    gin.HandlerFunc
			setup func(*http.Request)
		}{
			"jwt":     {JWTMiddleware(issuer, tc.status), withJWT},
			"api key": {ApiKeyMiddleware(newFakeApiKeyRepo(key), &fakeHasher{verifyResult: true}, tc.status), withKey},
		}
		for name, m := range mws {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				rec, reached := runMW(m.mw, m.setup)
				if rec.Code != tc.want || *reached {
					t.Fatalf("status = %d reached=%v, want %d", rec.Code, *reached, tc.want)
				}
			})
		}
	}
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

// ── API key: claims de integración ────────────────────────────────────────────

func TestApiKeyMiddleware_SetsIntegrationClaimsAndActor(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID)
	mw := ApiKeyMiddleware(newFakeApiKeyRepo(key), &fakeHasher{verifyResult: true}, openTenants)

	var claims application.AccessTokenClaims
	var actor string
	r := gin.New()
	r.Use(mw)
	r.GET("/probe", func(c *gin.Context) {
		claims, _ = ClaimsFromContext(c)
		actor = postgres.ActorFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_s3cr3t")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !claims.IsApiKey() || claims.ApiKeyID != key.ID() || claims.TenantID != tenantID {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != domain.ScopePaymentsRead {
		t.Errorf("scopes = %v, want [payments:read]", claims.Scopes)
	}
	if actor != "apikey:"+key.ID().String() {
		t.Errorf("actor = %q", actor)
	}
}

// ── JWTOrApiKeyMiddleware ─────────────────────────────────────────────────────

func TestJWTOrApiKeyMiddleware(t *testing.T) {
	tenantID := domain.NewTenantID()
	key := newApiKey(t, tenantID)
	jwtClaims := application.AccessTokenClaims{
		UserID: domain.NewUserID(), TenantID: domain.NewTenantID(),
		Role: domain.RoleOperator, Environment: domain.EnvironmentProduction,
	}
	issuer := &fakeTokenIssuer{claimsByToken: map[string]application.AccessTokenClaims{"good": jwtClaims}}
	mw := JWTOrApiKeyMiddleware(issuer, newFakeApiKeyRepo(key), &fakeHasher{verifyResult: true}, openTenants)

	t.Run("api key header authenticates with the key", func(t *testing.T) {
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "production_"+key.Prefix()+"_s3cr3t")
		})
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tenantID.String()) {
			t.Errorf("tenant of the api key not set: %s", rec.Body.String())
		}
	})

	t.Run("bearer token authenticates with JWT", func(t *testing.T) {
		rec, reached := runMW(mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer good") })
		if rec.Code != http.StatusOK || !*reached {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), jwtClaims.TenantID.String()) {
			t.Errorf("tenant of the JWT not set: %s", rec.Body.String())
		}
	})

	t.Run("no credentials aborts 401", func(t *testing.T) {
		rec, reached := runMW(mw, nil)
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d reached=%v", rec.Code, *reached)
		}
	})

	t.Run("invalid api key aborts 401 without falling back to JWT", func(t *testing.T) {
		rec, reached := runMW(mw, func(r *http.Request) {
			r.Header.Set("X-Api-Key", "garbage")
			r.Header.Set("Authorization", "Bearer good")
		})
		if rec.Code != http.StatusUnauthorized || *reached {
			t.Fatalf("expected 401, got %d reached=%v", rec.Code, *reached)
		}
	})
}

// ── RequirePermission ─────────────────────────────────────────────────────────

func TestRequirePermission(t *testing.T) {
	run := func(claims *application.AccessTokenClaims, mw gin.HandlerFunc) (int, bool) {
		r := gin.New()
		if claims != nil {
			r.Use(func(c *gin.Context) { c.Set(claimsKey, *claims); c.Next() })
		}
		r.Use(mw)
		reached := false
		r.GET("/probe", func(c *gin.Context) { reached = true; c.Status(http.StatusOK) })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
		return rec.Code, reached
	}
	user := func(role domain.Role) *application.AccessTokenClaims {
		return &application.AccessTokenClaims{UserID: domain.NewUserID(), TenantID: domain.NewTenantID(), Role: role}
	}
	apiKey := func(scopes ...domain.Scope) *application.AccessTokenClaims {
		return &application.AccessTokenClaims{TenantID: domain.NewTenantID(), ApiKeyID: domain.NewApiKeyID(), Scopes: scopes}
	}
	createPayment := RequirePermission(domain.ActionCreatePayment, domain.ScopePaymentsWrite)

	cases := []struct {
		name   string
		claims *application.AccessTokenClaims
		mw     gin.HandlerFunc
		want   int
	}{
		{"no claims → 401", nil, createPayment, http.StatusUnauthorized},
		{"operator can create payments", user(domain.RoleOperator), createPayment, http.StatusOK},
		{"read_only cannot create payments", user(domain.RoleReadOnly), createPayment, http.StatusForbidden},
		{"operator cannot refund", user(domain.RoleOperator), RequirePermission(domain.ActionRefundPayment, domain.ScopePaymentsWrite), http.StatusForbidden},
		{"admin can refund", user(domain.RoleAdmin), RequirePermission(domain.ActionRefundPayment, domain.ScopePaymentsWrite), http.StatusOK},
		{"key with write scope can create", apiKey(domain.ScopePaymentsWrite), createPayment, http.StatusOK},
		{"key with read scope cannot create", apiKey(domain.ScopePaymentsRead), createPayment, http.StatusForbidden},
		{"key accepted by any of the listed scopes", apiKey(domain.ScopePaymentsWrite),
			RequirePermission(domain.ActionReadPayments, domain.ScopePaymentsRead, domain.ScopePaymentsWrite), http.StatusOK},
		{"key rejected on user-only route", apiKey(domain.ScopePaymentsWrite), RequirePermission(domain.ActionManageUsers), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, reached := run(tc.claims, tc.mw)
			if code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
			if reached != (tc.want == http.StatusOK) {
				t.Errorf("handler reached = %v", reached)
			}
		})
	}
}
