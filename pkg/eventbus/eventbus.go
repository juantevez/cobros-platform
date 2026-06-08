// Package eventbus provee la abstracción sobre NATS JetStream.
//
// Las interfaces Publisher y Consumer son los puertos de salida que los
// contextos de dominio usan; la implementación concreta (NATS) vive en
// este mismo paquete pero los contextos solo dependen de las interfaces.
//
// Nomenclatura de subjects (convención del proyecto):
//
//	<contexto>.<agregado>.<evento>.<version>
//	Ejemplos:
//	  auth.tenant.created.v1
//	  ledger.entry.posted.v1
//	  audit.log.recorded.v1
package eventbus

import "context"

// Message es la unidad de intercambio del bus de eventos.
type Message struct {
	// Subject es el destino NATS, ej: "auth.tenant.created.v1"
	Subject string
	// ID es el identificador único del mensaje.
	// Se usa como Nats-Msg-Id para deduplicación en JetStream.
	// Debe ser el ID del evento de dominio (UUID).
	ID string
	// Payload es el cuerpo serializado del evento (JSON).
	Payload []byte
	// Headers son metadatos opcionales (correlation-id, tenant-id, etc.).
	Headers map[string]string
}

// Handler procesa un mensaje recibido.
// Si retorna error, el mensaje se re-entrega (hasta MaxDeliver del consumer).
// Las implementaciones deben ser idempotentes.
type Handler func(ctx context.Context, msg *Message) error

// Publisher publica mensajes en el bus de eventos.
// La implementación garantiza entrega al broker (ack de JetStream).
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// Consumer gestiona la recepción de mensajes de un consumer durable.
type Consumer interface {
	// Start arranca el loop de consumo. Bloquea hasta que ctx sea cancelado.
	// Crea o actualiza el consumer durable si no existe.
	Start(ctx context.Context, cfg ConsumerConfig, handler Handler) error
}

// ConsumerConfig define los parámetros de un consumer durable.
type ConsumerConfig struct {
	// Stream es el nombre del stream JetStream (ej: "AUTH").
	Stream string
	// Name es el nombre del consumer durable (ej: "audit-auth-consumer").
	Name string
	// FilterSubject limita los mensajes recibidos (ej: "auth.>").
	FilterSubject string
	// MaxDeliver es el máximo de re-entregas antes de ir a DLQ (default 5).
	MaxDeliver int
}
