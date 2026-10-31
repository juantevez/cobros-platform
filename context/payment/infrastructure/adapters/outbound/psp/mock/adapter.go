// Package mock provee un adaptador PSP de prueba que siempre aprueba.
// Usar en desarrollo local y tests de integración.
// NUNCA usar en producción.
package mock

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/juantevez/cobros-platform/context/payment/application"
)

// Adapter es un PSP simulado que aprueba todos los pagos.
// Simula la estructura de respuesta de un PSP real sin llamadas externas.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "mock" }

func (a *Adapter) AuthorizeAndCapture(_ context.Context, req application.PSPCaptureRequest) (application.PSPCaptureResult, error) {
	// Tokens especiales para probar los dos tipos de fallo:
	//   REJECT  → rechazo definitivo.
	//   TIMEOUT → resultado desconocido (simula un timeout antes de capturar).
	switch req.PaymentToken {
	case "REJECT":
		return application.PSPCaptureResult{}, fmt.Errorf("%w: mock psp: payment rejected (test token)", application.ErrPSPDeclined)
	case "TIMEOUT":
		return application.PSPCaptureResult{}, fmt.Errorf("mock psp: request timed out (test token)")
	}

	// Simular comisión del PSP: 1.5% del monto
	pspFee := req.Amount * 15 / 1000
	if pspFee < 1 {
		pspFee = 1
	}

	return application.PSPCaptureResult{
		PSPReference: fmt.Sprintf("MOCK-%s", uuid.NewString()[:8]),
		PSPFee:       pspFee,
		Status:       "approved",
	}, nil
}

// GetCapture: el mock no guarda estado. El único resultado desconocido que
// produce es TIMEOUT, que simula un timeout antes de capturar: el PSP no tiene
// registro de la operación.
func (a *Adapter) GetCapture(_ context.Context, _ application.PSPCaptureLookupRequest) (application.PSPCaptureLookup, error) {
	return application.PSPCaptureLookup{Found: false}, nil
}

func (a *Adapter) Refund(_ context.Context, req application.PSPRefundRequest) (application.PSPRefundResult, error) {
	return application.PSPRefundResult{
		PSPReference: fmt.Sprintf("MOCK-REF-%s", uuid.NewString()[:8]),
	}, nil
}
