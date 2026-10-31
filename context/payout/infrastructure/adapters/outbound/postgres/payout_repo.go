package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

const pgErrUniqueViolation = "23505"

type pgPayoutRepository struct {
	pool postgres.Conn
}

func NewPayoutRepository(pool postgres.Conn) *pgPayoutRepository {
	return &pgPayoutRepository{pool: pool}
}

// Cada método corre en la sesión RLS del tenant que corresponde (withTenant):
// el de la entidad al escribir, el del llamador en las búsquedas por ID.

func (r *pgPayoutRepository) Save(ctx context.Context, p *domain.Payout) error {
	return withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return save(ctx, conn, p)
	})
}

func save(ctx context.Context, conn postgres.Conn, p *domain.Payout) error {
	ba := p.BankAccount()
	_, err := conn.Exec(ctx, `
		INSERT INTO payouts (
			id, tenant_id, amount, currency,
			bank_acct_type, bank_acct_num, bank_name, holder_name,
			status, bank_reference, failure_reason, ledger_entry_key,
			idempotency_key,
			initiated_at, confirmed_at, failed_at,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		p.ID().String(), p.TenantID().String(),
		p.Amount().Amount(), p.Amount().Currency(),
		ba.AccountType, ba.AccountNumber(), nullStr(ba.BankName), ba.HolderName,
		p.Status().String(), nullStr(p.BankReference()), nullStr(p.FailureReason()),
		p.LedgerEntryKey(), p.IdempotencyKey(),
		p.InitiatedAt(), p.ConfirmedAt(), p.FailedAt(),
		p.CreatedAt(), p.UpdatedAt(),
	)
	if err != nil {
		// Violación del UNIQUE (tenant_id, idempotency_key): otro request con la
		// misma clave ganó la carrera. El caso de uso la mapea a un retry.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrUniqueViolation {
			return fmt.Errorf("payout repo: save: %w", domain.ErrPayoutAlreadyExists)
		}
		return wrapErr("save", err)
	}
	return nil
}

func (r *pgPayoutRepository) Update(ctx context.Context, p *domain.Payout, from domain.PayoutStatus) error {
	return withTenant(ctx, r.pool, p.TenantID().String(), func(ctx context.Context, conn postgres.Conn) error {
		return update(ctx, conn, p, from)
	})
}

func update(ctx context.Context, conn postgres.Conn, p *domain.Payout, from domain.PayoutStatus) error {
	// Condicional al estado de partida: el request original y el recuperador
	// pueden resolver el mismo payout; solo uno escribe.
	tag, err := conn.Exec(ctx, `
		UPDATE payouts SET
			status=$2, bank_reference=$3, failure_reason=$4,
			initiated_at=$5, confirmed_at=$6, failed_at=$7, updated_at=$8
		WHERE id=$1 AND status=$9`,
		p.ID().String(),
		p.Status().String(), nullStr(p.BankReference()), nullStr(p.FailureReason()),
		p.InitiatedAt(), p.ConfirmedAt(), p.FailedAt(), p.UpdatedAt(),
		from.String(),
	)
	if err != nil {
		return wrapErr("update", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("payout repo: update %s from %s: %w", p.ID(), from, domain.ErrPayoutChanged)
	}
	return nil
}

// ListUnresolved es cross-tenant (lo usa el recuperador del worker): corre
// con la política de operador, de solo lectura.
func (r *pgPayoutRepository) ListUnresolved(ctx context.Context, olderThan time.Time, limit int) ([]*domain.Payout, error) {
	var out []*domain.Payout
	err := asPlatformOperator(ctx, r.pool, func(ctx context.Context, conn postgres.Conn) error {
		var err error
		out, err = listUnresolved(ctx, conn, olderThan, limit)
		return err
	})
	return out, err
}

func listUnresolved(ctx context.Context, conn postgres.Conn, olderThan time.Time, limit int) ([]*domain.Payout, error) {
	rows, err := conn.Query(ctx,
		baseSelect+" WHERE status IN ('processing','unknown') AND updated_at < $1 ORDER BY updated_at LIMIT $2",
		olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("payout repo: list unresolved: %w", err)
	}
	defer rows.Close()
	var out []*domain.Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *pgPayoutRepository) FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error) {
	var p *domain.Payout
	err := withTenant(ctx, r.pool, callerTenant(ctx), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		p, err = scanPayout(conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String()))
		return err
	})
	return p, err
}

// FindByIdempotencyKey busca el payout del tenant con la clave dada.
// Usa la columna idempotency_key con su índice UNIQUE parcial.
func (r *pgPayoutRepository) FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payout, error) {
	if key == "" {
		return nil, domain.ErrPayoutNotFound
	}
	var p *domain.Payout
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		p, err = scanPayout(conn.QueryRow(ctx, baseSelect+" WHERE tenant_id=$1 AND idempotency_key=$2",
			tenantID.String(), key))
		return err
	})
	return p, err
}

func (r *pgPayoutRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Payout, error) {
	var out []*domain.Payout
	err := withTenant(ctx, r.pool, tenantID.String(), func(ctx context.Context, conn postgres.Conn) error {
		var err error
		out, err = listByTenant(ctx, conn, tenantID, limit)
		return err
	})
	return out, err
}

func listByTenant(ctx context.Context, conn postgres.Conn, tenantID domain.TenantID, limit int) ([]*domain.Payout, error) {
	rows, err := conn.Query(ctx, baseSelect+" WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2",
		tenantID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("payout repo: list: %w", err)
	}
	defer rows.Close()

	var payouts []*domain.Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		payouts = append(payouts, p)
	}
	return payouts, rows.Err()
}

const baseSelect = `
	SELECT id, tenant_id, amount, currency,
	       bank_acct_type, bank_acct_num, COALESCE(bank_name,''), holder_name,
	       status, COALESCE(bank_reference,''), COALESCE(failure_reason,''), ledger_entry_key,
	       COALESCE(idempotency_key,''),
	       initiated_at, confirmed_at, failed_at,
	       created_at, updated_at
	FROM payouts`

func scanPayout(row interface{ Scan(...any) error }) (*domain.Payout, error) {
	var (
		idStr, tenantIDStr, currency                        string
		acctType, acctNum, bankName, holderName             string
		status, bankRef, failReason, ledgerKey, idemKey            string
		amount                                              int64
		initiatedAt, confirmedAt, failedAt                  *time.Time
		createdAt, updatedAt                                time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &amount, &currency,
		&acctType, &acctNum, &bankName, &holderName,
		&status, &bankRef, &failReason, &ledgerKey,
		&idemKey,
		&initiatedAt, &confirmedAt, &failedAt,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPayoutNotFound
		}
		return nil, fmt.Errorf("payout repo: scan: %w", err)
	}

	return domain.ReconstitutePayoutWithIdempotency(
		domain.PayoutID(idStr),
		domain.TenantID(tenantIDStr),
		domain.ReconstituteMoney(amount, currency),
		domain.MustBankAccountInfo(acctType, acctNum, bankName, holderName, currency),
		domain.PayoutStatus(status),
		bankRef, failReason, ledgerKey, idemKey,
		initiatedAt, confirmedAt, failedAt,
		createdAt.UTC(), updatedAt.UTC(),
	), nil
}

func wrapErr(op string, err error) error {
	if err != nil {
		return fmt.Errorf("payout repo: %s: %w", op, err)
	}
	return nil
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
