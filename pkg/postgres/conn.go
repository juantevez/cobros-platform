package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Conn es la interfaz mínima compartida entre *pgxpool.Pool y pgx.Tx.
//
// Los repositorios la usan para ejecutar queries sin saber si están
// dentro de una transacción o no. Esto los hace agnósticos al ciclo
// de vida de la transacción, que es responsabilidad del TxManager.
//
// Tanto *pgxpool.Pool como pgx.Tx implementan esta interfaz en pgx v5.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ConnFromContext retorna la transacción activa del contexto si existe,
// o el pool como fallback.
//
// Todos los métodos de repositorio deben llamar esto en vez de usar
// el pool directamente:
//
//	conn := postgres.ConnFromContext(ctx, r.pool)
//	_, err := conn.Exec(ctx, `INSERT INTO ...`, args...)
func ConnFromContext(ctx context.Context, pool *pgxpool.Pool) Conn {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return pool
}
