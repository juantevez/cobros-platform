package application

import (
	"context"
	"errors"
	"testing"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

var errBoom = errors.New("boom")

func TestCreatePlan_Success(t *testing.T) {
	repo := newFakePlanRepo()
	pub := &fakePublisher{}
	uc := NewCreatePlanUseCase(repo, fakeTx{}, pub)

	res, err := uc.Execute(context.Background(), CreatePlanCmd{
		Name:            "Plan Pro",
		Description:     "premium",
		BaseRateBps:     250,
		BaseFixedAmount: 50,
		MonthlyFee:      1000,
		Currency:        "ARS",
		MethodRates: []MethodRateInput{
			{Method: "card", RateBps: 300, FixedAmount: 60},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PlanID == "" {
		t.Fatal("expected a plan id")
	}
	if repo.saved == nil || repo.saved.Name() != "Plan Pro" {
		t.Fatal("plan not saved with expected name")
	}
	if mr, ok := repo.saved.MethodRates()[domain.MethodCard]; !ok || mr.RateBps != 300 {
		t.Fatalf("expected card method rate 300, got %+v", repo.saved.MethodRates())
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 event, got %d", len(pub.published))
	}
	if _, ok := pub.published[0].(domain.PlanCreatedEvent); !ok {
		t.Fatalf("expected PlanCreatedEvent, got %T", pub.published[0])
	}
}

func TestCreatePlan_ValidationErrors(t *testing.T) {
	uc := NewCreatePlanUseCase(newFakePlanRepo(), fakeTx{}, &fakePublisher{})

	t.Run("empty name", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "", Currency: "ARS"})
		if !errors.Is(err, domain.ErrPlanNameEmpty) {
			t.Fatalf("expected ErrPlanNameEmpty, got %v", err)
		}
	})
	t.Run("invalid rate", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "P", BaseRateBps: 20000, Currency: "ARS"})
		if !errors.Is(err, domain.ErrInvalidRateBps) {
			t.Fatalf("expected ErrInvalidRateBps, got %v", err)
		}
	})
	t.Run("invalid currency", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "P", Currency: "XX"})
		if !errors.Is(err, domain.ErrInvalidCurrency) {
			t.Fatalf("expected ErrInvalidCurrency, got %v", err)
		}
	})
}

func TestCreatePlan_InvalidMethodRate(t *testing.T) {
	uc := NewCreatePlanUseCase(newFakePlanRepo(), fakeTx{}, &fakePublisher{})

	t.Run("unknown method", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreatePlanCmd{
			Name: "P", Currency: "ARS",
			MethodRates: []MethodRateInput{{Method: "crypto", RateBps: 100}},
		})
		if !errors.Is(err, domain.ErrInvalidPaymentMethod) {
			t.Fatalf("expected ErrInvalidPaymentMethod, got %v", err)
		}
	})
	t.Run("out of range rate", func(t *testing.T) {
		_, err := uc.Execute(context.Background(), CreatePlanCmd{
			Name: "P", Currency: "ARS",
			MethodRates: []MethodRateInput{{Method: "card", RateBps: 99999}},
		})
		if !errors.Is(err, domain.ErrInvalidRateBps) {
			t.Fatalf("expected ErrInvalidRateBps, got %v", err)
		}
	})
}

func TestCreatePlan_SaveErrorPropagates(t *testing.T) {
	repo := newFakePlanRepo()
	repo.saveErr = errBoom
	uc := NewCreatePlanUseCase(repo, fakeTx{}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "P", Currency: "ARS"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected wrapped errBoom, got %v", err)
	}
}

func TestCreatePlan_PublisherErrorPropagates(t *testing.T) {
	pub := &fakePublisher{err: errBoom}
	uc := NewCreatePlanUseCase(newFakePlanRepo(), fakeTx{}, pub)

	_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "P", Currency: "ARS"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}

func TestCreatePlan_TxErrorPropagates(t *testing.T) {
	uc := NewCreatePlanUseCase(newFakePlanRepo(), fakeTx{err: errBoom}, &fakePublisher{})

	_, err := uc.Execute(context.Background(), CreatePlanCmd{Name: "P", Currency: "ARS"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
}
