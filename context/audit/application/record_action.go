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
//   - La lectura del último hash y la inserción ocurren en la misma
//     transacción, bajo un lock de la cadena (LockChain). Los consumers de
//     AUTH y LEDGER corren en paralelo (y puede haber varias instancias del
//     worker): sin el lock, dos registros simultáneos leerían el mismo último
//     hash y bifurcarían la cadena. Un SELECT … FOR UPDATE de la última fila
//     no alcanza: en READ COMMITTED el segundo lector, al obtener el lock,
//     revalida la misma fila vieja en vez de ver la recién insertada.
//   - Si el sistema no tiene registros previos, el primer entry tiene prevHash=nil.
type RecordActionUseCase struct {
	repo      AuditLogRepository
	hasher    HashComputer
	clock     Clock
	txManager TxManager
}

func NewRecordActionUseCase(
	repo AuditLogRepository,
	hasher HashComputer,
	clock Clock,
	txManager TxManager,
) *RecordActionUseCase {
	return &RecordActionUseCase{repo: repo, hasher: hasher, clock: clock, txManager: txManager}
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

	return uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := uc.repo.LockChain(ctx); err != nil {
			return fmt.Errorf("record action: lock chain: %w", err)
		}

		// Idempotencia: una re-entrega del mismo evento (ack perdido después
		// del INSERT) no crea una segunda entrada. Con el lock tomado, el
		// chequeo y el INSERT no compiten con otro registro.
		if cmd.EventID != "" {
			exists, err := uc.repo.ExistsByEventID(ctx, cmd.EventID)
			if err != nil {
				return fmt.Errorf("record action: check event: %w", err)
			}
			if exists {
				return nil
			}
		}

		// Último hash de la cadena (con el lock tomado). Tabla vacía → prevHash = nil.
		last, err := uc.repo.FindLast(ctx)
		if err != nil {
			return fmt.Errorf("record action: find last entry: %w", err)
		}
		var prevHash []byte
		if last != nil {
			prevHash = last.Hash()
		}

		entry, err := domain.NewAuditLogEntry(domain.EntryInput{
			TenantID:      cmd.TenantID,
			Actor:         cmd.Actor,
			Action:        action,
			ResourceType:  resourceType,
			ResourceID:    cmd.ResourceID,
			Metadata:      cmd.Metadata,
			CorrelationID: cmd.CorrelationID,
			EventID:       cmd.EventID,
			OccurredAt:    cmd.OccurredAt,
		}, prevHash, uc.clock.Now(), uc.hasher.Compute)
		if err != nil {
			return fmt.Errorf("record action: build entry: %w", err)
		}

		if err := uc.repo.Save(ctx, entry); err != nil {
			return fmt.Errorf("record action: save: %w", err)
		}
		return nil
	})
}
