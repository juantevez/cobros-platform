package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	// ── Graceful shutdown ─────────────────────────────────────────────────────

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := relay.Start(ctx); err != nil {
			logger.Error("relay: stopped with error", "error", err)
			cancel()
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
