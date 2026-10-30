package nats

import (
	"context"
	"errors"

	"github.com/juantevez/cobros-platform/context/ledger/domain"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// invalidEventErrors son los errores del dominio que un reintento no resuelve:
// el evento trae datos que el Ledger nunca va a aceptar. Una cuenta que no se
// encuentra NO está acá: puede crearla un evento de Onboarding que todavía no
// se procesó.
var invalidEventErrors = []error{
	domain.ErrInvalidID,
	domain.ErrIdempotencyKeyConflict,
	domain.ErrEntryNotBalanced,
	domain.ErrCurrencyMismatch,
	domain.ErrAccountCurrencyMismatch,
	domain.ErrNotEnoughPostings,
	domain.ErrZeroAmount,
	domain.ErrNegativeAmount,
	domain.ErrInvalidDirection,
	domain.ErrInvalidCurrency,
	domain.ErrInvalidAccountType,
}

// strict envuelve un handler para que los errores de datos inválidos se
// descarten como permanentes (con log) en lugar de reintentarse hasta
// MaxDeliver.
func strict(h eventbus.Handler) eventbus.Handler {
	return func(ctx context.Context, msg *eventbus.Message) error {
		err := h(ctx, msg)
		for _, target := range invalidEventErrors {
			if errors.Is(err, target) {
				return eventbus.Permanent(err)
			}
		}
		return err
	}
}
