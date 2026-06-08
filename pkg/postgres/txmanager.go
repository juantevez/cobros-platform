package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxManager gestiona transacciones PostgreSQL e inyecta el contexto de RLS.
//
// Uso típico en un caso de uso:
//
//	func (uc *RegisterTenantUseCase) Execute(ctx context.Context, cmd Command) error {
//	    return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
//	        tenant := domain.NewTenant(...)
//	        if err := uc.repo.Save(ctx, tenant); err != nil {
//	            return err
//	        }
//	        return uc.outbox.Save(ctx, buildEvent(tenant))
//	    })
//	}
type TxManager struct {
	pool *pgxpool.Pool
}

// NewTxManager crea un TxManager con el pool dado.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// RunInTx ejecuta fn dentro de una transacción READ COMMITTED.
//
//   - Si el contexto contiene un tenantID, lo configura con set_config
//     para que las políticas de RLS tomen efecto automáticamente.
//   - Inyecta la transacción en el contexto resultante para que los
//     repositorios la reutilicen vía ConnFromContext.
//   - Si fn retorna error, hace rollback y propaga el error.
//   - Si fn retorna nil, hace commit.
func (m *TxManager) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("txmanager: begin: %w", err)
	}

	// Configurar el tenant para RLS (LOCAL = solo esta transacción).
	// set_config con is_local=true equivale a SET LOCAL.
	if tenantID, ok := TenantIDFromContext(ctx); ok {
		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.current_tenant', $1, true)`, tenantID,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("txmanager: set rls context: %w", err)
		}
	}

	// Inyectar la tx para que los repositorios la usen.
	txCtx := WithTx(ctx, tx)

	if err := fn(txCtx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("txmanager: commit: %w", err)
	}
	return nil
}
