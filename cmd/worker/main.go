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

	go func() {
		if err := auditConsumer.StartLedgerConsumer(ctx); err != nil {
			logger.Error("audit ledger consumer: stopped with error", "error", err)
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
