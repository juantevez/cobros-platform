package nats

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/juantevez/cobros-platform/context/auth/application"
	"github.com/juantevez/cobros-platform/pkg/eventbus"
)

// sanctionPayload es el payload común de compliance.sanction.*.v1.
type sanctionPayload struct {
	AlertID  string `json:"alert_id"`
	TenantID string `json:"tenant_id"`
}

// ComplianceConsumer aplica el enforcement de cumplimiento sobre el comercio:
//
//	compliance.sanction.matched.v1   → hold preventivo (bloquea cobros y payouts)
//	compliance.sanction.cleared.v1   → levanta el hold de esa alerta
//	compliance.sanction.confirmed.v1 → suspensión definitiva (motivo genérico)
type ComplianceConsumer struct {
	consumer eventbus.Consumer
	place    *application.PlaceComplianceHoldUseCase
	release  *application.ReleaseComplianceHoldUseCase
	suspend  *application.SuspendForComplianceUseCase
	logger   *slog.Logger
}

func NewComplianceConsumer(
	consumer eventbus.Consumer,
	place *application.PlaceComplianceHoldUseCase,
	release *application.ReleaseComplianceHoldUseCase,
	suspend *application.SuspendForComplianceUseCase,
	logger *slog.Logger,
) *ComplianceConsumer {
	return &ComplianceConsumer{consumer: consumer, place: place, release: release, suspend: suspend, logger: logger}
}

// Start arranca el consumer. Bloquea hasta que ctx sea cancelado.
func (c *ComplianceConsumer) Start(ctx context.Context) error {
	return c.consumer.Start(ctx, eventbus.ConsumerConfig{
		Stream:        "COMPLIANCE",
		Name:          "auth-compliance-consumer",
		FilterSubject: "compliance.sanction.>",
		MaxDeliver:    10, // bloquear dinero es crítico: más reintentos que el resto
	}, c.handle)
}

func (c *ComplianceConsumer) handle(ctx context.Context, msg *eventbus.Message) error {
	var p sanctionPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil || p.TenantID == "" || p.AlertID == "" {
		c.logger.Error("auth compliance consumer: malformed payload, skipping", "subject", msg.Subject)
		return nil // ack: reintentar un payload inválido solo agota MaxDeliver
	}
	cmd := application.ComplianceHoldCmd{TenantID: p.TenantID, AlertID: p.AlertID}

	var err error
	switch msg.Subject {
	case "compliance.sanction.matched.v1":
		err = c.place.Execute(ctx, cmd)
	case "compliance.sanction.cleared.v1":
		err = c.release.Execute(ctx, cmd)
	case "compliance.sanction.confirmed.v1":
		err = c.suspend.Execute(ctx, cmd)
	default:
		return nil // versión/subject desconocido → ack
	}
	if err != nil {
		c.logger.Error("auth compliance consumer: enforcement failed",
			"subject", msg.Subject, "tenantID", p.TenantID, "alertID", p.AlertID, "error", err)
		return err // nak → reintento
	}
	c.logger.Info("auth compliance consumer: enforcement applied",
		"subject", msg.Subject, "tenantID", p.TenantID, "alertID", p.AlertID)
	return nil
}
