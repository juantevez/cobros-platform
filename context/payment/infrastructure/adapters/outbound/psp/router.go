package psp

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
	"github.com/juantevez/cobros-platform/context/payment/infrastructure/adapters/outbound/psp/mock"
)

// Router selecciona el PSPAdapter correcto según el método de pago.
// En Fase 2 usa siempre el Mock. En Fase 3 enruta a MP, PayPal, etc.
type Router struct {
	adapters map[string]application.PSPAdapter
}

// NewRouter crea el Router con los adaptadores disponibles.
func NewRouter() *Router {
	r := &Router{
		adapters: make(map[string]application.PSPAdapter),
	}
	// Registrar adaptadores disponibles.
	mockPSP := mock.New()
	r.adapters[mockPSP.Name()] = mockPSP
	return r
}

// Route retorna el adaptador apropiado para el método de pago del tenant.
// En Fase 2: siempre retorna el Mock.
// En Fase 3: consultar la configuración del tenant para saber qué PSP usar.
func (r *Router) Route(_ context.Context, _ domain.PaymentMethod, _ domain.TenantID) (application.PSPAdapter, error) {
	adapter, ok := r.adapters["mock"]
	if !ok {
		return nil, fmt.Errorf("psp router: no adapter available")
	}
	return adapter, nil
}

// Register agrega un adaptador PSP al router.
// Llamar en el wiring de cmd/api al inicializar con los PSPs reales.
func (r *Router) Register(adapter application.PSPAdapter) {
	r.adapters[adapter.Name()] = adapter
}
