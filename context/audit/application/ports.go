package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// AuditLogRepository persiste y recupera entradas del log de auditoría.
// Las operaciones de escritura deben serializar las inserciones para
// mantener la integridad del hash chain.
type AuditLogRepository interface {
	// Save persiste una nueva entrada. Asigna el ID secuencial (BIGSERIAL).
	// Debe ejecutarse dentro de una tx serializable para el hash chain.
	Save(ctx context.Context, entry *domain.AuditLogEntry) error

	// FindLast retorna el último registro insertado (para obtener su hash).
	// Retorna nil, nil si la tabla está vacía.
	FindLast(ctx context.Context) (*domain.AuditLogEntry, error)

	// ListRecent retorna las últimas n entradas en orden descendente.
	ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error)

	// ListByTenant retorna las últimas n entradas de un tenant específico.
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error)

	// ListFromID retorna todas las entradas desde un ID dado, en orden ascendente.
	// Usado por VerifyChain para recorrer la cadena.
	ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error)
}

// HashComputer calcula el hash de un payload string.
// La implementación concreta usa SHA-256.
type HashComputer interface {
	Compute(payload string) []byte
}

// Clock abstrae el acceso al tiempo.
type Clock interface {
	Now() time.Time
}
