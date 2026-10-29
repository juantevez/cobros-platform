package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// maxReportedFeedErrors acota los errores que se reportan de un feed inválido.
const maxReportedFeedErrors = 20

// WatchlistFeedRow es una fila del feed ya separada en columnas (el parseo
// del formato vive en el adaptador). Line es la línea del archivo, para
// reportar errores.
type WatchlistFeedRow struct {
	Line        int
	ExternalRef string
	FullName    string
	ListType    string
	Country     string
}

// ImportWatchlistCmd sincroniza una fuente completa de la watchlist con un feed.
type ImportWatchlistCmd struct {
	Source string // OFAC, EU, UN, ... — el feed reemplaza a toda la fuente
	Rows   []WatchlistFeedRow
	DryRun bool // calcula el delta sin aplicarlo
}

// ImportWatchlistResult resume el delta (aplicado o simulado).
type ImportWatchlistResult struct {
	Source    string `json:"source"`
	DryRun    bool   `json:"dry_run"`
	Added     int    `json:"added"`
	Updated   int    `json:"updated"`
	Removed   int    `json:"removed"`
	Unchanged int    `json:"unchanged"`
}

// FeedError detalla por qué se rechazó un feed. Envuelve
// domain.ErrInvalidWatchlistFeed.
type FeedError struct {
	Problems []string
}

func (e *FeedError) Error() string {
	return fmt.Sprintf("%v: %s", domain.ErrInvalidWatchlistFeed, strings.Join(e.Problems, "; "))
}

func (e *FeedError) Unwrap() error { return domain.ErrInvalidWatchlistFeed }

// ImportWatchlistUseCase aplica un feed a una fuente de la watchlist: agrega
// lo nuevo, reemplaza lo que cambió (baja + alta, conserva la versión vieja
// para trazabilidad) y da de baja lo que el feed ya no trae. Es atómico: o se
// aplica el feed completo o nada.
type ImportWatchlistUseCase struct {
	watchlist WatchlistRepository
	txManager TxManager
	clock     Clock
}

func NewImportWatchlistUseCase(watchlist WatchlistRepository, txManager TxManager, clock Clock) *ImportWatchlistUseCase {
	return &ImportWatchlistUseCase{watchlist: watchlist, txManager: txManager, clock: clock}
}

func (uc *ImportWatchlistUseCase) Execute(ctx context.Context, cmd ImportWatchlistCmd) (ImportWatchlistResult, error) {
	source := strings.ToUpper(strings.TrimSpace(cmd.Source))
	incoming, err := validateFeed(source, cmd.Rows)
	if err != nil {
		return ImportWatchlistResult{}, err
	}

	res := ImportWatchlistResult{Source: source, DryRun: cmd.DryRun}
	err = uc.txManager.RunInTx(ctx, func(ctx context.Context) error {
		current, err := uc.watchlist.ListActiveBySource(ctx, source)
		if err != nil {
			return err
		}
		plan := domain.PlanWatchlistSync(current, incoming)
		res.Added, res.Updated = len(plan.ToAdd), len(plan.ToReplace)
		res.Removed, res.Unchanged = len(plan.ToRemove), plan.Unchanged
		if cmd.DryRun {
			return nil
		}

		now := uc.clock.Now()
		for _, e := range plan.ToRemove {
			if err := uc.watchlist.Remove(ctx, e.ID, now); err != nil {
				return fmt.Errorf("remove %s: %w", e.ExternalRef, err)
			}
		}
		for _, r := range plan.ToReplace {
			if err := uc.watchlist.Remove(ctx, r.Old.ID, now); err != nil {
				return fmt.Errorf("replace %s: %w", r.Old.ExternalRef, err)
			}
			plan.ToAdd = append(plan.ToAdd, r.New)
		}
		for _, e := range plan.ToAdd {
			if err := uc.watchlist.Add(ctx, e, domain.NormalizeName(e.FullName), now); err != nil {
				return fmt.Errorf("add %s: %w", e.ExternalRef, err)
			}
		}
		return nil
	})
	if err != nil {
		return ImportWatchlistResult{}, err
	}
	return res, nil
}

// validateFeed valida todas las filas antes de tocar la base y reporta los
// problemas con su número de línea. Un feed vacío se rechaza: aplicarlo daría
// de baja la fuente completa.
func validateFeed(source string, rows []WatchlistFeedRow) ([]domain.WatchlistEntry, error) {
	var problems []string
	report := func(format string, args ...any) {
		if len(problems) < maxReportedFeedErrors {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	if source == "" {
		report("source is required")
	}
	if len(rows) == 0 {
		report("feed has no rows (it would remove the whole source)")
	}

	entries := make([]domain.WatchlistEntry, 0, len(rows))
	refLine := make(map[string]int, len(rows))
	for _, r := range rows {
		ref := strings.TrimSpace(r.ExternalRef)
		if ref == "" {
			report("line %d: external_ref is required", r.Line)
			continue
		}
		if prev, dup := refLine[ref]; dup {
			report("line %d: duplicate external_ref %q (first at line %d)", r.Line, ref, prev)
			continue
		}
		refLine[ref] = r.Line
		e, err := domain.NewWatchlistEntry(uuid.NewString(), r.FullName, strings.ToLower(strings.TrimSpace(r.ListType)), r.Country, source)
		if err != nil {
			report("line %d: %v", r.Line, err)
			continue
		}
		e.ExternalRef = ref
		entries = append(entries, e)
	}
	if len(problems) > 0 {
		return nil, &FeedError{Problems: problems}
	}
	return entries, nil
}
