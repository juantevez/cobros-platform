package postgres

import "time"

// Config contiene los parámetros de conexión al pool de PostgreSQL.
type Config struct {
	// DSN en formato: "postgres://user:pass@host:5432/dbname?sslmode=disable"
	DSN string

	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	HealthCheck     time.Duration
}

// DefaultConfig retorna una Config con valores razonables para producción.
func DefaultConfig(dsn string) Config {
	return Config{
		DSN:             dsn,
		MaxConns:        25,
		MinConns:        5,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
		HealthCheck:     1 * time.Minute,
	}
}
