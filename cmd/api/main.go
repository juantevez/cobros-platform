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
	authdomain "github.com/juantevez/cobros-platform/context/auth/domain"
	authttp "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/inbound/http"
	authcrypto "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/crypto"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	authtoken "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/token"
	auditapp "github.com/juantevez/cobros-platform/context/audit/application"
	audithttp "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/inbound/http"
	auditcrypto "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/crypto"
	auditpg "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/postgres"
	ledgerapp "github.com/juantevez/cobros-platform/context/ledger/application"
	ledgerdomain "github.com/juantevez/cobros-platform/context/ledger/domain"
	ledgerhttp "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/inbound/http"
	ledgerpg "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/postgres"
	onboardingapp "github.com/juantevez/cobros-platform/context/onboarding/application"
	onboardingdomain "github.com/juantevez/cobros-platform/context/onboarding/domain"
	onboardinghttp "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/inbound/http"
	onboardingpg "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/outbound/postgres"
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

	// ── Publishers por contexto ───────────────────────────────────────────────
	// Go no permite covarianza en variadics, por lo que cada contexto necesita
	// su propio EventPublisher tipado sobre su domain.Event.
	authEventPublisher := outbox.NewEventPublisher[authdomain.Event](outboxStore)
	ledgerEventPublisher := outbox.NewEventPublisher[ledgerdomain.Event](outboxStore)
	onboardingEventPublisher := outbox.NewEventPublisher[onboardingdomain.Event](outboxStore)

	// Repositorios
	tenantRepo := authpg.NewTenantRepository(pool)
	userRepo := authpg.NewUserRepository(pool)
	membershipRepo := authpg.NewMembershipRepository(pool)
	apiKeyRepo := authpg.NewApiKeyRepository(pool)
	refreshRepo := authpg.NewRefreshTokenRepository(pool)

	// ── Auth: casos de uso ────────────────────────────────────────────────────

	clock := realClock{}

	registerTenant := application.NewRegisterTenantUseCase(tenantRepo, txManager, authEventPublisher)
	activateTenant := application.NewActivateTenantUseCase(tenantRepo, txManager, authEventPublisher)
	suspendTenant := application.NewSuspendTenantUseCase(tenantRepo, txManager, authEventPublisher)

	registerUser := application.NewRegisterUserUseCase(
		tenantRepo, userRepo, membershipRepo, hasher, txManager, authEventPublisher,
	)
	authenticate := application.NewAuthenticateUseCase(
		tenantRepo, userRepo, membershipRepo, refreshRepo, hasher, jwtIssuer, clock,
	)
	refreshTokenUC := application.NewRefreshTokenUseCase(
		userRepo, membershipRepo, tenantRepo, refreshRepo, hasher, jwtIssuer, clock,
	)
	logoutUC := application.NewLogoutUseCase(refreshRepo, hasher)

	issueApiKey := application.NewIssueApiKeyUseCase(
		tenantRepo, apiKeyRepo, hasher, txManager, authEventPublisher,
	)
	revokeApiKey := application.NewRevokeApiKeyUseCase(apiKeyRepo, txManager, authEventPublisher)
	assignRole := application.NewAssignRoleUseCase(
		tenantRepo, userRepo, membershipRepo, txManager, authEventPublisher,
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

	accountRepo := ledgerpg.NewAccountRepository(pool)
	entryRepo := ledgerpg.NewEntryRepository(pool)
	balanceRepo := ledgerpg.NewBalanceRepository(pool)

	createAccount := ledgerapp.NewCreateAccountUseCase(accountRepo, txManager, ledgerEventPublisher)
	postEntry := ledgerapp.NewPostEntryUseCase(entryRepo, balanceRepo, txManager, ledgerEventPublisher, ledgerapp.RealClock())
	reverseEntry := ledgerapp.NewReverseEntryUseCase(entryRepo, balanceRepo, txManager, ledgerEventPublisher)
	getBalance := ledgerapp.NewGetBalanceUseCase(accountRepo, balanceRepo)

	// ── Ledger: handlers HTTP (se registran en el grupo protegido) ────────────

	accountHandler := ledgerhttp.NewAccountHandler(createAccount, getBalance)
	entryHandler := ledgerhttp.NewEntryHandler(postEntry, reverseEntry)

	// Registrar rutas del Ledger en el grupo /api/v1 protegido por JWT.
	protected := router.Group("/api/v1")
	protected.Use(authttp.JWTMiddleware(jwtIssuer))
	ledgerhttp.RegisterRoutes(protected, accountHandler, entryHandler)

	// ── Audit: handlers HTTP ──────────────────────────────────────────────────

	auditRepo := auditpg.NewAuditLogRepository(pool)
	auditHasher := auditcrypto.NewSHA256Hasher()
	listLogs := auditapp.NewListLogsUseCase(auditRepo)
	verifyChain := auditapp.NewVerifyChainUseCase(auditRepo, auditHasher)
	auditHandler := audithttp.NewAuditHandler(listLogs, verifyChain)
	audithttp.RegisterRoutes(protected, auditHandler)

	// ── Onboarding ────────────────────────────────────────────────────────────

	appRepo := onboardingpg.NewApplicationRepository(pool)

	submitApp := onboardingapp.NewSubmitApplicationUseCase(appRepo, txManager, onboardingEventPublisher)
	uploadDoc := onboardingapp.NewUploadDocumentUseCase(appRepo, txManager)
	addPerson := onboardingapp.NewAddPersonUseCase(appRepo, txManager)
	setBankAcct := onboardingapp.NewSetBankAccountUseCase(appRepo, txManager)
	submitForReview := onboardingapp.NewSubmitForReviewUseCase(appRepo, txManager, onboardingEventPublisher)
	reviewApp := onboardingapp.NewReviewApplicationUseCase(appRepo, txManager, onboardingEventPublisher)
	getApp := onboardingapp.NewGetApplicationUseCase(appRepo)

	obHandler := onboardinghttp.NewOnboardingHandler(
		submitApp, uploadDoc, addPerson, setBankAcct, submitForReview, getApp,
	)
	reviewHandler := onboardinghttp.NewReviewHandler(reviewApp)
	onboardinghttp.RegisterRoutes(protected, obHandler, reviewHandler)

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
