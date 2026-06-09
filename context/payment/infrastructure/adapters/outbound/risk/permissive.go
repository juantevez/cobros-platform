// Package risk provee evaluadores de riesgo para el contexto Payment.
package risk

import (
	"context"

	"github.com/juantevez/cobros-platform/context/payment/application"
	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// PermissiveEvaluator aprueba todos los pagos.
// Usar en Fase 2 hasta implementar el módulo Fraud & Risk real.
type PermissiveEvaluator struct{}

func NewPermissiveEvaluator() *PermissiveEvaluator { return &PermissiveEvaluator{} }

func (e *PermissiveEvaluator) Evaluate(_ context.Context, _ *domain.Payment) (application.RiskDecision, error) {
	return application.RiskDecision{
		Approved: true,
		Reason:   "",
		Score:    0,
	}, nil
}
