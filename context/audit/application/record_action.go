package application

import (
	"context"
	"fmt"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// RecordActionUseCase registra una acción sensible en el log de auditoría.
//
// Garantías:
//   - Cada entrada incluye el hash de la anterior (hash chain tamper-evident).
//   - La inserción es serializada: se lee el último hash y se inserta el nuevo
//     dentro de la misma transacción con SELECT FOR UPDATE.
//   - Si el sistema no tiene registros previos, el primer entry tiene prevHash=nil.
//
// Nota sobre concurrencia: en Fase 1 el consumer de NATS que llama a este
// caso de uso es un único goroutine (pull consumer secuencial), por lo que
// no hay contención. El FOR UPDATE en FindLast protege contra múltiples
// instancias del worker en el futuro.
type RecordActionUseCase struct {
	repo   AuditLogRepository
	hasher HashComputer
	clock  Clock
}

func NewRecordActionUseCase(
	repo AuditLogRepository,
	hasher HashComputer,
	clock Clock,
) *RecordActionUseCase {
	return &RecordActionUseCase{repo: repo, hasher: hasher, clock: clock}
}

func (uc *RecordActionUseCase) Execute(ctx context.Context, cmd RecordActionCmd) error {
	action, err := domain.ParseAction(cmd.Action)
	if err != nil {
		return err
	}

	resourceType, err := domain.ParseResourceType(cmd.ResourceType)
	if err != nil {
		return err
	}

	// Leer el último hash de la cadena. Si la tabla está vacía, prevHash = nil.
	last, err := uc.repo.FindLast(ctx)
	if err != nil {
		return fmt.Errorf("record action: find last entry: %w", err)
	}

	var prevHash []byte
	if last != nil {
		prevHash = last.Hash()
	}

	entry, err := domain.NewAuditLogEntry(
		cmd.TenantID,
		cmd.Actor,
		action,
		resourceType,
		cmd.ResourceID,
		cmd.Metadata,
		cmd.CorrelationID,
		prevHash,
		uc.clock.Now(),
		uc.hasher.Compute,
	)
	if err != nil {
		return fmt.Errorf("record action: build entry: %w", err)
	}

	if err := uc.repo.Save(ctx, entry); err != nil {
		return fmt.Errorf("record action: save: %w", err)
	}

	return nil
}
