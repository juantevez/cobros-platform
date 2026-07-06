package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
)

var errBoom = errors.New("boom")

func TestCreateAccount_Success(t *testing.T) {
	repo := newFakeAccountRepo()
	pub := &fakePublisher{}
	uc := NewCreateAccountUseCase(repo, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), CreateAccountCmd{
		TenantID:    validUUID(),
		AccountType: "merchant_balance",
		Currency:    "ARS",
		Description: "saldo comercio",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AccountID == "" {
		t.Fatal("expected an account id")
	}
	if repo.saved == nil || repo.saved.AccountType() != domain.AccountTypeMerchantBalance {
		t.Fatal("account not saved with expected type")
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.AccountCreatedEvent); !ok {
		t.Fatalf("expected AccountCreatedEvent, got %T", pub.published[0])
	}
}

func TestCreateAccount_ValidationErrors(t *testing.T) {
	uc := NewCreateAccountUseCase(newFakeAccountRepo(), fakeTx{}, &fakePublisher{})

	t.Run("invalid tenant id", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreateAccountCmd{TenantID: "nope", AccountType: "reserve", Currency: "ARS"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid account type", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreateAccountCmd{TenantID: validUUID(), AccountType: "chicken", Currency: "ARS"})
		if !errors.Is(err, domain.ErrInvalidAccountType) {
			t.Fatalf("expected ErrInvalidAccountType, got %v", err)
		}
	})
	t.Run("invalid currency", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreateAccountCmd{TenantID: validUUID(), AccountType: "reserve", Currency: "XX"})
		if !errors.Is(err, domain.ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
	})
}

func TestCreateAccount_SaveErrorPropagates(t *testing.T) {
	repo := newFakeAccountRepo()
	repo.saveErr = errBoom
	uc := NewCreateAccountUseCase(repo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), CreateAccountCmd{TenantID: validUUID(), AccountType: "reserve", Currency: "ARS"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestCreateAccount_PublisherErrorPropagates(t *testing.T) {
	pub := &fakePublisher{err: errBoom}
	uc := NewCreateAccountUseCase(newFakeAccountRepo(), fakeTx{}, pub)

	_, err := uc.Execute(context.Background(), CreateAccountCmd{TenantID: validUUID(), AccountType: "reserve", Currency: "ARS"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
