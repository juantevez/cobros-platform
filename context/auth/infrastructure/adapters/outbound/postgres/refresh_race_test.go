package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/context/auth/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

type stubIssuer struct{}

func (stubIssuer) IssueAccessToken(application.AccessTokenClaims) (string, error) { return "access", nil }
func (stubIssuer) IssueRefreshToken() (string, error)                            { return uuid.NewString(), nil }
func (stubIssuer) VerifyAccessToken(string) (application.AccessTokenClaims, error) {
	return application.AccessTokenClaims{}, nil
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

// Dos refresh a la vez con el mismo token: antes ambos emitían un par válido.
// Ahora gana uno solo, y el perdedor revoca todas las sesiones (incluida la
// del ganador): no queda ningún refresh token vigente.
func TestRefreshToken_ConcurrentRotation(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	tenant := seedTenant(t, pool)
	user := seedUser(t, pool, tenant.ID(), "race@example.com")
	if err := NewMembershipRepository(pool).Save(ctx, domain.NewMembership(user.ID(), tenant.ID(), domain.RoleAdmin, "")); err != nil {
		t.Fatalf("membership: %v", err)
	}

	refresh := NewRefreshTokenRepository(pool)
	raw := uuid.NewString()
	sum := sha256.Sum256([]byte(raw))
	now := time.Now().UTC()
	if err := refresh.Save(ctx, application.RefreshToken{
		ID: uuid.NewString(), UserID: user.ID(), TenantID: tenant.ID(),
		TokenHash: hex.EncodeToString(sum[:]), IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	uc := application.NewRefreshTokenUseCase(NewUserRepository(pool), NewMembershipRepository(pool),
		NewTenantRepository(pool), refresh, stubIssuer{}, wallClock{}, pkgpostgres.NewTxManager(pool))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = uc.Execute(ctx, application.RefreshTokenCmd{RawRefreshToken: raw})
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("successful refreshes = %d (%v), want exactly 1", ok, errs)
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id=$1 AND revoked_at IS NULL`,
		user.ID().String()).Scan(&active); err != nil {
		t.Fatalf("count: %v", err)
	}
	if active != 0 {
		t.Errorf("active refresh tokens = %d, want 0 (reuse revokes every session)", active)
	}
}
