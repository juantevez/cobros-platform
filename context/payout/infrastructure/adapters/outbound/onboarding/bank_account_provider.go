// Package onboarding provee adaptadores para consultar datos del contexto Onboarding.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// GetBankAccount retorna la cuenta bancaria verificada del comercio en la
// moneda del payout. Antes tomaba la primera cuenta de una solicitud aprobada
// sin mirar verified ni la moneda: se podía transferir a una cuenta no
// validada o en otra moneda. Retorna ErrNoBankAccount si no hay.
func (p *BankAccountProvider) GetBankAccount(
	ctx context.Context,
	tenantID domain.TenantID,
	currency string,
) (domain.BankAccountInfo, error) {
	var accountType, accountNum, bankName, holderName, accountCurrency string

	// Las tablas de Onboarding aíslan por tenant con RLS: la consulta corre en
	// la sesión del tenant del payout (sin ella, un rol sin BYPASSRLS no ve
	// la cuenta y ningún payout sale).
	ctx = pkgpostgres.WithAppTenant(ctx, "app.current_tenant", tenantID.String())
	err := pkgpostgres.RunWithAppTenant(ctx, p.pool, func(ctx context.Context) error {
		return pkgpostgres.ConnFromContext(ctx, p.pool).QueryRow(ctx, `
		SELECT oba.account_type, oba.account_number,
		       COALESCE(oba.bank_name, ''), oba.holder_name, oba.currency
		FROM onboarding_bank_accounts oba
		JOIN onboarding_applications oa ON oa.id = oba.application_id
		WHERE oa.tenant_id = $1
		  AND oa.status    = 'approved'
		  AND oba.verified
		  AND oba.currency = $2
		LIMIT 1`,
			tenantID.String(), strings.ToUpper(strings.TrimSpace(currency)),
		).Scan(&accountType, &accountNum, &bankName, &holderName, &accountCurrency)
	})

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.BankAccountInfo{}, domain.ErrNoBankAccount
		}
		return domain.BankAccountInfo{}, fmt.Errorf("bank account provider: %w", err)
	}

	return domain.NewBankAccountInfo(accountType, accountNum, bankName, holderName, accountCurrency)
}
