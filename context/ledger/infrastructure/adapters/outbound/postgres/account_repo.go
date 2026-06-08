package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgAccountRepository struct {
	pool *pgxpool.Pool
}

func NewAccountRepository(pool *pgxpool.Pool) *pgAccountRepository {
	return &pgAccountRepository{pool: pool}
}

func (r *pgAccountRepository) Save(ctx context.Context, a *domain.Account) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		INSERT INTO ledger_accounts (id, tenant_id, type, currency, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ID().String(),
		a.TenantID().String(),
		a.AccountType().String(),
		a.Currency(),
		a.Description(),
		a.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("account repo: save: %w", err)
	}

	// Inicializar el saldo en cero para la nueva cuenta.
	_, err = conn.Exec(ctx, `
		INSERT INTO account_balances (account_id, balance, updated_at)
		VALUES ($1, 0, $2)`,
		a.ID().String(),
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("account repo: init balance: %w", err)
	}

	return nil
}

func (r *pgAccountRepository) FindByID(ctx context.Context, id domain.AccountID) (*domain.Account, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, tenant_id, type, currency, description, created_at
		FROM ledger_accounts WHERE id = $1`,
		id.String(),
	)
	return scanAccount(row)
}

func (r *pgAccountRepository) FindByTenantAndType(
	ctx context.Context,
	tenantID domain.TenantID,
	accountType domain.AccountType,
	currency string,
) (*domain.Account, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, `
		SELECT id, tenant_id, type, currency, description, created_at
		FROM ledger_accounts
		WHERE tenant_id = $1 AND type = $2 AND currency = $3
		LIMIT 1`,
		tenantID.String(), accountType.String(), currency,
	)
	return scanAccount(row)
}

func scanAccount(row pgx.Row) (*domain.Account, error) {
	var idStr, tenantIDStr, typeStr, currency, description string
	var createdAt time.Time

	if err := row.Scan(&idStr, &tenantIDStr, &typeStr, &currency, &description, &createdAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, fmt.Errorf("account repo: scan: %w", err)
	}

	accountType, err := domain.ParseAccountType(typeStr)
	if err != nil {
		return nil, fmt.Errorf("account repo: invalid type in db: %w", err)
	}

	return domain.ReconstituteAccount(
		domain.AccountID(idStr),
		domain.TenantID(tenantIDStr),
		accountType,
		currency,
		description,
		createdAt.UTC(),
	), nil
}
