package application

import (
	"context"
	"fmt"
)

// VerifyChainUseCase recorre el log de auditoría y verifica que cada entrada
// enlaza correctamente con la anterior (integridad del hash chain).
type VerifyChainUseCase struct {
	repo   AuditLogRepository
	hasher HashComputer
}

func NewVerifyChainUseCase(repo AuditLogRepository, hasher HashComputer) *VerifyChainUseCase {
	return &VerifyChainUseCase{repo: repo, hasher: hasher}
}

const verifyBatchSize = 500

func (uc *VerifyChainUseCase) Execute(ctx context.Context, q VerifyChainQuery) (VerifyChainResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = verifyBatchSize
	}

	entries, err := uc.repo.ListFromID(ctx, q.FromID, limit)
	if err != nil {
		return VerifyChainResult{}, fmt.Errorf("verify chain: list entries: %w", err)
	}

	for i, entry := range entries {
		// Verificar que el hash almacenado coincide con el recalculado.
		if !entry.VerifyHash(uc.hasher.Compute) {
			return VerifyChainResult{
				Valid:           false,
				EntriesChecked:  i + 1,
				FirstInvalidID:  entry.ID(),
				FirstInvalidMsg: fmt.Sprintf("hash mismatch at entry id=%d", entry.ID()),
			}, nil
		}

		// Verificar que el prev_hash enlaza con el registro anterior.
		if i > 0 {
			prev := entries[i-1]
			if !entry.ChainLinksTo(prev) {
				return VerifyChainResult{
					Valid:           false,
					EntriesChecked:  i + 1,
					FirstInvalidID:  entry.ID(),
					FirstInvalidMsg: fmt.Sprintf("chain broken at entry id=%d: prev_hash does not match hash of entry id=%d", entry.ID(), prev.ID()),
				}, nil
			}
		} else if q.FromID == 0 {
			// El primer registro del sistema no debe tener prev_hash.
			if len(entry.PrevHash()) > 0 {
				return VerifyChainResult{
					Valid:           false,
					EntriesChecked:  1,
					FirstInvalidID:  entry.ID(),
					FirstInvalidMsg: fmt.Sprintf("first entry id=%d has unexpected prev_hash", entry.ID()),
				}, nil
			}
		}
	}

	return VerifyChainResult{
		Valid:          true,
		EntriesChecked: len(entries),
	}, nil
}
