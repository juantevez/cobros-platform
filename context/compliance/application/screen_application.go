package application

import (
	"context"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
)

// ScreenApplicationUseCase compara el nombre legal de un onboarding contra la
// watchlist global. Si hay coincidencia, levanta una alerta sanctions_match.
type ScreenApplicationUseCase struct {
	repo      AlertRepository
	watchlist WatchlistRepository
	txManager TxManager
	publisher EventPublisher
	clock     Clock
}

func NewScreenApplicationUseCase(
	repo AlertRepository,
	watchlist WatchlistRepository,
	txManager TxManager,
	publisher EventPublisher,
	clock Clock,
) *ScreenApplicationUseCase {
	return &ScreenApplicationUseCase{
		repo: repo, watchlist: watchlist,
		txManager: txManager, publisher: publisher, clock: clock,
	}
}

func (uc *ScreenApplicationUseCase) Execute(ctx context.Context, cmd ScreenApplicationCmd) error {
	tenantID, err := domain.ParseTenantID(cmd.TenantID)
	if err != nil {
		return err
	}
	if cmd.LegalName == "" {
		return nil // nada que evaluar
	}

	matches, err := uc.watchlist.Screen(ctx, domain.NormalizeName(cmd.LegalName))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return nil // sin coincidencias → sin alerta
	}

	// Una alerta por aplicación, con el mejor match. Los detalles registran
	// la lista y el país que dispararon la coincidencia.
	best := matches[0]
	for _, m := range matches[1:] {
		if m.Score > best.Score {
			best = m
		}
	}

	alert := domain.NewAlert(
		domain.NewAlertID(),
		tenantID,
		domain.AlertSanctionsMatch,
		domain.RiskFromScore(best.Score),
		cmd.LegalName,
		best.Score,
		map[string]string{
			"application_id": cmd.ApplicationID,
			"matched_name":   best.Entry.FullName,
			"list_type":      best.Entry.ListType,
			"country":        best.Entry.Country,
			"source":         best.Entry.Source,
		},
		uc.clock.Now(),
	)

	return raiseAlert(ctx, uc.txManager, uc.repo, uc.publisher, alert)
}
