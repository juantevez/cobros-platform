package application

import "time"

// RecordActionCmd es el comando para registrar una acción en el log.
type RecordActionCmd struct {
	TenantID      string            // vacío para acciones de plataforma
	Actor         string            // userID o "system"
	Action        string            // ej: "auth.tenant.activated"
	ResourceType  string            // ej: "tenant"
	ResourceID    string            // UUID del recurso afectado
	Metadata      map[string]string // contexto adicional
	CorrelationID string            // para trazar con los logs de la app
	EventID       string            // ID del evento de dominio (idempotencia); vacío si no viene de un evento
	OccurredAt    time.Time         // cuándo ocurrió el hecho; cero = momento del registro
}

// VerifyChainQuery solicita verificación de integridad del hash chain.
type VerifyChainQuery struct {
	// FromID: verificar desde este ID (0 = desde el principio).
	FromID int64
	// Limit: cuántos registros verificar (≤ 0 → 500; máximo 5000).
	Limit int
}

// VerifyChainResult es el resultado de la verificación de un lote.
type VerifyChainResult struct {
	Valid           bool   `json:"valid"`             // true si el lote (y su enlace con lo anterior) es íntegro
	EntriesChecked  int    `json:"entries_checked"`   // cuántas entradas se revisaron
	FirstInvalidID  int64  `json:"first_invalid_id"`  // ID del primer registro inválido (0 si Valid)
	FirstInvalidMsg string `json:"first_invalid_msg"` // descripción del problema
	// Paginación: si Complete es false, verificar el siguiente lote desde NextFromID.
	NextFromID int64 `json:"next_from_id,omitempty"`
	Complete   bool  `json:"complete"`
}

// ListLogsQuery consulta entradas del log con filtros opcionales.
type ListLogsQuery struct {
	TenantID string // vacío = todos (requiere platform_support)
	Limit    int    // default 50, máximo 200
}

type LogEntryView struct {
	ID            int64             `json:"id"`
	TenantID      string            `json:"tenant_id,omitempty"`
	Actor         string            `json:"actor"`
	Action        string            `json:"action"`
	ResourceType  string            `json:"resource_type"`
	ResourceID    string            `json:"resource_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	EventID       string            `json:"event_id,omitempty"`
	OccurredAt    string            `json:"occurred_at"`
	PrevHash      string            `json:"prev_hash,omitempty"`
	Hash          string            `json:"hash"`
	CreatedAt     string            `json:"created_at"`
}
