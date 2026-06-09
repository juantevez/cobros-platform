// cmd/worker arranca el relay del Outbox + todos los consumers de NATS.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	auditapp "github.com/juantevez/cobros-platform/context/audit/application"
	auditnats "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/inbound/nats"
	auditcrypto "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/crypto"
	auditpg "github.com/juantevez/cobros-platform/context/audit/infrastructure/adapters/outbound/postgres"
	authapp "github.com/juantevez/cobros-platform/context/auth/application"
	authdomain "github.com/juantevez/cobros-platform/context/auth/domain"
	authnats "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/inbound/nats"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	ledgerapp "github.com/juantevez/cobros-platform/context/ledger/application"
	ledgerdomain "github.com/juantevez/cobros-platform/context/ledger/domain"
	ledgernats "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/inbound/nats"
	ledgerpg "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/postgres"
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

	// ── Infraestructura ───────────────────────────────────────────────────────

	pgCfg := postgres.DefaultConfig(cfg.DatabaseURL)
	pgCfg.MaxConns = cfg.DBMaxConns
	pgCfg.MinConns = cfg.DBMinConns

	pool, err := postgres.New(ctx, pgCfg)
	if err != nil {
		logger.Error("postgres: connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

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

	// ── Outbox relay ──────────────────────────────────────────────────────────

	outboxStore := outbox.NewPostgresStore(pool)
	relay := outbox.NewRelay(
		outboxStore,
		eventbus.NewPublisher(natsClient),
		outbox.WithInterval(cfg.OutboxInterval),
		outbox.WithBatchSize(cfg.OutboxBatchSize),
		outbox.WithLogger(logger.With("component", "outbox_relay")),
	)

	// ── Publishers tipados (usados por consumers que también producen eventos) ─

	authPub   := outbox.NewEventPublisher[authdomain.Event](outboxStore)
	ledgerPub := outbox.NewEventPublisher[ledgerdomain.Event](outboxStore)

	// ── Audit consumers ───────────────────────────────────────────────────────

	auditRepo    := auditpg.NewAuditLogRepository(pool)
	recordAction := auditapp.NewRecordActionUseCase(auditRepo, auditcrypto.NewSHA256Hasher(), realClock{})
	auditConsumer := auditnats.NewEventConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "audit")),
		recordAction,
		logger.With("component", "audit"),
	)

	// ── Auth consumer: activa tenant al aprobarse el KYC ─────────────────────

	tenantRepo     := authpg.NewTenantRepository(pool)
	activateTenant := authapp.NewActivateTenantUseCase(tenantRepo, postgres.NewTxManager(pool), authPub)
	authOnboardingConsumer := authnats.NewOnboardingConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "auth_onboarding")),
		activateTenant,
		logger.With("component", "auth_onboarding"),
	)

	// ── Ledger consumers ──────────────────────────────────────────────────────

	accountRepo := ledgerpg.NewAccountRepository(pool)
	entryRepo   := ledgerpg.NewEntryRepository(pool)
	balanceRepo := ledgerpg.NewBalanceRepository(pool)
	txMgr       := postgres.NewTxManager(pool)

	createAccount := ledgerapp.NewCreateAccountUseCase(accountRepo, txMgr, ledgerPub)
	postEntry     := ledgerapp.NewPostEntryUseCase(entryRepo, balanceRepo, txMgr, ledgerPub, ledgerapp.RealClock())

	ledgerOnboardingConsumer := ledgernats.NewOnboardingConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "ledger_onboarding")),
		createAccount,
		logger.With("component", "ledger_onboarding"),
	)
	ledgerPaymentConsumer := ledgernats.NewPaymentConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "ledger_payment")),
		postEntry,
		accountRepo,
		logger.With("component", "ledger_payment"),
	)

	// ── Arrancar goroutines ───────────────────────────────────────────────────

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := relay.Start(ctx); err != nil {
			logger.Error("relay stopped", "error", err); cancel()
		}
	}()
	go func() {
		if err := auditConsumer.StartAuthConsumer(ctx); err != nil {
			logger.Error("audit auth consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := auditConsumer.StartLedgerConsumer(ctx); err != nil {
			logger.Error("audit ledger consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := authOnboardingConsumer.Start(ctx); err != nil {
			logger.Error("auth onboarding consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := ledgerOnboardingConsumer.Start(ctx); err != nil {
			logger.Error("ledger onboarding consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := ledgerPaymentConsumer.Start(ctx); err != nil {
			logger.Error("ledger payment consumer stopped", "error", err)
		}
	}()

	logger.Info("worker started",
		"outbox_interval", cfg.OutboxInterval,
		"outbox_batch_size", cfg.OutboxBatchSize,
	)
	<-quit
	logger.Info("worker stopping...")
	cancel()
	logger.Info("worker stopped")
}

type realClock struct{}
func (realClock) Now() time.Time { return time.Now().UTC() }
