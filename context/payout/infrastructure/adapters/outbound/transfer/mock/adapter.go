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
	// Simular rechazo para cuenta especial en tests
	if req.AccountNumber == "REJECT" {
		return application.TransferResult{}, fmt.Errorf("mock bank: account rejected")
	}

	return application.TransferResult{
		BankReference: fmt.Sprintf("MOCK-BANK-%s", uuid.NewString()[:8]),
		Status:        "confirmed",
	}, nil
}
