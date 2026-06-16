package application

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/payout/domain"
)

const defaultPayoutLimit = 20

// GetPayoutUseCase consulta el estado de un payout.
type GetPayoutUseCase struct{ repo PayoutRepository }

func NewGetPayoutUseCase(repo PayoutRepository) *GetPayoutUseCase {
	return &GetPayoutUseCase{repo: repo}
}

func (uc *GetPayoutUseCase) Execute(ctx context.Context, q GetPayoutQuery) (PayoutView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return PayoutView{}, err
	}
	payoutID, err := domain.ParsePayoutID(q.PayoutID)
	if err != nil {
		return PayoutView{}, err
	}

	p, err := uc.repo.FindByID(ctx, payoutID)
	if err != nil {
		return PayoutView{}, err
	}
	if p.TenantID() != tenantID {
		return PayoutView{}, domain.ErrPayoutNotFound
	}
	return toView(p), nil
}

// ListPayoutsUseCase lista los payouts de un tenant.
type ListPayoutsUseCase struct{ repo PayoutRepository }

func NewListPayoutsUseCase(repo PayoutRepository) *ListPayoutsUseCase {
	return &ListPayoutsUseCase{repo: repo}
}

func (uc *ListPayoutsUseCase) Execute(ctx context.Context, q ListPayoutsQuery) ([]PayoutView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPayoutLimit
	}

	payouts, err := uc.repo.ListByTenant(ctx, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list payouts: %w", err)
	}

	views := make([]PayoutView, len(payouts))
	for i, p := range payouts {
		views[i] = toView(p)
	}
	return views, nil
}

func toView(p *domain.Payout) PayoutView {
	ba := p.BankAccount()
	return PayoutView{
		ID:              p.ID().String(),
		TenantID:        p.TenantID().String(),
		Amount:          p.Amount().Amount(),
		Currency:        p.Amount().Currency(),
		Status:          p.Status().String(),
		BankAccountType: ba.AccountType,
		BankAccountNum:  ba.AccountNumber,
		HolderName:      ba.HolderName,
		BankReference:   p.BankReference(),
		FailureReason:   p.FailureReason(),
		InitiatedAt:     p.InitiatedAt(),
		ConfirmedAt:     p.ConfirmedAt(),
		FailedAt:        p.FailedAt(),
		CreatedAt:       p.CreatedAt().Format(time.RFC3339),
	}
}
