package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/juantevez/cobros-platform/context/payout/domain"
	"github.com/juantevez/cobros-platform/pkg/postgres"
)

type pgPayoutRepository struct {
	pool *pgxpool.Pool
}

func NewPayoutRepository(pool *pgxpool.Pool) *pgPayoutRepository {
	return &pgPayoutRepository{pool: pool}
}

func (r *pgPayoutRepository) Save(ctx context.Context, p *domain.Payout) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	ba := p.BankAccount()
	_, err := conn.Exec(ctx, `
		INSERT INTO payouts (
			id, tenant_id, amount, currency,
			bank_acct_type, bank_acct_num, bank_name, holder_name,
			status, bank_reference, failure_reason, ledger_entry_key,
			initiated_at, confirmed_at, failed_at,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		p.ID().String(), p.TenantID().String(),
		p.Amount().Amount(), p.Amount().Currency(),
		ba.AccountType, ba.AccountNumber, nullStr(ba.BankName), ba.HolderName,
		p.Status().String(), nullStr(p.BankReference()), nullStr(p.FailureReason()),
		p.LedgerEntryKey(),
		p.InitiatedAt(), p.ConfirmedAt(), p.FailedAt(),
		p.CreatedAt(), p.UpdatedAt(),
	)
	return wrapErr("save", err)
}

func (r *pgPayoutRepository) Update(ctx context.Context, p *domain.Payout) error {
	conn := postgres.ConnFromContext(ctx, r.pool)
	_, err := conn.Exec(ctx, `
		UPDATE payouts SET
			status=$2, bank_reference=$3, failure_reason=$4,
			initiated_at=$5, confirmed_at=$6, failed_at=$7, updated_at=$8
		WHERE id=$1`,
		p.ID().String(),
		p.Status().String(), nullStr(p.BankReference()), nullStr(p.FailureReason()),
		p.InitiatedAt(), p.ConfirmedAt(), p.FailedAt(), p.UpdatedAt(),
	)
	return wrapErr("update", err)
}

func (r *pgPayoutRepository) FindByID(ctx context.Context, id domain.PayoutID) (*domain.Payout, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
	row := conn.QueryRow(ctx, baseSelect+" WHERE id=$1", id.String())
	return scanPayout(row)
}

func (r *pgPayoutRepository) ListByTenant(ctx context.Context, tenantID domain.TenantID, limit int) ([]*domain.Payout, error) {
	conn := postgres.ConnFromContext(ctx, r.pool)
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
	       initiated_at, confirmed_at, failed_at,
	       created_at, updated_at
	FROM payouts`

func scanPayout(row interface{ Scan(...any) error }) (*domain.Payout, error) {
	var (
		idStr, tenantIDStr, currency                        string
		acctType, acctNum, bankName, holderName             string
		status, bankRef, failReason, ledgerKey              string
		amount                                              int64
		initiatedAt, confirmedAt, failedAt                  *time.Time
		createdAt, updatedAt                                time.Time
	)

	if err := row.Scan(
		&idStr, &tenantIDStr, &amount, &currency,
		&acctType, &acctNum, &bankName, &holderName,
		&status, &bankRef, &failReason, &ledgerKey,
		&initiatedAt, &confirmedAt, &failedAt,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPayoutNotFound
		}
		return nil, fmt.Errorf("payout repo: scan: %w", err)
	}

	return domain.ReconstitutePayout(
		domain.PayoutID(idStr),
		domain.TenantID(tenantIDStr),
		domain.ReconstituteMoney(amount, currency),
		domain.BankAccountInfo{AccountType: acctType, AccountNumber: acctNum, BankName: bankName, HolderName: holderName, Currency: currency},
		domain.PayoutStatus(status),
		bankRef, failReason, ledgerKey,
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
