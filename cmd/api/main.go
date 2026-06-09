// cmd/api arranca el servidor HTTP de la plataforma.
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

	auditapp "github.com/juantevez/cobros-platform/context/audit/application"
	audithttp "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/inbound/http"
	auditcrypto "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/crypto"
	auditpg "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/context/auth/application"
	authdomain "github.com/juantevez/cobros-platform/context/auth/domain"
	authttp "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/inbound/http"
	authcrypto "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/crypto"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	authtoken "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/token"
	ledgerapp "github.com/juantevez/cobros-platform/context/ledger/application"
	ledgerdomain "github.com/juantevez/cobros-platform/context/ledger/domain"
	ledgerhttp "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/inbound/http"
	ledgerpg "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/postgres"
	onboardingapp "github.com/juantevez/cobros-platform/context/onboarding/application"
	onboardingdomain "github.com/juantevez/cobros-platform/context/onboarding/domain"
	onboardinghttp "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/inbound/http"
	onboardingpg "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/outbound/postgres"
	paymentapp "github.com/juantevez/cobros-platform/context/payment/application"
	paymentdomain "github.com/juantevez/cobros-platform/context/payment/domain"
	paymenthttp "github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/inbound/http"
	paymentpg "github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/fees"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/psp"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/risk"
	"github.com/juantevez/cobros-platform/pkg/config"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
	"github.com/juantevez/cobros-platform/pkg/outbox"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
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

	// ── NATS JetStream ────────────────────────────────────────────────────────

	natsClient, err := eventbus.New(eventbus.DefaultConfig(cfg.NatsURL))
	if err != nil {
		logger.Error("nats: connection failed", "error", err)
		os.Exit(1)
	}
	defer natsClient.Close()

	if err := eventbus.EnsureStreams(ctx, natsClient, eventbus.AppStreams()); err != nil {
		logger.Error("nats: ensure streams failed", "error", err)
		os.Exit(1)
	}

	// ── Infraestructura compartida ────────────────────────────────────────────

	txManager := postgres.NewTxManager(pool)
	outboxStore := outbox.NewPostgresStore(pool)

	// ── Publishers tipados por contexto ───────────────────────────────────────
	// Go no tiene covarianza en variadics: NewEventPublisher[T] resuelve esto.
	authPub        := outbox.NewEventPublisher[authdomain.Event](outboxStore)
	ledgerPub      := outbox.NewEventPublisher[ledgerdomain.Event](outboxStore)
	onboardingPub  := outbox.NewEventPublisher[onboardingdomain.Event](outboxStore)
	paymentPub     := outbox.NewEventPublisher[paymentdomain.Event](outboxStore)

	// ── Auth ──────────────────────────────────────────────────────────────────

	hasher := authcrypto.NewArgon2Hasher()
	jwtIssuer, err := authtoken.NewJWTIssuer(cfg.JWTSecret)
	if err != nil {
		logger.Error("jwt: invalid config", "error", err)
		os.Exit(1)
	}

	tenantRepo     := authpg.NewTenantRepository(pool)
	userRepo       := authpg.NewUserRepository(pool)
	membershipRepo := authpg.NewMembershipRepository(pool)
	apiKeyRepo     := authpg.NewApiKeyRepository(pool)
	refreshRepo    := authpg.NewRefreshTokenRepository(pool)
	clock          := realClock{}

	registerTenant  := application.NewRegisterTenantUseCase(tenantRepo, txManager, authPub)
	activateTenant  := application.NewActivateTenantUseCase(tenantRepo, txManager, authPub)
	suspendTenant   := application.NewSuspendTenantUseCase(tenantRepo, txManager, authPub)
	registerUser    := application.NewRegisterUserUseCase(tenantRepo, userRepo, membershipRepo, hasher, txManager, authPub)
	authenticate    := application.NewAuthenticateUseCase(tenantRepo, userRepo, membershipRepo, refreshRepo, hasher, jwtIssuer, clock)
	refreshTokenUC  := application.NewRefreshTokenUseCase(userRepo, membershipRepo, tenantRepo, refreshRepo, hasher, jwtIssuer, clock)
	logoutUC        := application.NewLogoutUseCase(refreshRepo, hasher)
	issueApiKey     := application.NewIssueApiKeyUseCase(tenantRepo, apiKeyRepo, hasher, txManager, authPub)
	revokeApiKey    := application.NewRevokeApiKeyUseCase(apiKeyRepo, txManager, authPub)
	assignRole      := application.NewAssignRoleUseCase(tenantRepo, userRepo, membershipRepo, txManager, authPub)

	tenantHandler := authttp.NewTenantHandler(registerTenant, activateTenant, suspendTenant)
	authHandler   := authttp.NewAuthHandler(authenticate, refreshTokenUC, logoutUC)
	userHandler   := authttp.NewUserHandler(registerUser, assignRole)
	apiKeyHandler := authttp.NewApiKeyHandler(issueApiKey, revokeApiKey)

	router := authttp.NewRouter(jwtIssuer, apiKeyRepo, hasher, tenantHandler, authHandler, userHandler, apiKeyHandler)

	protected := router.Group("/api/v1")
	protected.Use(authttp.JWTMiddleware(jwtIssuer))

	// ── Ledger ────────────────────────────────────────────────────────────────

	accountRepo := ledgerpg.NewAccountRepository(pool)
	entryRepo   := ledgerpg.NewEntryRepository(pool)
	balanceRepo := ledgerpg.NewBalanceRepository(pool)

	createAccount := ledgerapp.NewCreateAccountUseCase(accountRepo, txManager, ledgerPub)
	postEntry     := ledgerapp.NewPostEntryUseCase(entryRepo, balanceRepo, txManager, ledgerPub, ledgerapp.RealClock())
	reverseEntry  := ledgerapp.NewReverseEntryUseCase(entryRepo, balanceRepo, txManager, ledgerPub)
	getBalance    := ledgerapp.NewGetBalanceUseCase(accountRepo, balanceRepo)

	ledgerhttp.RegisterRoutes(protected, ledgerhttp.NewAccountHandler(createAccount, getBalance), ledgerhttp.NewEntryHandler(postEntry, reverseEntry))

	// ── Audit ─────────────────────────────────────────────────────────────────

	auditRepo    := auditpg.NewAuditLogRepository(pool)
	auditHasher  := auditcrypto.NewSHA256Hasher()
	listLogs     := auditapp.NewListLogsUseCase(auditRepo)
	verifyChain  := auditapp.NewVerifyChainUseCase(auditRepo, auditHasher)
	audithttp.RegisterRoutes(protected, audithttp.NewAuditHandler(listLogs, verifyChain))

	// ── Onboarding ────────────────────────────────────────────────────────────

	appRepo         := onboardingpg.NewApplicationRepository(pool)
	submitApp       := onboardingapp.NewSubmitApplicationUseCase(appRepo, txManager, onboardingPub)
	uploadDoc       := onboardingapp.NewUploadDocumentUseCase(appRepo, txManager)
	addPerson       := onboardingapp.NewAddPersonUseCase(appRepo, txManager)
	setBankAcct     := onboardingapp.NewSetBankAccountUseCase(appRepo, txManager)
	submitForReview := onboardingapp.NewSubmitForReviewUseCase(appRepo, txManager, onboardingPub)
	reviewApp       := onboardingapp.NewReviewApplicationUseCase(appRepo, txManager, onboardingPub)
	getApp          := onboardingapp.NewGetApplicationUseCase(appRepo)

	onboardinghttp.RegisterRoutes(protected,
		onboardinghttp.NewOnboardingHandler(submitApp, uploadDoc, addPerson, setBankAcct, submitForReview, getApp),
		onboardinghttp.NewReviewHandler(reviewApp),
	)

	// ── Payment Processing ────────────────────────────────────────────────────

	paymentRepo    := paymentpg.NewPaymentRepository(pool)
	pspRouter      := psp.NewRouter()
	riskEvaluator  := risk.NewPermissiveEvaluator()
	feeCalculator  := fees.NewFixedRateCalculator(300) // 3% por defecto

	processPayment := paymentapp.NewProcessPaymentUseCase(paymentRepo, pspRouter, riskEvaluator, feeCalculator, txManager, paymentPub)
	refundPayment  := paymentapp.NewRefundPaymentUseCase(paymentRepo, pspRouter, txManager, paymentPub)
	getPayment     := paymentapp.NewGetPaymentUseCase(paymentRepo)

	paymenthttp.RegisterRoutes(protected, paymenthttp.NewPaymentHandler(processPayment, refundPayment, getPayment))

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

	<-quit
	logger.Info("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	logger.Info("server stopped")
}

type realClock struct{}
func (realClock) Now() time.Time { return time.Now().UTC() }
