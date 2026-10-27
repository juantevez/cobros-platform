// Package onboarding provee adaptadores para consultar datos del contexto Onboarding.
package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/juantevez/cobros-platform/context/payout/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// BankAccountProvider implementa application.BankAccountProvider consultando
// directamente la tabla onboarding_bank_accounts en Postgres.
type BankAccountProvider struct {
	pool pkgpostgres.Conn
}

func NewBankAccountProvider(pool pkgpostgres.Conn) *BankAccountProvider {
	return &BankAccountProvider{pool: pool}
}

// GetBankAccount retorna la cuenta bancaria verificada del comercio.
// Retorna ErrNoBankAccount si el comercio no tiene cuenta registrada.
func (p *BankAccountProvider) GetBankAccount(
	ctx context.Context,
	tenantID domain.TenantID,
) (domain.BankAccountInfo, error) {
	var accountType, accountNum, bankName, holderName, currency string

	err := p.pool.QueryRow(ctx, `
		SELECT oba.account_type, oba.account_number,
		       COALESCE(oba.bank_name, ''), oba.holder_name, oba.currency
		FROM onboarding_bank_accounts oba
		JOIN onboarding_applications oa ON oa.id = oba.application_id
		WHERE oa.tenant_id = $1
		  AND oa.status    = 'approved'
		LIMIT 1`,
		tenantID.String(),
	).Scan(&accountType, &accountNum, &bankName, &holderName, &currency)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BankAccountInfo{}, domain.ErrNoBankAccount
		}
		return domain.BankAccountInfo{}, fmt.Errorf("bank account provider: %w", err)
	}

	return domain.NewBankAccountInfo(accountType, accountNum, bankName, holderName, currency)
}
