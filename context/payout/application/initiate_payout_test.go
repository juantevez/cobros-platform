package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

type initiateDeps struct {
	repo     *fakeRepo
	balance  *fakeBalanceChecker
	bank     *fakeBankProvider
	transfer *fakeTransfer
	tx       *fakeTx
	pub      *fakePublisher
}

func newInitiateDeps() *initiateDeps {
	return &initiateDeps{
		repo:     &fakeRepo{},
		balance:  &fakeBalanceChecker{balance: 10_000},
		bank:     &fakeBankProvider{account: validBankAccount()},
		transfer: &fakeTransfer{result: TransferResult{BankReference: "BANK-REF-1", Status: "confirmed"}},
		tx:       &fakeTx{},
		pub:      &fakePublisher{},
	}
}

func (d *initiateDeps) uc() *InitiatePayoutUseCase {
	return NewInitiatePayoutUseCase(d.repo, d.balance, d.bank, d.transfer, d.tx, d.pub)
}

func TestInitiatePayoutUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("happy path confirms payout and publishes initiated + confirmed", func(t *testing.T) {
		d := newInitiateDeps()

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 4_000, Currency: "ARS"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.Amount != 4_000 || res.Currency != "ARS" {
			t.Errorf("unexpected amount/currency: %+v", res)
		}
		if res.Status != domain.StatusConfirmed.String() {
			t.Errorf("status = %q, want confirmed", res.Status)
		}
		if res.BankReference != "BANK-REF-1" {
			t.Errorf("bank reference = %q", res.BankReference)
		}
		if _, err := domain.ParsePayoutID(res.PayoutID); err != nil {
			t.Errorf("payout id is not a uuid: %v", err)
		}

		if len(d.repo.saved) != 1 {
			t.Fatalf("expected 1 save, got %d", len(d.repo.saved))
		}
		saved := d.repo.saved[0]
		if saved.ID().String() != res.PayoutID {
			t.Errorf("saved id %q != result id %q", saved.ID(), res.PayoutID)
		}
		if saved.BankReference() != "BANK-REF-1" {
			t.Errorf("aggregate bank reference = %q", saved.BankReference())
		}

		wantUpdates := []domain.PayoutStatus{domain.StatusProcessing, domain.StatusConfirmed}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}

		wantEvents := []string{"payout.initiated.v1", "payout.confirmed.v1"}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, wantEvents) {
			t.Errorf("events = %v, want %v", got, wantEvents)
		}
		if d.tx.calls != 2 {
			t.Errorf("tx calls = %d, want 2", d.tx.calls)
		}
	})

	t.Run("transfer request is built from payout and bank account", func(t *testing.T) {
		d := newInitiateDeps()

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 2_500, Currency: "ARS"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(d.transfer.reqs) != 1 {
			t.Fatalf("expected 1 transfer, got %d", len(d.transfer.reqs))
		}
		req := d.transfer.reqs[0]
		ba := validBankAccount()
		want := TransferRequest{
			PayoutID:       res.PayoutID,
			IdempotencyKey: "transfer_" + res.PayoutID,
			Amount:         2_500,
			Currency:       "ARS",
			AccountType:    ba.AccountType,
			AccountNumber:  ba.AccountNumber,
			BankName:       ba.BankName,
			HolderName:     ba.HolderName,
			Description:    "Desembolso " + res.PayoutID[:8],
		}
		if req != want {
			t.Errorf("transfer request = %+v\nwant %+v", req, want)
		}
	})

	t.Run("zero amount pays out full available balance", func(t *testing.T) {
		d := newInitiateDeps()
		d.balance.balance = 7_777

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Currency: "ARS"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Amount != 7_777 {
			t.Errorf("amount = %d, want 7777", res.Amount)
		}
		if d.transfer.reqs[0].Amount != 7_777 {
			t.Errorf("transfer amount = %d", d.transfer.reqs[0].Amount)
		}
	})

	t.Run("amount equal to available balance is allowed", func(t *testing.T) {
		d := newInitiateDeps()

		if _, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 10_000, Currency: "ARS"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty currency defaults to ARS", func(t *testing.T) {
		d := newInitiateDeps()

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Currency != "ARS" {
			t.Errorf("currency = %q, want ARS", res.Currency)
		}
		if d.balance.lastCurrency != "ARS" {
			t.Errorf("balance checked with currency %q, want ARS", d.balance.lastCurrency)
		}
	})

	t.Run("invalid tenant id rejected before calling dependencies", func(t *testing.T) {
		d := newInitiateDeps()

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: "not-a-uuid", Amount: 100})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected invalid tenant id error, got %v", err)
		}
		if d.balance.calls != 0 || len(d.repo.saved) != 0 || len(d.transfer.reqs) != 0 {
			t.Error("no dependency should be called on invalid tenant")
		}
	})

	t.Run("bank provider error is wrapped", func(t *testing.T) {
		d := newInitiateDeps()
		d.bank.err = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "get bank account") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if d.balance.calls != 0 {
			t.Error("balance should not be checked when bank lookup fails")
		}
	})

	t.Run("missing bank account number returns ErrNoBankAccount", func(t *testing.T) {
		d := newInitiateDeps()
		d.bank.account = domain.BankAccountInfo{AccountType: "CBU"}

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, domain.ErrNoBankAccount) {
			t.Fatalf("expected ErrNoBankAccount, got %v", err)
		}
		if d.balance.calls != 0 {
			t.Error("balance should not be checked without bank account")
		}
	})

	t.Run("balance checker error is wrapped", func(t *testing.T) {
		d := newInitiateDeps()
		d.balance.err = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "get available balance") {
			t.Errorf("error not wrapped with context: %v", err)
		}
	})

	insufficient := []struct {
		name      string
		amount    int64
		available int64
	}{
		{"amount greater than available", 10_001, 10_000},
		{"zero amount with zero balance", 0, 0},
		{"zero amount with negative balance", 0, -500},
		{"negative amount", -100, 10_000},
	}
	for _, tc := range insufficient {
		t.Run("insufficient balance: "+tc.name, func(t *testing.T) {
			d := newInitiateDeps()
			d.balance.balance = tc.available

			_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: tc.amount, Currency: "ARS"})
			if !errors.Is(err, domain.ErrInsufficientBalance) {
				t.Fatalf("expected ErrInsufficientBalance, got %v", err)
			}
			if len(d.repo.saved) != 0 || d.tx.calls != 0 || len(d.transfer.reqs) != 0 {
				t.Error("nothing should be persisted or transferred")
			}
		})
	}

	t.Run("invalid currency rejected before persisting", func(t *testing.T) {
		d := newInitiateDeps()

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, Currency: "PESOS"})
		if !errors.Is(err, domain.ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
		if len(d.repo.saved) != 0 || d.tx.calls != 0 {
			t.Error("nothing should be persisted")
		}
	})

	t.Run("save error aborts before transfer", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.saveErr = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "save payout") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if d.pub.calls != 0 {
			t.Error("event should not be published when save fails")
		}
		if len(d.transfer.reqs) != 0 {
			t.Error("transfer should not run when save fails")
		}
	})

	t.Run("initial publish error aborts before transfer", func(t *testing.T) {
		d := newInitiateDeps()
		d.pub.err = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if len(d.transfer.reqs) != 0 {
			t.Error("transfer should not run when publish fails")
		}
	})

	t.Run("initial tx error aborts before transfer", func(t *testing.T) {
		d := newInitiateDeps()
		d.tx.err = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if len(d.repo.saved) != 0 || len(d.transfer.reqs) != 0 {
			t.Error("nothing should run when tx fails")
		}
	})

	t.Run("update to processing error aborts before transfer", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 1

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "update to processing") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if len(d.transfer.reqs) != 0 {
			t.Error("transfer should not run when processing update fails")
		}
	})

	t.Run("transfer failure marks payout failed and publishes failed event", func(t *testing.T) {
		d := newInitiateDeps()
		d.transfer.err = errors.New("account closed")

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if err == nil || !strings.Contains(err.Error(), "transfer failed") {
			t.Fatalf("expected transfer failed error, got %v", err)
		}
		if !errors.Is(err, d.transfer.err) {
			t.Errorf("transfer error not wrapped: %v", err)
		}

		saved := d.repo.saved[0]
		if saved.Status() != domain.StatusFailed {
			t.Errorf("status = %q, want failed", saved.Status())
		}
		if saved.FailureReason() != "account closed" {
			t.Errorf("failure reason = %q", saved.FailureReason())
		}

		wantUpdates := []domain.PayoutStatus{domain.StatusProcessing, domain.StatusFailed}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}
		wantEvents := []string{"payout.initiated.v1", "payout.failed.v1"}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, wantEvents) {
			t.Errorf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("transfer failure still returns transfer error when fail-persist fails", func(t *testing.T) {
		d := newInitiateDeps()
		d.transfer.err = errors.New("rejected")
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2
		d.pub.err = errBoom
		d.pub.errAt = 2

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, d.transfer.err) {
			t.Fatalf("expected transfer error, got %v", err)
		}
		if errors.Is(err, errBoom) {
			t.Error("persistence error of the failure path should be swallowed")
		}
	})

	t.Run("confirm update error is returned", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "update confirmed") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, []string{"payout.initiated.v1"}) {
			t.Errorf("confirmed event should not be published, got %v", got)
		}
	})

	t.Run("confirm publish error is returned", func(t *testing.T) {
		d := newInitiateDeps()
		d.pub.err = errBoom
		d.pub.errAt = 2

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})
}
