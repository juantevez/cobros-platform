// Package mock provee un adaptador de transferencia bancaria simulado.
// Siempre confirma la transferencia. NUNCA usar en producción.
package mock

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/juantevez/cobros-platform/context/payout/application"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return "mock_bank" }

func (a *Adapter) Transfer(_ context.Context, req application.TransferRequest) (application.TransferResult, error) {
	// Cuentas especiales para probar los dos tipos de fallo:
	//   REJECT  → rechazo definitivo del banco.
	//   TIMEOUT → resultado desconocido (simula un timeout).
	switch req.AccountNumber {
	case "REJECT":
		return application.TransferResult{}, fmt.Errorf("%w: mock bank: account rejected", application.ErrBankRejected)
	case "TIMEOUT":
		return application.TransferResult{}, fmt.Errorf("mock bank: request timed out")
	}

	return application.TransferResult{
		BankReference: fmt.Sprintf("MOCK-BANK-%s", uuid.NewString()[:8]),
		Status:        "confirmed",
	}, nil
}

// GetTransfer: el mock no guarda estado. Su único resultado desconocido es
// TIMEOUT, que simula un timeout antes de llegar al banco: no hay registro.
func (a *Adapter) GetTransfer(_ context.Context, _ application.TransferLookupRequest) (application.TransferLookup, error) {
	return application.TransferLookup{Found: false}, nil
}
