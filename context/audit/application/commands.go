package application

// RecordActionCmd es el comando para registrar una acción en el log.
type RecordActionCmd struct {
	TenantID      string            // vacío para acciones de plataforma
	Actor         string            // userID o "system"
	Action        string            // ej: "auth.tenant.activated"
	ResourceType  string            // ej: "tenant"
	ResourceID    string            // UUID del recurso afectado
	Metadata      map[string]string // contexto adicional
	CorrelationID string            // para trazar con los logs de la app
}

// VerifyChainQuery solicita verificación de integridad del hash chain.
type VerifyChainQuery struct {
	// FromID: verificar desde este ID (0 = desde el principio).
	FromID int64
	// Limit: cuántos registros verificar (0 = todos).
	Limit int
}

// VerifyChainResult es el resultado de la verificación.
type VerifyChainResult struct {
	Valid           bool   // true si toda la cadena es íntegra
	EntriesChecked  int    // cuántas entradas se revisaron
	FirstInvalidID  int64  // ID del primer registro inválido (0 si Valid=true)
	FirstInvalidMsg string // descripción del problema encontrado
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
	PrevHash      string            `json:"prev_hash,omitempty"`
	Hash          string            `json:"hash"`
	CreatedAt     string            `json:"created_at"`
}
