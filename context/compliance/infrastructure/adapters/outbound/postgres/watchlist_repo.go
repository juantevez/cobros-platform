package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/juantevez/cobros-platform/context/compliance/domain"
	pkgpostgres "github.com/juantevez/cobros-platform/pkg/postgres"
)

// WatchlistRepository consulta y gestiona la lista de vigilancia global.
// No es tenant-scoped: la watchlist es única para toda la plataforma.
type WatchlistRepository struct{ pool pkgpostgres.Conn }

func NewWatchlistRepository(pool pkgpostgres.Conn) *WatchlistRepository {
	return &WatchlistRepository{pool: pool}
}

// Screen retorna las entradas cuyo nombre normalizado está contenido en el
// nombre normalizado dado. Ej.: la entrada "vladimir petrov" coincide con
// "vladimir petrov holdings". Match → score 90 (containment de alta confianza).
func (r *WatchlistRepository) Screen(ctx context.Context, normalizedName string) ([]domain.Match, error) {
	if normalizedName == "" {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, full_name, list_type, country, source
		FROM aml_watchlist
		WHERE position(normalized_name IN $1) > 0`,
		normalizedName,
	)
	if err != nil {
		return nil, fmt.Errorf("compliance repo: screen: %w", err)
	}
	defer rows.Close()

	var matches []domain.Match
	for rows.Next() {
		var e domain.WatchlistEntry
		if err := rows.Scan(&e.ID, &e.FullName, &e.ListType, &e.Country, &e.Source); err != nil {
			return nil, fmt.Errorf("compliance repo: screen scan: %w", err)
		}
		matches = append(matches, domain.Match{Entry: e, Score: 90})
	}
	return matches, rows.Err()
}

func (r *WatchlistRepository) Add(ctx context.Context, entry domain.WatchlistEntry, normalizedName string, addedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO aml_watchlist (id, full_name, normalized_name, list_type, country, source, added_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		entry.ID, entry.FullName, normalizedName, entry.ListType,
		entry.Country, entry.Source, addedAt,
	)
	if err != nil {
		return fmt.Errorf("compliance repo: add watchlist entry: %w", err)
	}
	return nil
}

func (r *WatchlistRepository) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, full_name, list_type, country, source
		FROM aml_watchlist ORDER BY full_name LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("compliance repo: list watchlist: %w", err)
	}
	defer rows.Close()

	var entries []domain.WatchlistEntry
	for rows.Next() {
		var e domain.WatchlistEntry
		if err := rows.Scan(&e.ID, &e.FullName, &e.ListType, &e.Country, &e.Source); err != nil {
			return nil, fmt.Errorf("compliance repo: list watchlist scan: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
