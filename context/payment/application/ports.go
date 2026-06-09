package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/payment/domain"
)

// ── Repositorio ───────────────────────────────────────────────────────────────

type PaymentRepository interface {
	Save(ctx context.Context, p *domain.Payment) error
	Update(ctx context.Context, p *domain.Payment) error
	FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)
	FindByIdempotencyKey(ctx context.Context, tenantID domain.TenantID, key string) (*domain.Payment, error)
}

// ── PSP ───────────────────────────────────────────────────────────────────────

// PSPAdapter abstrae la comunicación con un proveedor de pago externo.
// Cada PSP (Mercado Pago, PayPal, adquirente) implementa esta interfaz.
// El núcleo de la aplicación nunca conoce al PSP concreto.
type PSPAdapter interface {
	// AuthorizeAndCapture captura fondos en un solo paso.
	// Es la operación más común para pagos de ecommerce.
	AuthorizeAndCapture(ctx context.Context, req PSPCaptureRequest) (PSPCaptureResult, error)
	// Refund devuelve fondos al pagador.
	Refund(ctx context.Context, req PSPRefundRequest) (PSPRefundResult, error)
	// Name retorna el identificador del PSP para logging y auditoría.
	Name() string
}

// PSPCaptureRequest contiene los datos necesarios para capturar un pago.
type PSPCaptureRequest struct {
	PaymentID      string
	IdempotencyKey string
	Amount         int64
	Currency       string
	PaymentMethod  string
	// Token generado por el SDK del PSP en el frontend. NUNCA datos de tarjeta en crudo.
	PaymentToken string
	Description  string
	PayerInfo    domain.PayerInfo
	Metadata     map[string]string
}

// PSPCaptureResult es la respuesta del PSP tras una captura exitosa.
type PSPCaptureResult struct {
	PSPReference string // ID de la transacción en el PSP
	PSPFee       int64  // comisión cobrada por el PSP (en centavos)
	Status       string // "approved", "pending", "rejected"
}

// PSPRefundRequest contiene los datos para un reembolso.
type PSPRefundRequest struct {
	OriginalPSPReference string
	IdempotencyKey       string
	Amount               int64
	Currency             string
}

// PSPRefundResult es la respuesta del PSP tras un reembolso.
type PSPRefundResult struct {
	PSPReference string
}

// PSPRouter selecciona el adaptador de PSP correcto según el método de pago
// y la configuración del tenant. En Fase 2 devuelve siempre el Mock.
type PSPRouter interface {
	Route(ctx context.Context, method domain.PaymentMethod, tenantID domain.TenantID) (PSPAdapter, error)
}

// ── Riesgo ────────────────────────────────────────────────────────────────────

// RiskEvaluator evalúa el riesgo de un pago antes de procesarlo.
// En Fase 2: implementación permisiva (siempre aprueba).
// En Fase 3: se conecta con el módulo Fraud & Risk real.
type RiskEvaluator interface {
	Evaluate(ctx context.Context, p *domain.Payment) (RiskDecision, error)
}

// RiskDecision es el resultado de la evaluación de riesgo.
type RiskDecision struct {
	Approved bool
	Reason   string // motivo de rechazo si !Approved
	Score    int    // 0-100; mayor = más riesgoso
}

// ── Comisiones ────────────────────────────────────────────────────────────────

// FeeCalculator calcula la comisión de la plataforma sobre un pago.
// En Fase 2: porcentaje fijo configurable.
// En Fase 3: se conecta con el módulo Billing & Fees real (planes por tenant).
type FeeCalculator interface {
	Calculate(tenantID domain.TenantID, amount domain.Money, method domain.PaymentMethod) (domain.Money, error)
}

// ── Outbox ────────────────────────────────────────────────────────────────────

// EventPublisher publica eventos de dominio hacia el Outbox transaccional.
type EventPublisher interface {
	Publish(ctx context.Context, events ...domain.Event) error
}

// TxManager abstrae las transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// Clock abstrae el acceso al tiempo.
type Clock interface {
	Now() time.Time
}
