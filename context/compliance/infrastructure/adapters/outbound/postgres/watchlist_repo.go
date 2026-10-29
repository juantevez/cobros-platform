package postgres

import (
	"context"
	"fmt"
	"strconv"
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

// Screen retorna las entradas vigentes que coinciden con el nombre evaluado,
// puntuadas por domain.MatchScore. La consulta preselecciona candidatos por
// dos vías (ambas indexadas):
//
//   - tokens: string_to_array(normalized_name) <@ string_to_array($1) — todos
//     los tokens de la entrada están en el nombre (GIN sobre el array);
//   - difusa: normalized_name %> token, para cada token del nombre — la
//     entrada contiene algo parecido a ese token (GIN trigram, pg_trgm, con
//     pg_trgm.word_similarity_threshold fijado localmente en la tx).
//
// La preselección es amplia: el puntaje (exacto 100, contiguo 90, reordenado
// 80, difuso 70) y el descarte final los decide el dominio en Go.
func (r *WatchlistRepository) Screen(ctx context.Context, normalizedName string) ([]domain.Match, error) {
	if normalizedName == "" {
		return nil, nil
	}
	args := []any{normalizedName}
	cond := "string_to_array(normalized_name, ' ') <@ string_to_array($1, ' ')"
	for _, tok := range domain.FuzzyCandidateTokens(normalizedName) {
		args = append(args, tok)
		cond += fmt.Sprintf(" OR normalized_name %%> $%d", len(args))
	}
	q := `
		SELECT id, full_name, list_type, country, source, normalized_name
		FROM aml_watchlist
		WHERE removed_at IS NULL AND (` + cond + `)`

	ctx = pkgpostgres.WithAppTenant(ctx, trgmThresholdKey, strconv.FormatFloat(domain.FuzzyPrefilterThreshold, 'f', 2, 64))
	var matches []domain.Match
	err := pkgpostgres.RunWithAppTenant(ctx, r.pool, func(ctx context.Context) error {
		rows, err := pkgpostgres.ConnFromContext(ctx, r.pool).Query(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("compliance repo: screen: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				e         domain.WatchlistEntry
				entryNorm string
			)
			if err := rows.Scan(&e.ID, &e.FullName, &e.ListType, &e.Country, &e.Source, &entryNorm); err != nil {
				return fmt.Errorf("compliance repo: screen scan: %w", err)
			}
			if score := domain.MatchScore(entryNorm, normalizedName); score > 0 {
				matches = append(matches, domain.Match{Entry: e, Score: score})
			}
		}
		return rows.Err()
	})
	return matches, err
}

// trgmThresholdKey es el GUC de pg_trgm que usa el operador %>. Se fija con
// set_config(..., is_local=true) dentro de la tx efímera de RunWithAppTenant.
const trgmThresholdKey = "pg_trgm.word_similarity_threshold"

// Add inserta una entrada. Usa la tx del contexto si hay una (import atómico).
func (r *WatchlistRepository) Add(ctx context.Context, entry domain.WatchlistEntry, normalizedName string, addedAt time.Time) error {
	_, err := pkgpostgres.ConnFromContext(ctx, r.pool).Exec(ctx, `
		INSERT INTO aml_watchlist (id, full_name, normalized_name, list_type, country, source, external_ref, added_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		entry.ID, entry.FullName, normalizedName, entry.ListType,
		entry.Country, entry.Source, nullStr(entry.ExternalRef), addedAt,
	)
	if err != nil {
		return fmt.Errorf("compliance repo: add watchlist entry: %w", err)
	}
	return nil
}

// Remove da de baja la entrada (soft delete). La fila se conserva para que las
// alertas pasadas sigan siendo trazables a la entrada que las disparó.
func (r *WatchlistRepository) Remove(ctx context.Context, id string, removedAt time.Time) error {
	tag, err := pkgpostgres.ConnFromContext(ctx, r.pool).Exec(ctx, `
		UPDATE aml_watchlist SET removed_at=$2
		WHERE id=$1 AND removed_at IS NULL`,
		id, removedAt,
	)
	if err != nil {
		return fmt.Errorf("compliance repo: remove watchlist entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWatchlistEntryNotFound
	}
	return nil
}

const watchlistSelect = `
		SELECT id, full_name, list_type, country, source, COALESCE(external_ref,'')
		FROM aml_watchlist`

func (r *WatchlistRepository) List(ctx context.Context, limit int) ([]domain.WatchlistEntry, error) {
	return r.query(ctx, "list watchlist",
		watchlistSelect+` WHERE removed_at IS NULL ORDER BY full_name LIMIT $1`, limit)
}

// ListActiveBySource retorna las entradas vigentes de la fuente cargadas por
// feed (con external_ref), base del delta de ImportWatchlist.
func (r *WatchlistRepository) ListActiveBySource(ctx context.Context, source string) ([]domain.WatchlistEntry, error) {
	return r.query(ctx, "list watchlist by source",
		watchlistSelect+` WHERE removed_at IS NULL AND source = $1 AND external_ref IS NOT NULL`, source)
}

func (r *WatchlistRepository) query(ctx context.Context, op, q string, args ...any) ([]domain.WatchlistEntry, error) {
	rows, err := pkgpostgres.ConnFromContext(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("compliance repo: %s: %w", op, err)
	}
	defer rows.Close()

	var entries []domain.WatchlistEntry
	for rows.Next() {
		var e domain.WatchlistEntry
		if err := rows.Scan(&e.ID, &e.FullName, &e.ListType, &e.Country, &e.Source, &e.ExternalRef); err != nil {
			return nil, fmt.Errorf("compliance repo: %s scan: %w", op, err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
