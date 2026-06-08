// Package config carga la configuración de la aplicación desde variables de entorno.
//
// Convencion: cada variable tiene un valor por defecto seguro para desarrollo local.
// En producción todas deben estar explícitamente configuradas, especialmente
// DATABASE_URL, JWT_SECRET y NATS_URL.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config contiene toda la configuración de la aplicación.
// Un único struct compartido por cmd/api y cmd/worker.
type Config struct {
	// ── HTTP ────────────────────────────────────────────────────────────────
	HTTPAddr        string
	ReadTimeoutSec  int
	WriteTimeoutSec int

	// ── PostgreSQL ──────────────────────────────────────────────────────────
	DatabaseURL     string
	DBMaxConns      int32
	DBMinConns      int32

	// ── NATS ────────────────────────────────────────────────────────────────
	NatsURL string

	// ── Auth ────────────────────────────────────────────────────────────────
	// JWTSecret debe tener al menos 32 caracteres.
	// En producción usar un secreto generado con: openssl rand -hex 32
	JWTSecret string

	// ── Outbox relay ────────────────────────────────────────────────────────
	OutboxInterval  time.Duration
	OutboxBatchSize int
}

// Load carga la configuración desde variables de entorno.
// Valores por defecto apuntan al entorno local (docker-compose).
func Load() Config {
	return Config{
		// HTTP
		HTTPAddr:        getStr("HTTP_ADDR", ":8080"),
		ReadTimeoutSec:  getInt("HTTP_READ_TIMEOUT_SEC", 10),
		WriteTimeoutSec: getInt("HTTP_WRITE_TIMEOUT_SEC", 30),

		// PostgreSQL
		DatabaseURL: getStr("DATABASE_URL",
			"postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable"),
		DBMaxConns: int32(getInt("DB_MAX_CONNS", 25)),
		DBMinConns: int32(getInt("DB_MIN_CONNS", 5)),

		// NATS
		NatsURL: getStr("NATS_URL", "nats://localhost:4222"),

		// Auth
		JWTSecret: getStr("JWT_SECRET", "change-me-in-production-needs-32-chars!!"),

		// Outbox relay
		OutboxInterval:  getDuration("OUTBOX_INTERVAL", 1*time.Second),
		OutboxBatchSize: getInt("OUTBOX_BATCH_SIZE", 50),
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func getStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
