// cmd/worker arranca el relay del Transactional Outbox.
//
// Responsabilidades:
//   - Lee mensajes pendientes de outbox_messages.
//   - Los publica en NATS JetStream (con deduplicación por Nats-Msg-Id).
//   - Marca cada mensaje como publicado.
//
// Puede correr en múltiples instancias (SKIP LOCKED + deduplicación de JetStream
// garantizan que no haya publicaciones duplicadas).
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
	authnats "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/inbound/nats"
	authapp "github.com/juantevez/cobros-platform/context/auth/application"
	authpg "github.com/juantevez/cobros-platform/context/auth/infrastructure/adapters/outbound/postgres"
	ledgerapp "github.com/juantevez/cobros-platform/context/ledger/application"
	ledgerpg "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/outbound/postgres"
	ledgernats "github.com/juantevez/cobros-platform/context/ledger/infrastructure/adapters/inbound/nats"
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

	// Provisionar streams antes de que el relay empiece a publicar.
	if err := eventbus.EnsureStreams(ctx, natsClient, eventbus.AppStreams()); err != nil {
		logger.Error("nats: ensure streams failed", "error", err)
		os.Exit(1)
	}

	// ── Outbox relay ──────────────────────────────────────────────────────────

	outboxStore := outbox.NewPostgresStore(pool)
	publisher := eventbus.NewPublisher(natsClient)

	relay := outbox.NewRelay(
		outboxStore,
		publisher,
		outbox.WithInterval(cfg.OutboxInterval),
		outbox.WithBatchSize(cfg.OutboxBatchSize),
		outbox.WithLogger(logger.With("component", "outbox_relay")),
	)

	// ── Audit: consumers de eventos ───────────────────────────────────────────

	auditRepo := auditpg.NewAuditLogRepository(pool)
	auditHasher := auditcrypto.NewSHA256Hasher()
	recordAction := auditapp.NewRecordActionUseCase(auditRepo, auditHasher, realClock{})
	natsConsumer := eventbus.NewConsumer(natsClient, logger.With("component", "audit_consumer"))
	auditConsumer := auditnats.NewEventConsumer(natsConsumer, recordAction, logger.With("component", "audit"))

	// ── Graceful shutdown ─────────────────────────────────────────────────────

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := relay.Start(ctx); err != nil {
			logger.Error("relay: stopped with error", "error", err)
			cancel()
		}
	}()

	go func() {
		if err := auditConsumer.StartAuthConsumer(ctx); err != nil {
			logger.Error("audit auth consumer: stopped with error", "error", err)
		}
	}()

	// ── Auth consumer: reacciona a onboarding aprobado ───────────────────────

	tenantRepo := authpg.NewTenantRepository(pool)
	activateTenant := authapp.NewActivateTenantUseCase(
		tenantRepo,
		postgres.NewTxManager(pool),
		// eventPublisher para auth (outbox ya inicializado arriba)
		nil, // placeholder: en producción inyectar el publisher de auth
	)
	authOnboardingConsumer := authnats.NewOnboardingConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "auth_onboarding")),
		activateTenant,
		logger.With("component", "auth_onboarding"),
	)

	// ── Ledger consumer: crea cuentas al aprobarse el KYC ────────────────────

	accountRepo := ledgerpg.NewAccountRepository(pool)
	// El outbox del ledger necesita su propio publisher; usamos el mismo store.
	createAccount := ledgerapp.NewCreateAccountUseCase(
		accountRepo,
		postgres.NewTxManager(pool),
		nil, // placeholder: en producción inyectar el publisher de ledger
	)
	ledgerOnboardingConsumer := ledgernats.NewOnboardingConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "ledger_onboarding")),
		createAccount,
		logger.With("component", "ledger_onboarding"),
	)

	go func() {
		if err := authOnboardingConsumer.Start(ctx); err != nil {
			logger.Error("auth onboarding consumer: stopped", "error", err)
		}
	}()

	go func() {
		if err := ledgerOnboardingConsumer.Start(ctx); err != nil {
			logger.Error("ledger onboarding consumer: stopped", "error", err)
		}
	}()

	logger.Info("worker started",
		"outbox_interval", cfg.OutboxInterval,
		"outbox_batch_size", cfg.OutboxBatchSize,
	)

	<-quit
	logger.Info("worker: shutting down...")
	cancel()
	logger.Info("worker: stopped")
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
