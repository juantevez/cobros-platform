package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// RefundPaymentUseCase procesa el reembolso de un pago capturado.
type RefundPaymentUseCase struct {
	repo      PaymentRepository
	pspRouter PSPRouter
	txManager TxManager
	publisher EventPublisher
}

func NewRefundPaymentUseCase(
	repo PaymentRepository,
	pspRouter PSPRouter,
	txManager TxManager,
	publisher EventPublisher,
) *RefundPaymentUseCase {
	return &RefundPaymentUseCase{
		repo:      repo,
		pspRouter: pspRouter,
		txManager: txManager,
		publisher: publisher,
	}
}

func (uc *RefundPaymentUseCase) Execute(ctx context.Context, cmd RefundPaymentCmd) (RefundPaymentResult, error) {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return RefundPaymentResult{}, err
	}

	paymentID, err := domain.ParsePaymentID(cmd.PaymentID)
	if err != nil {
		return RefundPaymentResult{}, err
	}

	payment, err := uc.repo.FindByID(ctx, paymentID)
	if err != nil {
		return RefundPaymentResult{}, fmt.Errorf("find payment: %w", err)
	}

	// Aislamiento: el pago debe pertenecer al tenant del caller.
	if payment.TenantID() != tenantID {
		return RefundPaymentResult{}, domain.ErrPaymentNotFound
	}

	// Validación: solo se pueden reembolsar pagos capturados.
	if payment.Status() != domain.StatusCaptured {
		return RefundPaymentResult{}, domain.ErrNotCaptured
	}

	// Ejecutar el reembolso contra el PSP.
	psp, err := uc.pspRouter.Route(ctx, payment.Method(), tenantID)
	if err != nil {
		return RefundPaymentResult{}, fmt.Errorf("route psp: %w", err)
	}

	refundResult, err := psp.Refund(ctx, PSPRefundRequest{
		OriginalPSPReference: payment.PSPReference(),
		IdempotencyKey:       "refund_" + payment.IdempotencyKey(),
		Amount:               payment.Amount().Amount(),
		Currency:             payment.Amount().Currency(),
	})
	if err != nil {
		return RefundPaymentResult{}, fmt.Errorf("psp refund: %w", err)
	}

	if err := payment.Refund(refundResult.PSPReference); err != nil {
		return RefundPaymentResult{}, err
	}

	// Update condicional a captured: de dos reembolsos concurrentes solo uno
	// escribe y publica payment.refunded.v1. Los dos llamaron al PSP con la
	// misma clave (refund_<clave>), así que el PSP reembolsa una sola vez.
	if err := uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.Update(ctx, payment, domain.StatusCaptured); err != nil {
			return fmt.Errorf("update refunded: %w", err)
		}
		return uc.publisher.Publish(ctx, payment.PullEvents()...)
	}); err != nil {
		if !errors.Is(err, domain.ErrPaymentChanged) {
			return RefundPaymentResult{}, err
		}
		// Otro reembolso ganó: se informa cómo quedó el pago.
		current, findErr := uc.repo.FindByID(ctx, paymentID)
		if findErr != nil {
			return RefundPaymentResult{}, fmt.Errorf("reload refunded payment: %w", findErr)
		}
		if current.Status() != domain.StatusRefunded {
			return RefundPaymentResult{}, err
		}
		payment = current
	}

	return RefundPaymentResult{
		PaymentID:    payment.ID().String(),
		PSPReference: payment.RefundPSPReference(),
		Status:       payment.Status().String(),
	}, nil
}

// ── GetPayment ────────────────────────────────────────────────────────────────

// GetPaymentUseCase consulta el estado de un pago.
type GetPaymentUseCase struct {
	repo PaymentRepository
}

func NewGetPaymentUseCase(repo PaymentRepository) *GetPaymentUseCase {
	return &GetPaymentUseCase{repo: repo}
}

func (uc *GetPaymentUseCase) Execute(ctx context.Context, q GetPaymentQuery) (PaymentView, error) {
	tenantID, err := domain.ParseTenantID(q.TenantID)
	if err != nil {
		return PaymentView{}, err
	}

	paymentID, err := domain.ParsePaymentID(q.PaymentID)
	if err != nil {
		return PaymentView{}, err
	}

	p, err := uc.repo.FindByID(ctx, paymentID)
	if err != nil {
		return PaymentView{}, err
	}

	if p.TenantID() != tenantID {
		return PaymentView{}, domain.ErrPaymentNotFound
	}

	return PaymentView{
		ID:                 p.ID().String(),
		TenantID:           p.TenantID().String(),
		Status:             p.Status().String(),
		Amount:             p.Amount().Amount(),
		Currency:           p.Amount().Currency(),
		PlatformFee:        p.PlatformFee().Amount(),
		PSPFee:             p.PSPFee().Amount(),
		NetAmount:          p.NetAmount(),
		PaymentMethod:      p.Method().String(),
		PSPName:            p.PSPName(),
		PSPReference:       p.PSPReference(),
		FailureReason:      p.FailureReason(),
		IdempotencyKey:     p.IdempotencyKey(),
		CapturedAt:         p.CapturedAt(),
		FailedAt:           p.FailedAt(),
		RefundPSPReference: p.RefundPSPReference(),
		RefundedAt:         p.RefundedAt(),
		CreatedAt:          p.CreatedAt().Format(time.RFC3339),
	}, nil
}
