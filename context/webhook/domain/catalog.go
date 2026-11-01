package domain

import (
	"fmt"
	"slices"
	"strings"
)

// SubscribableEvents es el catálogo de tipos de evento (sin versión) que un
// endpoint puede suscribir: los que reenvía el consumer de Webhook (filtros
// payment.>, payout.>, onboarding.>, auth.tenant.> y dispute.>). Una suscripción fuera
// del catálogo (un typo, un tipo que no existe) daba un endpoint que nunca
// recibía nada, sin error.
//
// Al agregar un evento a esos streams que el comercio deba recibir, sumarlo
// acá. Un test del consumer verifica que cada entrada pase sus filtros.
var SubscribableEvents = []string{
	"payment.captured",
	"payment.failed",
	"payment.refunded",
	"payout.initiated",
	"payout.confirmed",
	"payout.failed",
	"payout.unknown",
	"onboarding.application.submitted",
	"onboarding.application.sent_for_review",
	"onboarding.application.approved",
	"onboarding.application.rejected",
	"onboarding.application.more_info",
	"onboarding.application.reopened",
	"auth.tenant.created",
	"auth.tenant.activated",
	"auth.tenant.suspended",
	"auth.tenant.reactivated",
	"dispute.opened",
	"dispute.resolved",
}

// validateSubscriptions exige que todos los tipos (ya normalizados) estén en
// el catálogo; el error nombra los rechazados y los permitidos.
func validateSubscriptions(events []string) error {
	var unknown []string
	for _, ev := range events {
		if !slices.Contains(SubscribableEvents, ev) {
			unknown = append(unknown, ev)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s (allowed: %s)", ErrUnknownEventType,
		strings.Join(unknown, ", "), strings.Join(SubscribableEvents, ", "))
}
