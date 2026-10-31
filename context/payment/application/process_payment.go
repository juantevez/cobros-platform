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
//  1. Validar inputs.
//  2. Idempotencia → retornar el existente si ya avanzó; retomarlo si quedó en "initiated".
//  3. Verificar que el tenant esté habilitado para cobrar (TenantGate).
//  4. Construir el Payment y persistirlo en "initiated" (fuera de tx — checkpoint temprano).
//  5. Evaluar riesgo → si rechazado, fallar y publicar PaymentFailed.
//  6. Calcular comisión de la plataforma.
//  7. Seleccionar PSP y reclamar el pago (initiated → processing, UPDATE condicional).
//  8. Ejecutar captura contra el PSP.
//  9. Si PSP rechaza (ErrPSPDeclined) → Payment.Fail() → publicar PaymentFailed →
//     ErrPaymentDeclined. Si el resultado es desconocido (timeout, red) → el pago
//     queda en processing → ErrPaymentPending.
// 10. Si PSP ok → Payment.Capture() → RunInTx { Update + Publish PaymentCaptured }.
//
// Nota sobre consistencia: el Ledger registra el asiento al recibir PaymentCaptured
// via NATS (consistencia eventual). No hay llamada directa al Ledger desde aquí.
type ProcessPaymentUseCase struct {
	repo          PaymentRepository
	tenantGate    TenantGate
	pspRouter     PSPRouter
	riskEvaluator RiskEvaluator
	feeCalculator FeeCalculator
	txManager     TxManager
	publisher     EventPublisher
}

func NewProcessPaymentUseCase(
	repo PaymentRepository,
	tenantGate TenantGate,
	pspRouter PSPRouter,
	riskEvaluator RiskEvaluator,
	feeCalculator FeeCalculator,
	txManager TxManager,
	publisher EventPublisher,
) *ProcessPaymentUseCase {
	return &ProcessPaymentUseCase{
		repo:          repo,
		tenantGate:    tenantGate,
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
	// Un pago ya avanzado se devuelve tal cual. Uno que quedó en initiated
	// (falló riesgo, Billing o el ruteo antes de llegar al PSP) se retoma: si
	// se devolviera, ese pago no se procesaría nunca.

	existing, err := uc.repo.FindByIdempotencyKey(ctx, tenantID, cmd.IdempotencyKey)
	if err != nil && !errors.Is(err, domain.ErrPaymentNotFound) {
		return ProcessPaymentResult{}, fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil && existing.Status() != domain.StatusInitiated {
		return toResult(existing, true), nil
	}

	// ── 3. El comercio debe estar habilitado (KYC aprobado, no suspendido) ───

	if err := uc.tenantGate.EnsureCanProcessPayments(ctx, tenantID); err != nil {
		return ProcessPaymentResult{}, err
	}

	if existing != nil {
		return uc.process(ctx, existing, cmd, true)
	}

	// ── 4. Construir y persistir el Payment en estado initiated ──────────────

	payment, err := domain.NewPayment(
		domain.NewPaymentID(), tenantID, cmd.IdempotencyKey, amount,
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

	return uc.process(ctx, payment, cmd, false)
}

// process lleva un pago en initiated hasta su estado final: riesgo, comisión,
// claim de processing, captura en el PSP y persistencia del resultado.
//
// Los montos y el método salen del pago persistido (no del comando), para que
// un reintento con otros valores no altere un pago existente. Del comando solo
// se usan el token del PSP y la descripción, que no se persisten.
func (uc *ProcessPaymentUseCase) process(ctx context.Context, payment *domain.Payment, cmd ProcessPaymentCmd, wasExisting bool) (ProcessPaymentResult, error) {
	tenantID := payment.TenantID()
	amount := payment.Amount()
	method := payment.Method()

	// ── 5. Evaluación de riesgo ──────────────────────────────────────────────

	decision, err := uc.riskEvaluator.Evaluate(ctx, payment)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("risk evaluation: %w", err)
	}

	if !decision.Approved {
		_ = payment.RejectByRisk(decision.Reason)
		if updateErr := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.repo.Update(ctx, payment, domain.StatusInitiated); err != nil {
				return err
			}
			return uc.publisher.Publish(ctx, payment.PullEvents()...)
		}); updateErr != nil {
			return ProcessPaymentResult{}, fmt.Errorf("persist risk rejection: %w", updateErr)
		}
		return ProcessPaymentResult{}, domain.ErrRiskRejected
	}

	// ── 6. Calcular comisión ─────────────────────────────────────────────────

	platformFee, err := uc.feeCalculator.Calculate(ctx, tenantID, amount, method)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("calculate fee: %w", err)
	}

	// ── 7. Seleccionar PSP y reclamar el pago (initiated → processing) ───────

	psp, err := uc.pspRouter.Route(ctx, method, tenantID)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("route psp: %w", err)
	}

	if err := payment.MarkProcessing(platformFee); err != nil {
		return ProcessPaymentResult{}, err
	}
	claimed, err := uc.repo.ClaimProcessing(ctx, payment)
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("update to processing: %w", err)
	}
	if !claimed {
		// Otra ejecución (request original o reintento concurrente) ya lo está
		// procesando: no se vuelve a llamar al PSP; se informa el estado actual.
		current, err := uc.repo.FindByID(ctx, payment.ID())
		if err != nil {
			return ProcessPaymentResult{}, fmt.Errorf("reload claimed payment: %w", err)
		}
		return toResult(current, true), nil
	}

	// ── 8. Ejecutar captura contra el PSP ────────────────────────────────────

	captureResult, pspErr := psp.AuthorizeAndCapture(ctx, PSPCaptureRequest{
		PaymentID:      payment.ID().String(),
		IdempotencyKey: payment.IdempotencyKey(),
		Amount:         amount.Amount(),
		Currency:       amount.Currency(),
		PaymentMethod:  method.String(),
		PaymentToken:   cmd.PaymentToken,
		Description:    cmd.Description,
		PayerInfo:      payment.PayerInfo(),
		Metadata:       payment.Metadata(),
	})

	// ── 9. PSP rechazó o no confirmó ─────────────────────────────────────────

	if pspErr != nil && !errors.Is(pspErr, ErrPSPDeclined) {
		// Resultado desconocido: el PSP pudo haber capturado. Marcarlo failed
		// dejaba al pagador cobrado sin pago registrado (y un reintento con la
		// misma clave devolvía el failed). Queda en processing, como está en
		// la base desde el claim.
		return toResult(payment, wasExisting), fmt.Errorf("%w: %w", domain.ErrPaymentPending, pspErr)
	}
	if pspErr != nil {
		_ = payment.Fail(psp.Name(), pspErr.Error())
		if updateErr := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
			if err := uc.repo.Update(ctx, payment, domain.StatusProcessing); err != nil {
				return err
			}
			return uc.publisher.Publish(ctx, payment.PullEvents()...)
		}); updateErr != nil {
			return ProcessPaymentResult{}, fmt.Errorf("persist psp failure: %w", updateErr)
		}
		// Se retorna el resultado junto al error tipado: el handler responde
		// 402 con el payment_id y el estado, sin exponer el error del PSP.
		return toResult(payment, wasExisting), fmt.Errorf("%w: %w", domain.ErrPaymentDeclined, pspErr)
	}

	// ── 10. PSP aprobó: capturar y publicar ──────────────────────────────────

	pspFee := domain.ReconstituteMoney(captureResult.PSPFee, amount.Currency())

	if err := payment.Capture(psp.Name(), captureResult.PSPReference, platformFee, pspFee); err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("capture payment: %w", err)
	}

	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, payment, domain.StatusProcessing); err != nil {
			return fmt.Errorf("update captured: %w", err)
		}
		// PaymentCapturedEvent en outbox → Ledger lo consume y registra el asiento.
		return uc.publisher.Publish(ctx, payment.PullEvents()...)
	}); err != nil {
		if errors.Is(err, domain.ErrPaymentChanged) {
			// El recuperador lo resolvió mientras el PSP respondía: se informa
			// el estado que quedó.
			current, findErr := uc.repo.FindByID(ctx, payment.ID())
			if findErr != nil {
				return ProcessPaymentResult{}, fmt.Errorf("reload resolved payment: %w", findErr)
			}
			return toResult(current, wasExisting), nil
		}
		// El PSP capturó pero no pudimos persistir: el pago queda en
		// processing y lo resuelve el recuperador consultando al PSP.
		return ProcessPaymentResult{}, fmt.Errorf("persist capture: %w", err)
	}

	return toResult(payment, wasExisting), nil
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
