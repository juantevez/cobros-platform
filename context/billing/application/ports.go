package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/billing/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// Page es la paginación estándar de los listados: limit ∈ [1, MaxPageSize],
// offset ≥ 0. Total es el conteo total de filas (ignorando limit/offset).
type Page struct {
	Limit  int
	Offset int
	Total  int64
}

// MaxPageSize acota los listados para evitar consultas sin límite.
const MaxPageSize = 100

// Normalize aplica los defaults: limit ausente → 20; fuera de rango → se acota.
func (p *Page) Normalize() {
	if p.Limit <= 0 {
		p.Limit = 20
	}
	if p.Limit > MaxPageSize {
		p.Limit = MaxPageSize
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
}

// PlanListFilter filtra el listado del catálogo de planes.
// IncludeInactive=true lista también los planes desactivados (gestión).
type PlanListFilter struct {
	Page
	IncludeInactive bool
}

// PlanRepository persiste y recupera PricingPlans.
type PlanRepository interface {
	Save(ctx context.Context, p *domain.PricingPlan) error
	Update(ctx context.Context, p *domain.PricingPlan) error
	FindByID(ctx context.Context, id domain.PlanID) (*domain.PricingPlan, error)
	ListActive(ctx context.Context) ([]*domain.PricingPlan, error)
	// ListPage pagina el catálogo ordenado por nombre. filter.IncludeInactive
	// controla si se incluyen los planes desactivados.
	ListPage(ctx context.Context, filter PlanListFilter) ([]*domain.PricingPlan, Page, error)
}

// TenantPlanRepository persiste y recupera asignaciones de planes a tenants.
type TenantPlanRepository interface {
	Save(ctx context.Context, tp *domain.TenantPlan) error
	Update(ctx context.Context, tp *domain.TenantPlan) error
	// FindActive retorna el TenantPlan activo del tenant, si existe.
	// Retorna ErrTenantPlanNotFound si no hay ninguno activo.
	FindActive(ctx context.Context, tenantID domain.TenantID) (*domain.TenantPlan, error)
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.TenantPlan, error)
	// ListByTenantPage pagina el historial de asignaciones del tenant
	// (activas e inactivas), ordenado por valid_from DESC.
	ListByTenantPage(ctx context.Context, tenantID domain.TenantID, page Page) ([]*domain.TenantPlan, Page, error)
}

// EventPublisher publica eventos de dominio hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}
