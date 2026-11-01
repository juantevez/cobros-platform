package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/auth/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// rlsProbeRole es un rol sin SUPERUSER ni BYPASSRLS: con él las políticas RLS
// de users y memberships se aplican de verdad.
const rlsProbeRole = "auth_rls_probe"

func requireRLSPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := requireDB(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+rlsProbeRole+`') THEN
				CREATE ROLE `+rlsProbeRole+` NOLOGIN NOSUPERUSER NOBYPASSRLS;
			END IF;
		END $$;
		GRANT SELECT, INSERT, UPDATE ON users, memberships TO `+rlsProbeRole); err != nil {
		t.Skipf("no se pudo preparar el rol RLS de prueba: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(testPool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+rlsProbeRole)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("rls pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func asTenant(id domain.TenantID) context.Context {
	return pkgpostgres.WithTenantID(context.Background(), id.String())
}

// Con un rol sin BYPASSRLS: el login (sin tenant en el contexto) encuentra al
// usuario y su rol porque el repo fija la sesión con el tenant pedido, y
// nadie ve usuarios de otro tenant.
func TestRLS_UsersAndMemberships(t *testing.T) {
	rls := requireRLSPool(t)
	admin := requireDB(t)
	a, b := seedTenant(t, admin), seedTenant(t, admin)

	users, members := NewUserRepository(rls), NewMembershipRepository(rls)
	email, _ := domain.NewEmail("owner@rls.test")
	u, _ := domain.NewUser(domain.NewUserID(), a.ID(), email, "argon2:hash")
	ctx := context.Background() // como el login y el alta de tenant: sin JWT
	if err := users.Save(ctx, u); err != nil {
		t.Fatalf("save user without caller tenant: %v", err)
	}
	if err := members.Save(ctx, domain.NewMembership(u.ID(), a.ID(), domain.RoleAdmin, "")); err != nil {
		t.Fatalf("save membership: %v", err)
	}

	if got, err := users.FindByEmail(ctx, a.ID(), email); err != nil || got.ID() != u.ID() {
		t.Fatalf("FindByEmail = %v, %v", got, err)
	}
	if m, err := members.FindByUserAndTenant(ctx, u.ID(), a.ID()); err != nil || m.Role() != domain.RoleAdmin {
		t.Fatalf("FindByUserAndTenant = %v, %v", m, err)
	}
	if got, err := users.FindByID(asTenant(a.ID()), u.ID()); err != nil || got.ID() != u.ID() {
		t.Fatalf("FindByID as owner = %v, %v", got, err)
	}

	for name, c := range map[string]context.Context{"other tenant": asTenant(b.ID()), "no tenant": context.Background()} {
		if _, err := users.FindByID(c, u.ID()); !errors.Is(err, domain.ErrUserNotFound) {
			t.Errorf("FindByID %s = %v, want ErrUserNotFound", name, err)
		}
	}
	if _, err := users.FindByEmail(ctx, b.ID(), email); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("FindByEmail in other tenant = %v", err)
	}
	if list, err := members.ListByTenant(ctx, b.ID()); err != nil || len(list) != 0 {
		t.Errorf("ListByTenant(b) = %v, %v", list, err)
	}

	// Una escritura con el tenant de otro en la sesión no pasa la política.
	other, _ := domain.NewUser(domain.NewUserID(), a.ID(), email, "argon2:hash")
	err := pkgpostgres.NewTxManager(rls).RunInTx(asTenant(b.ID()), func(ctx context.Context) error {
		return users.Save(ctx, other)
	})
	if err == nil {
		t.Error("insert of tenant A's user under tenant B's session must fail")
	}
}
