package domain

import "strings"

// WatchlistEntry es una entrada de la lista de vigilancia global (sanciones/PEP).
type WatchlistEntry struct {
	ID       string
	FullName string
	ListType string // "sanctions" | "pep"
	Country  string
	Source   string // OFAC, EU, UN, ...
}

// Match es el resultado de un screening: una entrada de la watchlist que
// coincide con el nombre evaluado, con un puntaje de confianza.
type Match struct {
	Entry WatchlistEntry
	Score int // 0..100
}

// NormalizeName lleva un nombre a su forma canónica para comparar:
// minúsculas, sin espacios de más. Se usa tanto al sembrar la watchlist
// como al evaluar un nombre entrante.
func NormalizeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
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
