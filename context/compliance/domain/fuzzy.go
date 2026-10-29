package domain

import "strings"

// Matching difuso del screening: tolera errores de tipeo y transliteraciones
// ("Petrov"/"Petroff", "Muhammad"/"Mohamed", "Gonzalez"/"Gonsales"). Es más
// ruidoso que el match por tokens, por eso puntúa ScoreFuzzyMatch (riesgo
// medio): genera alerta para revisión, nunca hold (el hold exige
// ScoreExactMatch).

// ScoreFuzzyMatch es el puntaje de una coincidencia difusa.
const ScoreFuzzyMatch = 70

// FuzzyPrefilterThreshold es el umbral de pg_trgm word_similarity con que el
// repositorio preselecciona candidatos: entradas que contienen algo parecido
// a ALGÚN token del nombre (normalized_name %> token). Es una red amplia; la
// decisión final (TODOS los tokens de la entrada) la toma fuzzyTokensMatch.
const FuzzyPrefilterThreshold = 0.5

// maxFuzzyCandidateTokens acota los tokens del nombre que se usan para
// preseleccionar (una razón social rara vez tiene más).
const maxFuzzyCandidateTokens = 12

// FuzzyCandidateTokens retorna los tokens del nombre normalizado aptos para
// preseleccionar candidatos difusos: únicos y de al menos minFuzzyTokenLen.
func FuzzyCandidateTokens(nameNorm string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Fields(nameNorm) {
		if len([]rune(t)) < minFuzzyTokenLen || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == maxFuzzyCandidateTokens {
			break
		}
	}
	return out
}

// Reglas por token.
const (
	minFuzzyTokenLen = 4 // tokens más cortos solo matchean exactos
	longTokenLen     = 8 // desde esta longitud se toleran 2 ediciones
)

// fuzzyTokensMatch indica si cada token de la entrada coincide con un token
// distinto del nombre: igual, a distancia de edición acotada o con la misma
// clave fonética. Exige al menos dos tokens en la entrada: con uno solo el
// ruido (homónimos parciales) supera al valor de la alerta.
func fuzzyTokensMatch(entryNorm, nameNorm string) bool {
	entryTokens := strings.Fields(entryNorm)
	nameTokens := strings.Fields(nameNorm)
	if len(entryTokens) < 2 || len(nameTokens) < len(entryTokens) {
		return false
	}
	used := make([]bool, len(nameTokens))
	for _, et := range entryTokens {
		found := false
		for i, nt := range nameTokens {
			if !used[i] && tokensSimilar(et, nt) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tokensSimilar(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < minFuzzyTokenLen || len(rb) < minFuzzyTokenLen {
		return false
	}
	maxEdits := 1
	if len(ra) >= longTokenLen && len(rb) >= longTokenLen {
		maxEdits = 2
	}
	if damerauLevenshtein(ra, rb) <= maxEdits {
		return true
	}
	return phoneticKey(a) == phoneticKey(b)
}

// damerauLevenshtein es la distancia OSA (inserción, borrado, sustitución y
// transposición de adyacentes).
func damerauLevenshtein(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

// phoneticKey es una clave fonética simplificada para nombres en español y
// transliteraciones (ruso, árabe): unifica grafías que suenan igual
// (v/b/w/f, z/s, c/k/q, ll/y, ph/f, sh/sch/ch, h muda) y descarta las vocales
// después de la primera letra. Ej.: petrov/petroff → "ptrb",
// muhammad/mohamed → "md", gonzalez/gonsales → "gnsls".
func phoneticKey(token string) string {
	s := token
	for _, r := range []struct{ from, to string }{
		{"sch", "x"}, {"sh", "x"}, {"ch", "x"}, {"ph", "f"}, {"ck", "k"},
		{"qu", "k"}, {"kh", "k"}, {"gh", "g"}, {"th", "t"}, {"dh", "d"}, {"ll", "y"},
		{"ce", "se"}, {"ci", "si"},
	} {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	var b strings.Builder
	var last rune
	for i, r := range s {
		switch r {
		case 'v', 'w', 'f':
			r = 'b'
		case 'c', 'q':
			r = 'k'
		case 'z':
			r = 's'
		case 'h':
			continue
		}
		if i > 0 && strings.ContainsRune("aeiouy", r) {
			last = 0 // la vocal separa consonantes iguales ("mm" vs "m-m")
			continue
		}
		if r == last {
			continue
		}
		b.WriteRune(r)
		last = r
	}
	return b.String()
}
