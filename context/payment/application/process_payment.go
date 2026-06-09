package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// ProcessPaymentUseCase orquesta el procesamiento de un cobro.
//
// Flujo (Saga simplificada para Fase 2):
//
//  1. Verificar idempotencia → retornar existente si ya procesado.
//  2. Validar inputs y construir el agregado Payment.
//  3. Persistir en estado "initiated" (fuera de tx — checkpoint temprano).
//  4. Evaluar riesgo → si rechazado, fallar y publicar PaymentFailed.
//  5. Calcular comisión de la plataforma.
//  6. Seleccionar PSP según método de pago del tenant.
//  7. Marcar como "processing" y persistir.
//  8. Ejecutar captura contra el PSP.
//  9. Si PSP falla → Payment.Fail() → publicar PaymentFailed.
// 10. Si PSP ok → Payment.Capture() → RunInTx { Update + Publish PaymentCaptured }.
//
// Nota sobre consistencia: el Ledger registra el asiento al recibir PaymentCaptured
// via NATS (consistencia eventual). No hay llamada directa al Ledger desde aquí.
type ProcessPaymentUseCase struct {
	repo          PaymentRepository
	pspRouter     PSPRouter
	riskEvaluator RiskEvaluator
	feeCalculator FeeCalculator
	txManager     TxManager
	publisher     EventPublisher
}

func NewProcessPaymentUseCase(
	repo PaymentRepository,
	pspRouter PSPRouter,
	riskEvaluator RiskEvaluator,
	feeCalculator FeeCalculator,
	txManager TxManager,
	publisher EventPublisher,
) *ProcessPaymentUseCase {
	return &ProcessPaymentUseCase{
		repo:          repo,
		pspRouter:     pspRouter,
		riskEvaluator: riskEvaluator,
		feeCalculator: feeCalculator,
		txManager:     txManager,
		publisher:     publisher,
	}
}

func (uc *ProcessPaymentUseCase) Execute(ctx context.Context, cmd ProcessPaymentCmd) (ProcessPaymentResult, error) {
	// ── 1. Parsear y validar inputs ──────────────────────────────────────────

	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return ProcessPaymentResult{}, err
	}

	if cmd.IdempotencyKey == "" {
		return ProcessPaymentResult{}, fmt.Errorf("idempotency_key is required")
	}

	amount, err := domain.NewMoney(cmd.Amount, cmd.Currency)
	if err != nil {
		return ProcessPaymentResult{}, err
	}

	method, err := domain.ParsePaymentMethod(cmd.PaymentMethod)
	if err != nil {
		return ProcessPaymentResult{}, err
	}

	// ── 2. Idempotencia ──────────────────────────────────────────────────────

	existing, err := uc.repo.FindByIdempotencyKey(ctx, tenantID, cmd.IdempotencyKey)
	if err != nil && !errors.Is(err, domain.ErrPaymentNotFound) {
		return ProcessPaymentResult{}, fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil {
		return toResult(existing, true), nil
	}

	// ── 3. Construir y persistir el Payment en estado initiated ──────────────

	id := domain.NewPaymentID()
	payment, err := domain.NewPayment(
		id, tenantID, cmd.IdempotencyKey, amount,
		domain.PayerInfo{
			Name:    cmd.PayerName,
			Email:   cmd.PayerEmail,
			DocType: cmd.PayerDocType,
			DocNum:  cmd.PayerDocNum,
		},
		method,
		cmd.Metadata,
	)
	if err != nil {
		return ProcessPaymentResult{}, err
	}

	if err := uc.repo.Save(ctx, payment); err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("save payment: %w", err)
	}

	// ── 4. Evaluación de riesgo ──────────────────────────────────────────────

	decision, err := uc.riskEvaluator.Evaluate(ctx, payment)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("risk evaluation: %w", err)
	}

	if !decision.Approved {
		_ = payment.RejectByRisk(decision.Reason)
		if updateErr := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.repo.Update(ctx, payment); err != nil {
				return err
			}
			return uc.publisher.Publish(ctx, payment.PullEvents()...)
		}); updateErr != nil {
			return ProcessPaymentResult{}, fmt.Errorf("persist risk rejection: %w", updateErr)
		}
		return ProcessPaymentResult{}, domain.ErrRiskRejected
	}

	// ── 5. Calcular comisión ─────────────────────────────────────────────────

	platformFee, err := uc.feeCalculator.Calculate(tenantID, amount, method)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("calculate fee: %w", err)
	}

	// ── 6. Seleccionar PSP y marcar como processing ──────────────────────────

	psp, err := uc.pspRouter.Route(ctx, method, tenantID)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("route psp: %w", err)
	}

	if err := payment.MarkProcessing(); err != nil {
		return ProcessPaymentResult{}, err
	}
	if err := uc.repo.Update(ctx, payment); err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("update to processing: %w", err)
	}

	// ── 7. Ejecutar captura contra el PSP ────────────────────────────────────

	captureResult, pspErr := psp.AuthorizeAndCapture(ctx, PSPCaptureRequest{
		PaymentID:      id.String(),
		IdempotencyKey: cmd.IdempotencyKey,
		Amount:         amount.Amount(),
		Currency:       amount.Currency(),
		PaymentMethod:  method.String(),
		PaymentToken:   cmd.PaymentToken,
		Description:    cmd.Description,
		PayerInfo:      payment.PayerInfo(),
		Metadata:       cmd.Metadata,
	})

	// ── 8. PSP rechazó ───────────────────────────────────────────────────────

	if pspErr != nil {
		_ = payment.Fail(psp.Name(), pspErr.Error())
		if updateErr := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.repo.Update(ctx, payment); err != nil {
				return err
			}
			return uc.publisher.Publish(ctx, payment.PullEvents()...)
		}); updateErr != nil {
			return ProcessPaymentResult{}, fmt.Errorf("persist psp failure: %w", updateErr)
		}
		return ProcessPaymentResult{}, fmt.Errorf("psp rejected: %w", pspErr)
	}

	// ── 9. PSP aprobó: capturar y publicar ───────────────────────────────────

	pspFee := domain.ReconstituteMoney(captureResult.PSPFee, amount.Currency())

	if err := payment.Capture(psp.Name(), captureResult.PSPReference, platformFee, pspFee); err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("capture payment: %w", err)
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, payment); err != nil {
			return fmt.Errorf("update captured: %w", err)
		}
		// PaymentCapturedEvent en outbox → Ledger lo consume y registra el asiento.
		return uc.publisher.Publish(ctx, payment.PullEvents()...)
	}); err != nil {
		// CRÍTICO: el PSP capturó pero no pudimos persistir.
		// El job de reconciliación detectará esta discrepancia.
		return ProcessPaymentResult{}, fmt.Errorf("persist capture: %w", err)
	}

	return toResult(payment, false), nil
}

func toResult(p *domain.Payment, wasExisting bool) ProcessPaymentResult {
	return ProcessPaymentResult{
		PaymentID:    p.ID().String(),
		Status:       p.Status().String(),
		PSPReference: p.PSPReference(),
		Amount:       p.Amount().Amount(),
		Currency:     p.Amount().Currency(),
		PlatformFee:  p.PlatformFee().Amount(),
		CapturedAt:   p.CapturedAt(),
		WasExisting:  wasExisting,
	}
}
