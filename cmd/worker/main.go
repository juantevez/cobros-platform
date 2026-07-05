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
	webhookapp "github.com/juantevez/cobros-platform/context/webhook/application"
	webhookdomain "github.com/juantevez/cobros-platform/context/webhook/domain"
	webhooknats "github.com/juantevez/cobros-platform/context/webhook/infrastructure/adapters/inbound/nats"
	notificationapp "github.com/juantevez/cobros-platform/context/notification/application"
	notificationnats "github.com/juantevez/cobros-platform/context/notification/infrastructure/adapters/inbound/nats"
	notificationemail "github.com/juantevez/cobros-platform/context/notification/infrastructure/adapters/outbound/email"
	notificationpg "github.com/juantevez/cobros-platform/context/notification/infrastructure/adapters/outbound/postgres"
	disputeapp "github.com/juantevez/cobros-platform/context/dispute/application"
	disputedomain "github.com/juantevez/cobros-platform/context/dispute/domain"
	disputepg "github.com/juantevez/cobros-platform/context/dispute/infrastructure/adapters/outbound/postgres"
	reportingapp "github.com/juantevez/cobros-platform/context/reporting/application"
	reportingnats "github.com/juantevez/cobros-platform/context/reporting/infrastructure/adapters/inbound/nats"
	reportingpg "github.com/juantevez/cobros-platform/context/reporting/infrastructure/adapters/outbound/postgres"
	complianceapp "github.com/juantevez/cobros-platform/context/compliance/application"
	compliancedomain "github.com/juantevez/cobros-platform/context/compliance/domain"
	compliancenats "github.com/juantevez/cobros-platform/context/compliance/infrastructure/adapters/inbound/nats"
	compliancepg "github.com/juantevez/cobros-platform/context/compliance/infrastructure/adapters/outbound/postgres"
	webhookcrypto "github.com/juantevez/cobros-platform/context/webhook/infrastructure/adapters/outbound/crypto"
	webhookdispatcher "github.com/juantevez/cobros-platform/context/webhook/infrastructure/adapters/outbound/http"
	webhookpg "github.com/juantevez/cobros-platform/context/webhook/infrastructure/adapters/outbound/postgres"
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

	authPub    := outbox.NewEventPublisher[authdomain.Event](outboxStore)
	ledgerPub  := outbox.NewEventPublisher[ledgerdomain.Event](outboxStore)
	webhookPub  := outbox.NewEventPublisher[webhookdomain.Event](outboxStore)
	disputePub  := outbox.NewEventPublisher[disputedomain.Event](outboxStore)

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
	ledgerPayoutConsumer := ledgernats.NewPayoutConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "ledger_payout")),
		postEntry,
		accountRepo,
		logger.With("component", "ledger_payout"),
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
	go func() {
		if err := ledgerPayoutConsumer.Start(ctx); err != nil {
			logger.Error("ledger payout consumer stopped", "error", err)
		}
	}()

	// ── Webhook: consumers + RetryPoller ─────────────────────────────────────

	endpointRepo  := webhookpg.NewEndpointRepository(pool)
	deliveryRepo  := webhookpg.NewDeliveryRepository(pool)
	secretGen     := webhookcrypto.NewHexSecretGenerator()
	httpDispatcher := webhookdispatcher.NewDispatcher()
	_ = secretGen // usado en cmd/api

	dispatchEvent := webhookapp.NewDispatchEventUseCase(endpointRepo, deliveryRepo)
	retryDelivery := webhookapp.NewRetryDeliveryUseCase(
		endpointRepo, deliveryRepo, httpDispatcher, webhookPub, realClock{},
	)
	retryPoller := webhookapp.NewRetryPoller(
		retryDelivery, deliveryRepo, 5*time.Second,
		logger.With("component", "webhook_retry_poller"),
	)

	webhookConsumer := webhooknats.NewEventConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "webhook")),
		dispatchEvent,
		logger.With("component", "webhook"),
	)

	go func() {
		if err := webhookConsumer.StartPaymentConsumer(ctx); err != nil {
			logger.Error("webhook payment consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := webhookConsumer.StartPayoutConsumer(ctx); err != nil {
			logger.Error("webhook payout consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := webhookConsumer.StartOnboardingConsumer(ctx); err != nil {
			logger.Error("webhook onboarding consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := webhookConsumer.StartAuthConsumer(ctx); err != nil {
			logger.Error("webhook auth consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := retryPoller.Start(ctx); err != nil {
			logger.Error("webhook retry poller stopped", "error", err)
		}
	}()

	// ── Notification consumers ────────────────────────────────────────────────

	notifRepo    := notificationpg.NewNotificationRepository(pool)
	notifPrefRepo := notificationpg.NewPreferenceRepository(pool)
	contactReader := notificationpg.NewContactReader(pool)
	notifEmailSender := notificationemail.NewLogSender(logger.With("component", "email"))

	sendNotif := notificationapp.NewSendNotificationUseCase(
		notifRepo, notifPrefRepo, notifEmailSender, contactReader,
		logger.With("component", "notification"),
	)
	notifConsumer := notificationnats.NewEventConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "notification")),
		sendNotif,
		logger.With("component", "notification"),
	)

	go func() {
		if err := notifConsumer.StartPaymentConsumer(ctx); err != nil {
			logger.Error("notification payment consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := notifConsumer.StartPayoutConsumer(ctx); err != nil {
			logger.Error("notification payout consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := notifConsumer.StartOnboardingConsumer(ctx); err != nil {
			logger.Error("notification onboarding consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := notifConsumer.StartAuthConsumer(ctx); err != nil {
			logger.Error("notification auth consumer stopped", "error", err)
		}
	}()

	// ── Dispute: Ledger consumer + ExpiryPoller ───────────────────────────────

	disputeRepo   := disputepg.NewDisputeRepository(pool)
	expiryPoller  := disputeapp.NewExpiryPoller(
		disputeRepo, postgres.NewTxManager(pool), disputePub,
		realClock{}, logger.With("component", "dispute_expiry"),
	)
	ledgerDisputeConsumer := ledgernats.NewDisputeConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "ledger_dispute")),
		postEntry,
		accountRepo,
		logger.With("component", "ledger_dispute"),
	)

	go func() {
		if err := ledgerDisputeConsumer.Start(ctx); err != nil {
			logger.Error("ledger dispute consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := expiryPoller.Start(ctx); err != nil {
			logger.Error("dispute expiry poller stopped", "error", err)
		}
	}()

	// ── Reporting: proyección del read-model ──────────────────────────────────
	// Consume PAYMENT y LEDGER y proyecta hechos idempotentes para el dashboard.

	projectionRepo := reportingpg.NewProjectionRepository(pool)
	accountReader  := reportingpg.NewAccountReader(pool)
	projectEvents  := reportingapp.NewProjectEventsUseCase(projectionRepo, accountReader, realClock{})
	reportingConsumer := reportingnats.NewEventConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "reporting")),
		projectEvents,
		logger.With("component", "reporting"),
	)

	go func() {
		if err := reportingConsumer.StartPaymentConsumer(ctx); err != nil {
			logger.Error("reporting payment consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := reportingConsumer.StartLedgerConsumer(ctx); err != nil {
			logger.Error("reporting ledger consumer stopped", "error", err)
		}
	}()

	// ── Compliance & AML: screening + monitoreo transaccional ─────────────────

	compliancePub  := outbox.NewEventPublisher[compliancedomain.Event](outboxStore)
	complianceAlerts := compliancepg.NewAlertRepository(pool)
	complianceWatch  := compliancepg.NewWatchlistRepository(pool)
	complianceTxRead := compliancepg.NewTransactionReader(pool)

	screenApp := complianceapp.NewScreenApplicationUseCase(
		complianceAlerts, complianceWatch, txMgr, compliancePub, realClock{},
	)
	monitorTx := complianceapp.NewMonitorTransactionUseCase(
		complianceAlerts, complianceTxRead, txMgr, compliancePub, realClock{},
		complianceapp.DefaultMonitoringRules(),
	)
	complianceConsumer := compliancenats.NewEventConsumer(
		eventbus.NewConsumer(natsClient, logger.With("component", "compliance")),
		screenApp, monitorTx,
		logger.With("component", "compliance"),
	)

	go func() {
		if err := complianceConsumer.StartOnboardingConsumer(ctx); err != nil {
			logger.Error("compliance onboarding consumer stopped", "error", err)
		}
	}()
	go func() {
		if err := complianceConsumer.StartPaymentConsumer(ctx); err != nil {
			logger.Error("compliance payment consumer stopped", "error", err)
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
