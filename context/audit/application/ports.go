package application

import (
	"context"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// TxManager abstrae transacciones de base de datos.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(context.Context) error) error
}

// AuditLogRepository persiste y recupera entradas del log de auditoría.
// Las escrituras deben serializarse para mantener la integridad del hash
// chain: LockChain + FindLast + Save en la misma transacción.
type AuditLogRepository interface {
	// LockChain serializa los registros concurrentes (de este y de otros
	// procesos) hasta el fin de la transacción. Debe llamarse dentro de
	// RunInTx, antes de FindLast.
	LockChain(ctx context.Context) error

	// Save persiste una nueva entrada. Asigna el ID secuencial (BIGSERIAL).
	Save(ctx context.Context, entry *domain.AuditLogEntry) error

	// ExistsByEventID indica si ya se registró una entrada para ese evento
	// (re-entregas de JetStream). Debe llamarse con LockChain tomado.
	ExistsByEventID(ctx context.Context, eventID string) (bool, error)

	// FindLast retorna el último registro insertado (para obtener su hash).
	// Retorna nil, nil si la tabla está vacía.
	FindLast(ctx context.Context) (*domain.AuditLogEntry, error)

	// ListRecent retorna las últimas n entradas en orden descendente.
	ListRecent(ctx context.Context, limit int) ([]*domain.AuditLogEntry, error)

	// ListByTenant retorna las últimas n entradas de un tenant específico.
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]*domain.AuditLogEntry, error)

	// FindBefore retorna la última entrada con id < beforeID (la predecesora
	// en la cadena), o nil, nil si no hay ninguna.
	FindBefore(ctx context.Context, beforeID int64) (*domain.AuditLogEntry, error)

	// ListFromID retorna todas las entradas desde un ID dado, en orden ascendente.
	// Usado por VerifyChain para recorrer la cadena.
	ListFromID(ctx context.Context, fromID int64, limit int) ([]*domain.AuditLogEntry, error)
}

// CheckpointRepository persiste los checkpoints de la cadena (append-only).
type CheckpointRepository interface {
	Save(ctx context.Context, cp domain.ChainCheckpoint) error
	// Latest retorna el último checkpoint, o nil, nil si no hay ninguno.
	Latest(ctx context.Context) (*domain.ChainCheckpoint, error)
	// FirstMismatch retorna el primer checkpoint cuya entrada falta en
	// audit_log o tiene otro hash, o nil, nil si todos se respetan.
	FirstMismatch(ctx context.Context) (*domain.CheckpointMismatch, error)
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
