package token

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 chars

func newIssuer(t *testing.T) *JWTIssuer {
	t.Helper()
	iss, err := NewJWTIssuer(testSecret)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	return iss
}

func sampleClaims() application.AccessTokenClaims {
	return application.AccessTokenClaims{
		UserID:      domain.UserID(uuid.NewString()),
		TenantID:    domain.TenantID(uuid.NewString()),
		Role:        domain.RoleAdmin,
		Environment: domain.EnvironmentProduction,
	}
}

// signWith firma unos jwtClaims con el secret dado y el método indicado.
func signWith(t *testing.T, method jwt.SigningMethod, key any, claims jwtClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, &claims)
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestNewJWTIssuer(t *testing.T) {
	t.Run("rejects short secret", func(t *testing.T) {
		if _, err := NewJWTIssuer("too-short"); err == nil {
			t.Fatal("expected error for short secret")
		}
	})
	t.Run("rejects 31 chars", func(t *testing.T) {
		if _, err := NewJWTIssuer("0123456789abcdef0123456789abcde"); err == nil {
			t.Fatal("expected error for 31-char secret")
		}
	})
	t.Run("accepts 32 chars", func(t *testing.T) {
		if _, err := NewJWTIssuer(testSecret); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestIssueAndVerify_RoundTrip(t *testing.T) {
	iss := newIssuer(t)
	claims := sampleClaims()

	tokenStr, err := iss.IssueAccessToken(claims)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("empty token")
	}

	got, err := iss.VerifyAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.UserID != claims.UserID || got.TenantID != claims.TenantID {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Role != claims.Role || got.Environment != claims.Environment {
		t.Errorf("claims mismatch: %+v", got)
	}
}

func TestVerify_Garbage(t *testing.T) {
	iss := newIssuer(t)
	if _, err := iss.VerifyAccessToken("not.a.jwt"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	issuer := newIssuer(t)
	tokenStr, _ := issuer.IssueAccessToken(sampleClaims())

	other, _ := NewJWTIssuer("ffffffffffffffffffffffffffffffff") // otro secret de 32
	if _, err := other.VerifyAccessToken(tokenStr); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	iss := newIssuer(t)
	past := time.Now().Add(-time.Hour)
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(past),
			IssuedAt:  jwt.NewNumericDate(past.Add(-time.Minute)),
			ID:        uuid.NewString(),
		},
		TenantID: uuid.NewString(),
		Role:     domain.RoleAdmin.String(),
		Env:      domain.EnvironmentProduction.String(),
	}
	expired := signWith(t, jwt.SigningMethodHS256, []byte(testSecret), claims)

	if _, err := iss.VerifyAccessToken(expired); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for expired token, got %v", err)
	}
}

func TestVerify_RejectsNoneAlgorithm(t *testing.T) {
	iss := newIssuer(t)
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			ID:        uuid.NewString(),
		},
		TenantID: uuid.NewString(),
		Role:     domain.RoleAdmin.String(),
		Env:      domain.EnvironmentProduction.String(),
	}
	// Ataque "alg=none": debe rechazarse porque el keyfunc exige HMAC.
	noneToken := signWith(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims)

	if _, err := iss.VerifyAccessToken(noneToken); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for none-alg token, got %v", err)
	}
}

func TestVerify_ValidSignatureButBadClaims(t *testing.T) {
	iss := newIssuer(t)
	base := func() jwtClaims {
		return jwtClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   uuid.NewString(),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				ID:        uuid.NewString(),
			},
			TenantID: uuid.NewString(),
			Role:     domain.RoleAdmin.String(),
			Env:      domain.EnvironmentProduction.String(),
		}
	}

	tests := []struct {
		name  string
		mutate func(*jwtClaims)
	}{
		{"non-uuid subject", func(c *jwtClaims) { c.Subject = "not-a-uuid" }},
		{"non-uuid tenant", func(c *jwtClaims) { c.TenantID = "nope" }},
		{"invalid role", func(c *jwtClaims) { c.Role = "wizard" }},
		{"invalid env", func(c *jwtClaims) { c.Env = "staging" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mutate(&c)
			tokenStr := signWith(t, jwt.SigningMethodHS256, []byte(testSecret), c)
			if _, err := iss.VerifyAccessToken(tokenStr); !errors.Is(err, domain.ErrInvalidCredentials) {
				t.Fatalf("expected ErrInvalidCredentials, got %v", err)
			}
		})
	}
}

func TestIssueRefreshToken(t *testing.T) {
	iss := newIssuer(t)

	tok, err := iss.IssueRefreshToken()
	if err != nil {
		t.Fatalf("issue refresh: %v", err)
	}
	// URL-safe base64 que decodifica a 32 bytes de entropía.
	raw, err := base64.URLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("refresh token not url-safe base64: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("entropy = %d bytes, want 32", len(raw))
	}
}

func TestIssueRefreshToken_IsRandom(t *testing.T) {
	iss := newIssuer(t)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := iss.IssueRefreshToken()
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate refresh token: %q", tok)
		}
		seen[tok] = true
	}
}
