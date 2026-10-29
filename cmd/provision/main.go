// Command provision da de alta usuarios con roles de plataforma
// (compliance_officer, platform_support), que la API no permite otorgar.
//
//	PROVISION_PASSWORD=... go run ./cmd/provision -tenant <uuid> -email ana@plataforma.com
//	echo "$PASS" | go run ./cmd/provision -tenant <uuid> -email ana@x.com -role platform_support -password-stdin
//
// El tenant es el de la propia plataforma (se crea con el registro normal de
// tenants). La contraseña nunca va por flag: quedaría en el historial del
// shell y en la lista de procesos. Usa DATABASE_URL como cmd/api.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	authapp "github.com/juantevez/cobros-platform/context/auth/application"
	authdomain "github.com/juantevez/cobros-platform/context/auth/domain"
	authcrypto "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/crypto"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/pkg/config"
	"github.com/juantevez/cobros-platform/pkg/outbox"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "provision:", err)
		os.Exit(1)
	}
}

type options struct {
	tenantID string
	email    string
	role     string
	password string
}

// parseOptions valida flags y obtiene la contraseña de stdin o del entorno.
func parseOptions(args []string, stdin io.Reader, getenv func(string) string) (options, error) {
	fs := flag.NewFlagSet("provision", flag.ContinueOnError)
	var o options
	var fromStdin bool
	fs.StringVar(&o.tenantID, "tenant", "", "UUID del tenant de la plataforma (requerido)")
	fs.StringVar(&o.email, "email", "", "email del usuario (requerido)")
	fs.StringVar(&o.role, "role", string(authdomain.RoleComplianceOfficer), "compliance_officer | platform_support")
	fs.BoolVar(&fromStdin, "password-stdin", false, "leer la contraseña de stdin (si no, PROVISION_PASSWORD)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if o.tenantID == "" || o.email == "" {
		return options{}, errors.New("-tenant y -email son requeridos")
	}
	if fromStdin {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return options{}, fmt.Errorf("leer contraseña: %w", err)
		}
		o.password = strings.TrimRight(line, "\r\n")
	} else {
		o.password = getenv("PROVISION_PASSWORD")
	}
	if o.password == "" {
		return options{}, errors.New("contraseña vacía: usar PROVISION_PASSWORD o -password-stdin")
	}
	return o, nil
}

func run(args []string, stdin io.Reader, getenv func(string) string) error {
	o, err := parseOptions(args, stdin, getenv)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cfg := config.Load()
	pool, err := postgres.New(ctx, postgres.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("conectar a postgres: %w", err)
	}
	defer pool.Close()

	uc := authapp.NewProvisionPlatformUserUseCase(
		authpg.NewTenantRepository(pool),
		authpg.NewUserRepository(pool),
		authpg.NewMembershipRepository(pool),
		authcrypto.NewArgon2Hasher(),
		postgres.NewTxManager(pool),
		outbox.NewEventPublisher[authdomain.Event](outbox.NewPostgresStore(pool)),
	)
	// El tenant en el contexto activa RLS de users/memberships en la tx.
	res, err := uc.Execute(postgres.WithTenantID(ctx, o.tenantID), authapp.RegisterUserCmd{
		TenantID: o.tenantID, Email: o.email, Password: o.password, Role: o.role,
	})
	if err != nil {
		return err
	}
	slog.Info("usuario de plataforma creado", "user_id", res.UserID, "role", o.role, "tenant_id", o.tenantID)
	return nil
}
