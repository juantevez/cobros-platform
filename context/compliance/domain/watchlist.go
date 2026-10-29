package domain

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Tipos de lista admitidos (mismo dominio que el CHECK de aml_watchlist).
const (
	ListTypeSanctions = "sanctions"
	ListTypePEP       = "pep"
)

// WatchlistEntry es una entrada de la lista de vigilancia global (sanciones/PEP).
type WatchlistEntry struct {
	ID       string
	FullName string
	ListType string // "sanctions" | "pep"
	Country  string
	Source   string // OFAC, EU, UN, ...
	// ExternalRef es el identificador de la entrada en el feed de origen
	// (único por fuente entre las vigentes). Vacío = carga manual.
	ExternalRef string
}

// NewWatchlistEntry valida y construye una entrada. El nombre debe conservar
// al menos un token tras normalizar (si no, nunca podría matchear) y el tipo
// de lista debe ser sanctions|pep.
func NewWatchlistEntry(id, fullName, listType, country, source string) (WatchlistEntry, error) {
	if NormalizeName(fullName) == "" {
		return WatchlistEntry{}, ErrInvalidWatchlistName
	}
	if listType != ListTypeSanctions && listType != ListTypePEP {
		return WatchlistEntry{}, ErrInvalidListType
	}
	return WatchlistEntry{
		ID:       id,
		FullName: strings.TrimSpace(fullName),
		ListType: listType,
		Country:  strings.ToUpper(strings.TrimSpace(country)),
		Source:   strings.TrimSpace(source),
	}, nil
}

// Match es el resultado de un screening: una entrada de la watchlist que
// coincide con el nombre evaluado, con un puntaje de confianza.
type Match struct {
	Entry WatchlistEntry
	Score int // 0..100
}

// NormalizeName lleva un nombre a su forma canónica para comparar:
// sin acentos/diacríticos, minúsculas, puntuación reemplazada por espacio y
// espacios colapsados ("Pérez-Gómez, S.A." → "perez gomez s a"). Se usa tanto
// al cargar la watchlist como al evaluar un nombre entrante. Idempotente.
//
// La migración 000018 replica esta regla en SQL para re-normalizar las filas
// existentes; si cambia, hay que re-normalizar aml_watchlist.normalized_name.
func NormalizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range norm.NFD.String(name) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// marca diacrítica separada por NFD → se descarta
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Puntajes del screening por calidad de coincidencia.
const (
	ScoreExactMatch   = 100 // el nombre evaluado es exactamente la entrada
	ScoreOrderedMatch = 90  // la entrada aparece como secuencia contigua de tokens
	ScoreTokenMatch   = 80  // todos los tokens de la entrada aparecen, en otro orden
)

// MatchScore compara una entrada normalizada contra un nombre normalizado.
// Primero por tokens completos (no por substrings: "ivan sokolov" no coincide
// exacto con "ivan sokolovsky"); si no hay coincidencia por tokens, prueba el
// matching difuso (ScoreFuzzyMatch, ver fuzzy.go). Retorna 0 si no hay
// coincidencia.
func MatchScore(entryNorm, nameNorm string) int {
	if score := tokenMatchScore(entryNorm, nameNorm); score > 0 {
		return score
	}
	if fuzzyTokensMatch(entryNorm, nameNorm) {
		return ScoreFuzzyMatch
	}
	return 0
}

func tokenMatchScore(entryNorm, nameNorm string) int {
	if entryNorm == "" || nameNorm == "" {
		return 0
	}
	if entryNorm == nameNorm {
		return ScoreExactMatch
	}
	if strings.Contains(" "+nameNorm+" ", " "+entryNorm+" ") {
		return ScoreOrderedMatch
	}
	tokens := make(map[string]struct{})
	for _, t := range strings.Fields(nameNorm) {
		tokens[t] = struct{}{}
	}
	for _, t := range strings.Fields(entryNorm) {
		if _, ok := tokens[t]; !ok {
			return 0
		}
	}
	return ScoreTokenMatch
}

// RiskFromScore mapea un puntaje de match a un nivel de riesgo.
func RiskFromScore(score int) RiskLevel {
	switch {
	case score >= 90:
		return RiskHigh
	case score >= 60:
		return RiskMedium
	default:
		return RiskLow
	}
}

// ── Sincronización de una fuente (ingesta de feeds) ───────────────────────────

// SyncPlan es el delta entre las entradas vigentes de una fuente y un feed.
type SyncPlan struct {
	ToAdd     []WatchlistEntry // external_ref nuevo
	ToReplace []Replacement    // external_ref existente con datos distintos
	ToRemove  []WatchlistEntry // vigentes que el feed ya no trae
	Unchanged int
}

// Replacement da de baja la versión vigente y crea la nueva: las alertas
// pasadas siguen siendo trazables a la versión que las disparó.
type Replacement struct {
	Old WatchlistEntry
	New WatchlistEntry
}

// PlanWatchlistSync calcula el delta de una fuente. `current` son las
// entradas vigentes con external_ref de esa fuente; `incoming`, las del feed
// (ya validadas, con external_ref único). Las entradas cargadas a mano (sin
// external_ref) no participan: el feed no las conoce.
func PlanWatchlistSync(current, incoming []WatchlistEntry) SyncPlan {
	byRef := make(map[string]WatchlistEntry, len(current))
	for _, e := range current {
		byRef[e.ExternalRef] = e
	}
	var plan SyncPlan
	seen := make(map[string]bool, len(incoming))
	for _, in := range incoming {
		seen[in.ExternalRef] = true
		old, ok := byRef[in.ExternalRef]
		switch {
		case !ok:
			plan.ToAdd = append(plan.ToAdd, in)
		case old.FullName != in.FullName || old.ListType != in.ListType || old.Country != in.Country:
			plan.ToReplace = append(plan.ToReplace, Replacement{Old: old, New: in})
		default:
			plan.Unchanged++
		}
	}
	for _, e := range current {
		if !seen[e.ExternalRef] {
			plan.ToRemove = append(plan.ToRemove, e)
		}
	}
	return plan
}
