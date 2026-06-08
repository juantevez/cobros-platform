// cmd/api arranca el servidor HTTP de la plataforma.
//
// Este archivo es el punto de ensamblaje (composition root): instancia las
// dependencias concretas y las inyecta en los casos de uso. Ninguna otra
// capa conoce las implementaciones concretas; solo conoce interfaces.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/juantevez/cobros-platform/context/auth/application"
	authttp "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/inbound/http"
	authcrypto "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/crypto"
	authevents "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/events"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	authtoken "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/token"
	ledgerapp "github.com/juantevez/cobros-platform/context/ledger/application"
	ledgerhttp "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/inbound/http"
	ledgerevents "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/events"
	ledgerpg "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/pkg/config"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
	"github.com/juantevez/cobros-platform/pkg/outbox"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── PostgreSQL ────────────────────────────────────────────────────────────

	pgCfg := postgres.DefaultConfig(cfg.DatabaseURL)
	pgCfg.MaxConns = cfg.DBMaxConns
	pgCfg.MinConns = cfg.DBMinConns

	pool, err := postgres.New(ctx, pgCfg)
	if err != nil {
		logger.Error("postgres: connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("postgres: connected")

	// ── NATS JetStream ────────────────────────────────────────────────────────

	natsClient, err := eventbus.New(eventbus.DefaultConfig(cfg.NatsURL))
	if err != nil {
		logger.Error("nats: connection failed", "error", err)
		os.Exit(1)
	}
	defer natsClient.Close()
	logger.Info("nats: connected")

	if err := eventbus.EnsureStreams(ctx, natsClient, eventbus.AppStreams()); err != nil {
		logger.Error("nats: ensure streams failed", "error", err)
		os.Exit(1)
	}

	// ── Infraestructura compartida ────────────────────────────────────────────

	txManager := postgres.NewTxManager(pool)
	outboxStore := outbox.NewPostgresStore(pool)

	// ── Auth: implementaciones concretas ──────────────────────────────────────

	hasher := authcrypto.NewArgon2Hasher()

	jwtIssuer, err := authtoken.NewJWTIssuer(cfg.JWTSecret)
	if err != nil {
		logger.Error("jwt: invalid config", "error", err)
		os.Exit(1)
	}

	eventPublisher := authevents.NewEventPublisher(outboxStore)

	// Repositorios
	tenantRepo := authpg.NewTenantRepository(pool)
	userRepo := authpg.NewUserRepository(pool)
	membershipRepo := authpg.NewMembershipRepository(pool)
	apiKeyRepo := authpg.NewApiKeyRepository(pool)
	refreshRepo := authpg.NewRefreshTokenRepository(pool)

	// ── Auth: casos de uso ────────────────────────────────────────────────────

	clock := realClock{}

	registerTenant := application.NewRegisterTenantUseCase(tenantRepo, txManager, eventPublisher)
	activateTenant := application.NewActivateTenantUseCase(tenantRepo, txManager, eventPublisher)
	suspendTenant := application.NewSuspendTenantUseCase(tenantRepo, txManager, eventPublisher)

	registerUser := application.NewRegisterUserUseCase(
		tenantRepo, userRepo, membershipRepo, hasher, txManager, eventPublisher,
	)
	authenticate := application.NewAuthenticateUseCase(
		tenantRepo, userRepo, membershipRepo, refreshRepo, hasher, jwtIssuer, clock,
	)
	refreshTokenUC := application.NewRefreshTokenUseCase(
		userRepo, membershipRepo, tenantRepo, refreshRepo, hasher, jwtIssuer, clock,
	)
	logoutUC := application.NewLogoutUseCase(refreshRepo, hasher)

	issueApiKey := application.NewIssueApiKeyUseCase(
		tenantRepo, apiKeyRepo, hasher, txManager, eventPublisher,
	)
	revokeApiKey := application.NewRevokeApiKeyUseCase(apiKeyRepo, txManager, eventPublisher)
	assignRole := application.NewAssignRoleUseCase(
		tenantRepo, userRepo, membershipRepo, txManager, eventPublisher,
	)

	// ── Auth: handlers HTTP ───────────────────────────────────────────────────

	tenantHandler := authttp.NewTenantHandler(registerTenant, activateTenant, suspendTenant)
	authHandler := authttp.NewAuthHandler(authenticate, refreshTokenUC, logoutUC)
	userHandler := authttp.NewUserHandler(registerUser, assignRole)
	apiKeyHandler := authttp.NewApiKeyHandler(issueApiKey, revokeApiKey)

	// ── Router base (Auth) ────────────────────────────────────────────────────

	router := authttp.NewRouter(
		jwtIssuer,
		apiKeyRepo,
		hasher,
		tenantHandler,
		authHandler,
		userHandler,
		apiKeyHandler,
	)

	// ── Ledger: repositorios y casos de uso ───────────────────────────────────

	ledgerEventPub := ledgerevents.NewEventPublisher(outboxStore)
	accountRepo := ledgerpg.NewAccountRepository(pool)
	entryRepo := ledgerpg.NewEntryRepository(pool)
	balanceRepo := ledgerpg.NewBalanceRepository(pool)

	createAccount := ledgerapp.NewCreateAccountUseCase(accountRepo, txManager, ledgerEventPub)
	postEntry := ledgerapp.NewPostEntryUseCase(entryRepo, balanceRepo, txManager, ledgerEventPub, ledgerapp.RealClock())
	reverseEntry := ledgerapp.NewReverseEntryUseCase(entryRepo, balanceRepo, txManager, ledgerEventPub)
	getBalance := ledgerapp.NewGetBalanceUseCase(accountRepo, balanceRepo)

	// ── Ledger: handlers HTTP (se registran en el grupo protegido) ────────────

	accountHandler := ledgerhttp.NewAccountHandler(createAccount, getBalance)
	entryHandler := ledgerhttp.NewEntryHandler(postEntry, reverseEntry)

	// Registrar rutas del Ledger en el grupo /api/v1 protegido por JWT.
	protected := router.Group("/api/v1")
	protected.Use(authttp.JWTMiddleware(jwtIssuer))
	ledgerhttp.RegisterRoutes(protected, accountHandler, entryHandler)

	// ── HTTP Server ───────────────────────────────────────────────────────────

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeoutSec) * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
			cancel()
		}
	}()

	// Bloquear hasta señal de apagado o error crítico.
	<-quit
	logger.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}

	logger.Info("server stopped")
}

// realClock implementa application.Clock con time.Now() real.
// En tests se usa un mock que retorna tiempo fijo.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
