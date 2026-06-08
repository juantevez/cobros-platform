package eventbus

import "time"

// Config contiene los parámetros de conexión a NATS.
type Config struct {
	// URL de conexión NATS, ej: "nats://localhost:4222"
	URL string

	ConnectTimeout time.Duration
	ReconnectWait  time.Duration
	// MaxReconnects: -1 = infinito
	MaxReconnects int
}

// DefaultConfig retorna una Config con valores razonables.
func DefaultConfig(url string) Config {
	return Config{
		URL:            url,
		ConnectTimeout: 5 * time.Second,
		ReconnectWait:  2 * time.Second,
		MaxReconnects:  -1, // reconectar indefinidamente
	}
}

// StreamDefinition define un stream que la aplicación debe provisionar al iniciar.
type StreamDefinition struct {
	// Name es el nombre del stream en JetStream (ej: "AUTH").
	Name string
	// Subjects son los subject patterns que el stream captura (ej: ["auth.>"]).
	Subjects []string
}
