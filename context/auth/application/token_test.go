package application

import (
	"testing"
	"time"
)

func TestRefreshToken_IsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"future expiry", now.Add(time.Hour), false},
		{"past expiry", now.Add(-time.Hour), true},
		{"exactly now is not expired", now, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &RefreshToken{ExpiresAt: tt.expiresAt}
			if got := rt.IsExpired(now); got != tt.want {
				t.Errorf("IsExpired = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRefreshToken_IsRevoked(t *testing.T) {
	if (&RefreshToken{}).IsRevoked() {
		t.Error("token with nil RevokedAt should not be revoked")
	}
	revoked := time.Now()
	if !(&RefreshToken{RevokedAt: &revoked}).IsRevoked() {
		t.Error("token with RevokedAt set should be revoked")
	}
}

func TestRefreshToken_IsValid(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	revokedAt := now.Add(-time.Minute)

	tests := []struct {
		name  string
		token RefreshToken
		want  bool
	}{
		{"active and unexpired", RefreshToken{ExpiresAt: now.Add(time.Hour)}, true},
		{"expired", RefreshToken{ExpiresAt: now.Add(-time.Hour)}, false},
		{"revoked", RefreshToken{ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt}, false},
		{"revoked and expired", RefreshToken{ExpiresAt: now.Add(-time.Hour), RevokedAt: &revokedAt}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.IsValid(now); got != tt.want {
				t.Errorf("IsValid = %v, want %v", got, tt.want)
			}
		})
	}
}
