package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/onboarding/domain"
)

// TxManager abstrae las transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// ApplicationRepository persiste y recupera el agregado OnboardingApplication.
// Persiste el aggregate completo incluyendo documentos, personas y cuenta bancaria.
type ApplicationRepository interface {
	Save(ctx context.Context, app *domain.OnboardingApplication) error
	Update(ctx context.Context, app *domain.OnboardingApplication) error
	FindByID(ctx context.Context, id domain.ApplicationID) (*domain.OnboardingApplication, error)
	FindByTenantID(ctx context.Context, tenantID domain.TenantID) (*domain.OnboardingApplication, error)
}

// EventPublisher publica eventos hacia el Outbox.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}
