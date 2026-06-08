package eventbus

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// streamDuplicatesWindow es la ventana de deduplicación de JetStream.
// Mensajes con el mismo Nats-Msg-Id publicados dentro de esta ventana
// se descartan automáticamente. Debe ser mayor al intervalo del relay.
const streamDuplicatesWindow = 5 * time.Minute

// EnsureStreams crea o actualiza los streams definidos en defs.
// Es idempotente: si un stream ya existe con la misma config, no hace nada.
// Debe llamarse al iniciar la aplicación, antes de publicar o consumir.
func EnsureStreams(ctx context.Context, client *Client, defs []StreamDefinition) error {
	for _, def := range defs {
		if err := ensureStream(ctx, client.JS, def); err != nil {
			return err
		}
	}
	return nil
}

func ensureStream(ctx context.Context, js jetstream.JetStream, def StreamDefinition) error {
	cfg := jetstream.StreamConfig{
		Name:      def.Name,
		Subjects:  def.Subjects,
		Storage:   jetstream.FileStorage,  // persistido en disco
		Retention: jetstream.LimitsPolicy, // retención por límites (tamaño/tiempo)
		MaxBytes:  -1,                     // sin límite por defecto; ajustar por env
		// Deduplication window: JetStream deduplica por Nats-Msg-Id
		// dentro de esta ventana. Debe ser mayor al intervalo del relay.
		Duplicates: streamDuplicatesWindow,
	}

	_, err := js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return fmt.Errorf("eventbus: ensure stream %q: %w", def.Name, err)
	}

	slog.Info("eventbus: stream ready", "stream", def.Name, "subjects", def.Subjects)
	return nil
}

// AppStreams retorna los streams que la aplicación debe provisionar.
// Agregar aquí cada nuevo stream al incorporar un contexto.
func AppStreams() []StreamDefinition {
	return []StreamDefinition{
		{Name: "AUTH", Subjects: []string{"auth.>"}},
		{Name: "LEDGER", Subjects: []string{"ledger.>"}},
		// AUDIT consume; no produce streams propios de negocio.
		// OUTBOX no tiene stream propio: usa AUTH y LEDGER.
	}
}
