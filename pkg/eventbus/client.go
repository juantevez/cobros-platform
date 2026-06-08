package eventbus

import (
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Client mantiene la conexión a NATS y el handle de JetStream.
// Es el punto de entrada para crear Publishers y Consumers.
type Client struct {
	nc *nats.Conn
	JS jetstream.JetStream
}

// New crea y valida una conexión a NATS con JetStream habilitado.
func New(cfg Config) (*Client, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.Timeout(cfg.ConnectTimeout),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Error("nats: disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("nats: reconnected")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("eventbus: connect to %q: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("eventbus: jetstream init: %w", err)
	}

	return &Client{nc: nc, JS: js}, nil
}

// Close drena la conexión de forma ordenada, esperando que los mensajes
// en vuelo sean procesados antes de cerrar.
func (c *Client) Close() {
	if err := c.nc.Drain(); err != nil {
		slog.Error("nats: drain error on close", "error", err)
	}
}
