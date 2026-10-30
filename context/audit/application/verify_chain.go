package application

import (
	"context"
	"fmt"
)

// VerifyChainUseCase recorre el log de auditoría y verifica que cada entrada
// enlaza correctamente con la anterior (integridad del hash chain).
//
// Verifica de a lotes (from_id, limit). Cada lote se enlaza con la entrada
// anterior a su primera fila, así que paginar con next_from_id hasta
// complete=true verifica la cadena completa sin dejar bordes sin chequear.
type VerifyChainUseCase struct {
	repo   AuditLogRepository
	hasher HashComputer
}

func NewVerifyChainUseCase(repo AuditLogRepository, hasher HashComputer) *VerifyChainUseCase {
	return &VerifyChainUseCase{repo: repo, hasher: hasher}
}

const (
	verifyBatchSize    = 500
	maxVerifyBatchSize = 5000 // acota la memoria de un lote
)

func (uc *VerifyChainUseCase) Execute(ctx context.Context, q VerifyChainQuery) (VerifyChainResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = verifyBatchSize
	}
	if limit > maxVerifyBatchSize {
		limit = maxVerifyBatchSize
	}

	entries, err := uc.repo.ListFromID(ctx, q.FromID, limit)
	if err != nil {
		return VerifyChainResult{}, fmt.Errorf("verify chain: list entries: %w", err)
	}
	if len(entries) == 0 {
		return VerifyChainResult{Valid: true, Complete: true}, nil
	}

	// Predecesora de la primera fila del lote: enlaza el lote con el resto de
	// la cadena. Si no existe, la primera fila es la génesis y no debe tener
	// prev_hash (ChainLinksTo(nil)).
	prev, err := uc.repo.FindBefore(ctx, entries[0].ID())
	if err != nil {
		return VerifyChainResult{}, fmt.Errorf("verify chain: find predecessor: %w", err)
	}

	for i, entry := range entries {
		if !entry.VerifyHash(uc.hasher.Compute) {
			return invalid(i+1, entry.ID(), fmt.Sprintf("hash mismatch at entry id=%d", entry.ID())), nil
		}
		if !entry.ChainLinksTo(prev) {
			if prev == nil {
				return invalid(i+1, entry.ID(), fmt.Sprintf("first entry id=%d has unexpected prev_hash", entry.ID())), nil
			}
			return invalid(i+1, entry.ID(), fmt.Sprintf(
				"chain broken at entry id=%d: prev_hash does not match hash of entry id=%d", entry.ID(), prev.ID())), nil
		}
		prev = entry
	}

	res := VerifyChainResult{Valid: true, EntriesChecked: len(entries), Complete: len(entries) < limit}
	if !res.Complete {
		res.NextFromID = entries[len(entries)-1].ID() + 1
	}
	return res, nil
}

func invalid(checked int, id int64, msg string) VerifyChainResult {
	return VerifyChainResult{Valid: false, EntriesChecked: checked, FirstInvalidID: id, FirstInvalidMsg: msg}
}
