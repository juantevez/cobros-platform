package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/auth/domain"
)

// ComplianceHoldCmd identifica el hold por (tenant, alerta de cumplimiento).
type ComplianceHoldCmd struct {
	TenantID string
	AlertID  string
}

func (c ComplianceHoldCmd) parse() (domain.TenantID, string, error) {
	tenantID, err := domain.ParseTenantID(c.TenantID)
	if err != nil {
		return "", "", err
	}
	if _, err := uuid.Parse(c.AlertID); err != nil {
		return "", "", fmt.Errorf("compliance hold: invalid alert id %q", c.AlertID)
	}
	return tenantID, c.AlertID, nil
}

// PlaceComplianceHoldUseCase pone al comercio en hold preventivo ante un match
// exacto en lista de sanciones (compliance.sanction.matched.v1). El hold no
// cambia el estado del tenant (el KYC puede seguir su curso); los gates de
// Payment y Payout lo consultan y bloquean cobros y desembolsos.
//
// No publica eventos: auth.tenant.* se reenvía al comercio y anunciar el hold
// sería tipping-off. La traza queda en los eventos de Compliance.
type PlaceComplianceHoldUseCase struct {
	holds ComplianceHoldRepository
	clock Clock
}

func NewPlaceComplianceHoldUseCase(holds ComplianceHoldRepository, clock Clock) *PlaceComplianceHoldUseCase {
	return &PlaceComplianceHoldUseCase{holds: holds, clock: clock}
}

func (uc *PlaceComplianceHoldUseCase) Execute(ctx context.Context, cmd ComplianceHoldCmd) error {
	tenantID, alertID, err := cmd.parse()
	if err != nil {
		return err
	}
	return uc.holds.Place(ctx, tenantID, alertID, uc.clock.Now())
}

// ReleaseComplianceHoldUseCase levanta el hold de la alerta cuando
// cumplimiento la descarta como falso positivo (compliance.sanction.cleared.v1).
type ReleaseComplianceHoldUseCase struct {
	holds ComplianceHoldRepository
	clock Clock
}

func NewReleaseComplianceHoldUseCase(holds ComplianceHoldRepository, clock Clock) *ReleaseComplianceHoldUseCase {
	return &ReleaseComplianceHoldUseCase{holds: holds, clock: clock}
}

func (uc *ReleaseComplianceHoldUseCase) Execute(ctx context.Context, cmd ComplianceHoldCmd) error {
	tenantID, alertID, err := cmd.parse()
	if err != nil {
		return err
	}
	return uc.holds.Release(ctx, tenantID, alertID, uc.clock.Now())
}

// SuspendForComplianceUseCase suspende al comercio cuando cumplimiento
// confirma la coincidencia con sanciones (compliance.sanction.confirmed.v1).
// El hold se conserva: la suspensión es definitiva.
//
// Usa ComplianceReviewReason (genérico) porque auth.tenant.suspended.v1 llega
// al comercio. Es idempotente: un tenant ya suspendido es un no-op.
type SuspendForComplianceUseCase struct {
	suspend *SuspendTenantUseCase
}

func NewSuspendForComplianceUseCase(suspend *SuspendTenantUseCase) *SuspendForComplianceUseCase {
	return &SuspendForComplianceUseCase{suspend: suspend}
}

func (uc *SuspendForComplianceUseCase) Execute(ctx context.Context, cmd ComplianceHoldCmd) error {
	if _, _, err := cmd.parse(); err != nil {
		return err
	}
	err := uc.suspend.Execute(ctx, SuspendTenantCmd{
		TenantID: cmd.TenantID,
		Reason:   domain.ComplianceReviewReason,
	})
	if errors.Is(err, domain.ErrTenantCannotTransition) {
		return nil // ya suspendido → re-entrega o suspensión previa
	}
	return err
}
