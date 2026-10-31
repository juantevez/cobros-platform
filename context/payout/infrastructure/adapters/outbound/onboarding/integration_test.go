package onboarding

import (
	"context"
	"errors"
	"testing"

	onbdomain "github.com/juantevez/cobros-platform/context/onboarding/domain"
	onbpg "github.com/juantevez/cobros-platform/context/onboarding/infrastructure/adapters/outbound/postgres"
	"github.com/juantevez/cobros-platform/context/payout/domain"
)

// approvedApplication crea en Onboarding una solicitud aprobada con su cuenta
// de desembolso en currency, y registra la limpieza.
func approvedApplication(t *testing.T, currency string) onbdomain.TenantID {
	t.Helper()
	pool := requireDB(t)
	ctx := context.Background()
	repo := onbpg.NewApplicationRepository(pool)
	tenantID, _ := onbdomain.ParseTenantID(onbdomain.NewApplicationID().String())
	t.Cleanup(func() {
		for _, table := range []string{"onboarding_bank_accounts", "onboarding_persons", "onboarding_documents", "onboarding_applications"} {
			_, _ = pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE tenant_id=$1`, tenantID.String())
		}
	})

	app, err := onbdomain.NewOnboardingApplication(onbdomain.NewApplicationID(), tenantID, onbdomain.BusinessInfo{
		LegalName: "Acme", TaxID: onbdomain.TaxID("20304050607"), BusinessCategory: onbdomain.CategoryRetail,
		Address: onbdomain.Address{Street: "Calle 1", City: "CABA", State: "BA", Country: "AR", PostalCode: "1000"},
	})
	if err != nil {
		t.Fatalf("new application: %v", err)
	}
	if err := repo.Save(ctx, app); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = app.AddDocument(onbdomain.NewBusinessDocument(onbdomain.NewDocumentID(), onbdomain.DocTypeIDCard, "s3://ref"))
	_ = app.AddPerson(onbdomain.NewPerson(onbdomain.NewPersonID(), "Owner", onbdomain.RoleOwner, "DNI", "123", "AR"))
	_ = app.SetBankAccount(onbdomain.NewBankAccount(onbdomain.NewBankAccountID(), onbdomain.BankAccountCBU, "0011223344556677889900", "Banco", "Acme", currency))
	if err := app.SubmitForReview(); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := app.Approve("ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := repo.Update(ctx, app); err != nil {
		t.Fatalf("update: %v", err)
	}
	return tenantID
}

// Solo una cuenta verificada (la aprobación del KYC la verifica) y en la
// moneda del payout: antes se tomaba la primera cuenta de una solicitud
// aprobada sin mirar ninguna de las dos cosas.
func TestBankAccountProvider_VerifiedAndCurrency(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	provider := NewBankAccountProvider(pool)
	tenant := approvedApplication(t, "ARS")

	acct, err := provider.GetBankAccount(ctx, domain.TenantID(tenant.String()), "ARS")
	if err != nil || acct.AccountNumber() != "0011223344556677889900" {
		t.Fatalf("approved ARS account = %+v, %v", acct, err)
	}
	if _, err := provider.GetBankAccount(ctx, domain.TenantID(tenant.String()), "USD"); !errors.Is(err, domain.ErrNoBankAccount) {
		t.Errorf("USD payout against an ARS account = %v, want ErrNoBankAccount", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE onboarding_bank_accounts SET verified=false WHERE tenant_id=$1`, tenant.String()); err != nil {
		t.Fatalf("unverify: %v", err)
	}
	if _, err := provider.GetBankAccount(ctx, domain.TenantID(tenant.String()), "ARS"); !errors.Is(err, domain.ErrNoBankAccount) {
		t.Errorf("unverified account = %v, want ErrNoBankAccount", err)
	}
}
