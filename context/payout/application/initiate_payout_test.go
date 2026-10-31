package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

type initiateDeps struct {
	gate     *fakeGate
	repo     *fakeRepo
	balance  *fakeBalanceChecker
	bank     *fakeBankProvider
	transfer *fakeTransfer
	tx       *fakeTx
	pub      *fakePublisher
}

func newInitiateDeps() *initiateDeps {
	return &initiateDeps{
		gate:     &fakeGate{},
		repo:     &fakeRepo{},
		balance:  &fakeBalanceChecker{balance: 10_000},
		bank:     &fakeBankProvider{account: validBankAccount()},
		transfer: &fakeTransfer{result: TransferResult{BankReference: "BANK-REF-1", Status: "confirmed"}},
		tx:       &fakeTx{},
		pub:      &fakePublisher{},
	}
}

func (d *initiateDeps) uc() *InitiatePayoutUseCase {
	return NewInitiatePayoutUseCase(d.gate, d.repo, d.balance, d.bank, d.transfer, d.tx, d.pub)
}

type fakeGate struct{ err error }

func (g *fakeGate) EnsurePayoutsAllowed(ctx context.Context, tenantID domain.TenantID) error {
	return g.err
}

func TestInitiatePayoutUseCase_Execute(t *testing.T) {
	ctx := context.Background()

	t.Run("tenant on hold or suspended cannot pay out", func(t *testing.T) {
		d := newInitiateDeps()
		d.gate.err = domain.ErrPayoutsNotAllowed

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 4_000, Currency: "ARS", IdempotencyKey: "k1"})
		if !errors.Is(err, domain.ErrPayoutsNotAllowed) {
			t.Fatalf("expected ErrPayoutsNotAllowed, got %v", err)
		}
		if len(d.repo.saved) != 0 || len(d.pub.events) != 0 {
			t.Error("no payout must be created nor published")
		}
	})

	t.Run("happy path confirms payout and publishes initiated + confirmed", func(t *testing.T) {
		d := newInitiateDeps()

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 4_000, Currency: "ARS", IdempotencyKey: "k1"})
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

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 2_500, Currency: "ARS", IdempotencyKey: "k1"})
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
			AccountNumber:  ba.AccountNumber(),
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

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Currency: "ARS", IdempotencyKey: "k1"})
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

		if _, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 10_000, Currency: "ARS", IdempotencyKey: "k1"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty currency defaults to ARS", func(t *testing.T) {
		d := newInitiateDeps()

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: "not-a-uuid", Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

			_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: tc.amount, Currency: "ARS", IdempotencyKey: "k1"})
			if !errors.Is(err, domain.ErrInsufficientBalance) {
				t.Fatalf("expected ErrInsufficientBalance, got %v", err)
			}
			// El payout se crea dentro de la tx y luego se hace rollback: no queda
			// persistido (saved/updates vacíos) y nunca se transfiere.
			if len(d.repo.saved) != 0 || len(d.repo.updates) != 0 || len(d.transfer.reqs) != 0 {
				t.Error("nothing should be persisted or transferred")
			}
			if d.tx.calls == 0 {
				t.Error("balance check must happen inside the advisory-lock transaction")
			}
		})
	}

	t.Run("invalid currency rejected before persisting", func(t *testing.T) {
		d := newInitiateDeps()

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, Currency: "PESOS", IdempotencyKey: "k1"})
		if !errors.Is(err, domain.ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
		// NewMoney falla dentro de la tx → rollback: nada persistido.
		if len(d.repo.saved) != 0 || len(d.repo.updates) != 0 {
			t.Error("nothing should be persisted")
		}
	})

	t.Run("save error aborts before transfer", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.saveErr = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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
		d.transfer.err = fmt.Errorf("%w: account closed", ErrBankRejected)

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if !errors.Is(err, domain.ErrPayoutRejected) {
			t.Fatalf("expected ErrPayoutRejected, got %v", err)
		}
		// El resultado acompaña al error: el handler responde 422 con el id.
		if res.Status != domain.StatusFailed.String() || res.PayoutID == "" {
			t.Errorf("result = %+v, want the failed payout", res)
		}
		if !errors.Is(err, d.transfer.err) {
			t.Errorf("transfer error not wrapped: %v", err)
		}

		saved := d.repo.saved[0]
		if saved.Status() != domain.StatusFailed {
			t.Errorf("status = %q, want failed", saved.Status())
		}
		if !strings.Contains(saved.FailureReason(), "account closed") {
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
		d.transfer.err = fmt.Errorf("%w: rejected", ErrBankRejected)
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2
		d.pub.err = errBoom
		d.pub.errAt = 2

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		// Antes el error de persistencia se descartaba y el payout quedaba
		// processing sin aviso; ahora se propaga (junto al rechazo) y el
		// recuperador lo resuelve consultando al banco.
		if !errors.Is(err, d.transfer.err) || !errors.Is(err, errBoom) {
			t.Fatalf("expected both the transfer and the persistence error, got %v", err)
		}
	})

	t.Run("bank account is looked up in the payout currency", func(t *testing.T) {
		d := newInitiateDeps()
		_, _ = d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, Currency: "ARS", IdempotencyKey: "k1"})
		if d.bank.lastCurrency != "ARS" {
			t.Errorf("provider currency = %q, want ARS", d.bank.lastCurrency)
		}
	})

	t.Run("transitions are conditional on their starting status", func(t *testing.T) {
		d := newInitiateDeps()
		if _, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.PayoutStatus{domain.StatusInitiated, domain.StatusProcessing}
		if !reflect.DeepEqual(d.repo.froms, want) {
			t.Errorf("update froms = %v, want %v", d.repo.froms, want)
		}
	})

	// El recuperador lo confirmó mientras el banco respondía: el Update
	// condicional falla y se informa el estado que quedó, sin error.
	t.Run("confirm resolved meanwhile by the recoverer returns the current state", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.updateErr = domain.ErrPayoutChanged
		d.repo.updateErrAt = 2

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if err != nil || res.PayoutID == "" {
			t.Fatalf("res = %+v err = %v, want the current payout without error", res, err)
		}
	})

	t.Run("unknown persistence error is returned (the recoverer resolves it)", func(t *testing.T) {
		d := newInitiateDeps()
		d.transfer.err = errors.New("dial tcp: i/o timeout")
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2
		if _, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"}); !errors.Is(err, errBoom) {
			t.Fatalf("expected the persistence error, got %v", err)
		}
	})

	t.Run("confirm update error is returned", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.updateErr = errBoom
		d.repo.updateErrAt = 2

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
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

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
	})

	// ── Idempotencia de negocio (§10.2) ────────────────────────────────────────

	t.Run("missing idempotency key is rejected", func(t *testing.T) {
		d := newInitiateDeps()

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100})
		if !errors.Is(err, domain.ErrIdempotencyKeyRequired) {
			t.Fatalf("expected ErrIdempotencyKeyRequired, got %v", err)
		}
		if len(d.repo.saved) != 0 || d.tx.calls != 0 || len(d.transfer.reqs) != 0 {
			t.Error("nothing should be persisted or transferred")
		}
	})

	t.Run("retry with same key replays existing payout without second transfer", func(t *testing.T) {
		d := newInitiateDeps()
		cmd := InitiatePayoutCmd{TenantID: tenantA, Amount: 4_000, Currency: "ARS", IdempotencyKey: "same-key"}

		first, err := d.uc().Execute(ctx, cmd)
		if err != nil {
			t.Fatalf("first execute: %v", err)
		}
		if first.Replayed {
			t.Error("first call should not be a replay")
		}

		second, err := d.uc().Execute(ctx, cmd)
		if err != nil {
			t.Fatalf("retry execute: %v", err)
		}
		if !second.Replayed {
			t.Error("retry should be flagged as replay")
		}
		if second.PayoutID != first.PayoutID {
			t.Errorf("replay returned different payout: %q vs %q", second.PayoutID, first.PayoutID)
		}
		if second.Status != domain.StatusConfirmed.String() {
			t.Errorf("replay status = %q, want confirmed (final state)", second.Status)
		}
		// Un solo payout persistido y una sola transferencia al banco.
		if len(d.repo.saved) != 1 {
			t.Errorf("expected 1 saved payout, got %d", len(d.repo.saved))
		}
		if len(d.transfer.reqs) != 1 {
			t.Errorf("expected exactly 1 bank transfer, got %d", len(d.transfer.reqs))
		}
	})

	t.Run("same key with different amount returns conflict", func(t *testing.T) {
		d := newInitiateDeps()
		if _, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 4_000, Currency: "ARS", IdempotencyKey: "dup"}); err != nil {
			t.Fatalf("first execute: %v", err)
		}

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 5_000, Currency: "ARS", IdempotencyKey: "dup"})
		if !errors.Is(err, domain.ErrIdempotencyKeyConflict) {
			t.Fatalf("expected ErrIdempotencyKeyConflict, got %v", err)
		}
		if len(d.transfer.reqs) != 1 {
			t.Error("conflicting retry must not trigger a second transfer")
		}
	})

	t.Run("lookup by key error aborts before balance check", func(t *testing.T) {
		d := newInitiateDeps()
		d.repo.findKeyErr = errBoom

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if !errors.Is(err, errBoom) {
			t.Fatalf("expected errBoom, got %v", err)
		}
		if !strings.Contains(err.Error(), "lookup idempotency key") {
			t.Errorf("error not wrapped with context: %v", err)
		}
		if len(d.transfer.reqs) != 0 {
			t.Error("transfer should not run when lookup fails")
		}
	})

	// ── Resultado indeterminado de transferencia (§10.3) ───────────────────────

	t.Run("timeout marks payout unknown and does not revert funds", func(t *testing.T) {
		d := newInitiateDeps()
		d.transfer.err = fmt.Errorf("call bank: %w", context.DeadlineExceeded)

		res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if err == nil || !errors.Is(err, domain.ErrBankTransferUnknown) {
			t.Fatalf("expected ErrBankTransferUnknown, got %v", err)
		}
		if res.Status != domain.StatusUnknown.String() {
			t.Errorf("status = %q, want unknown", res.Status)
		}
		saved := d.repo.saved[0]
		if saved.Status() != domain.StatusUnknown {
			t.Errorf("aggregate status = %q, want unknown", saved.Status())
		}
		// No se emite payout.failed.v1: los fondos quedan en payout_transit.
		wantEvents := []string{"payout.initiated.v1", "payout.unknown.v1"}
		if got := d.pub.eventTypes(); !reflect.DeepEqual(got, wantEvents) {
			t.Errorf("events = %v, want %v", got, wantEvents)
		}
		wantUpdates := []domain.PayoutStatus{domain.StatusProcessing, domain.StatusUnknown}
		if !reflect.DeepEqual(d.repo.updates, wantUpdates) {
			t.Errorf("updates = %v, want %v", d.repo.updates, wantUpdates)
		}
	})

	// Un error que no es un rechazo explícito (5xx, error imprevisto del
	// SDK) revertía el asiento aunque el banco hubiera pagado.
	for _, transferErr := range []error{errors.New("dial tcp: i/o timeout"), errors.New("bank api: 503 service unavailable"), errors.New("sdk: unexpected response")} {
		t.Run("error that is not an explicit rejection goes to unknown: "+transferErr.Error(), func(t *testing.T) {
			d := newInitiateDeps()
			d.transfer.err = transferErr

			res, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
			if !errors.Is(err, domain.ErrBankTransferUnknown) {
				t.Fatalf("expected ErrBankTransferUnknown, got %v", err)
			}
			if res.Status != domain.StatusUnknown.String() {
				t.Errorf("status = %q, want unknown", res.Status)
			}
			if got := d.pub.eventTypes(); len(got) != 2 || got[1] != "payout.unknown.v1" {
				t.Errorf("events = %v, want initiated + unknown (no reversal)", got)
			}
		})
	}

	t.Run("explicit rejection fails definitively", func(t *testing.T) {
		d := newInitiateDeps()
		d.transfer.err = fmt.Errorf("%w: CBU invalido", ErrBankRejected)

		_, err := d.uc().Execute(ctx, InitiatePayoutCmd{TenantID: tenantA, Amount: 100, IdempotencyKey: "k1"})
		if errors.Is(err, domain.ErrBankTransferUnknown) {
			t.Fatal("explicit rejection must not be treated as unknown")
		}
		if d.repo.saved[0].Status() != domain.StatusFailed {
			t.Errorf("status = %q, want failed", d.repo.saved[0].Status())
		}
	})
}
