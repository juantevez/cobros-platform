package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// PlanRepository persiste y recupera PricingPlans.
type PlanRepository interface {
	Save(ctx context.Context, p *domain.PricingPlan) error
	Update(ctx context.Context, p *domain.PricingPlan) error
	FindByID(ctx context.Context, id domain.PlanID) (*domain.PricingPlan, error)
	ListActive(ctx context.Context) ([]*domain.PricingPlan, error)
}

// TenantPlanRepository persiste y recupera asignaciones de planes a tenants.
type TenantPlanRepository interface {
	Save(ctx context.Context, tp *domain.TenantPlan) error
	Update(ctx context.Context, tp *domain.TenantPlan) error
	// FindActive retorna el TenantPlan activo del tenant, si existe.
	// Retorna ErrTenantPlanNotFound si no hay ninguno activo.
	FindActive(ctx context.Context, tenantID domain.TenantID) (*domain.TenantPlan, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.TenantPlan, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}
