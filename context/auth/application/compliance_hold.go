package application

import (
	"context"
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
// El hold se conserva. La suspensión queda marcada como de cumplimiento: solo
// un compliance_officer puede levantarla (ReactivateTenant).
//
// Usa ComplianceReviewReason (genérico) porque auth.tenant.suspended.v1 llega
// al comercio. Es idempotente; si un operador ya lo había suspendido, no
// emite otro evento pero marca la suspensión como de cumplimiento.
type SuspendForComplianceUseCase struct {
	tenantRepo TenantRepository
	txManager  TxManager
	publisher  EventPublisher
}

func NewSuspendForComplianceUseCase(tenantRepo TenantRepository, txManager TxManager, publisher EventPublisher) *SuspendForComplianceUseCase {
	return &SuspendForComplianceUseCase{tenantRepo: tenantRepo, txManager: txManager, publisher: publisher}
}

func (uc *SuspendForComplianceUseCase) Execute(ctx context.Context, cmd ComplianceHoldCmd) error {
	tenantID, _, err := cmd.parse()
	if err != nil {
		return err
	}
	tenant, err := uc.tenantRepo.FindByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}
	if tenant.Status() == domain.TenantStatusSuspended && tenant.SuspendedForCompliance() {
		return nil // re-entrega
	}
	tenant.SuspendForCompliance()
	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.tenantRepo.Update(ctx, tenant); err != nil {
			return fmt.Errorf("update tenant: %w", err)
		}
		return uc.publisher.Publish(ctx, tenant.PullEvents()...)
	})
}
