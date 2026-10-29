package http

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/juantevez/cobros-platform/context/compliance/application"
)

// Formato del feed de watchlist (CSV propio, UTF-8, con encabezado):
//
//	external_ref,full_name,list_type,country
//	SDN-36,Vladimir Petrov,sanctions,RU
//
// Columnas en cualquier orden; country es opcional. La fuente (OFAC, EU, ...)
// no va en el archivo: la indica la request, y el feed reemplaza a esa fuente.
var requiredFeedColumns = []string{"external_ref", "full_name", "list_type"}

// errFeedFormat marca errores de formato del CSV (no de contenido).
var errFeedFormat = errors.New("invalid csv")

func parseWatchlistCSV(r io.Reader) ([]application.WatchlistFeedRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.ReuseRecord = true

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: empty file", errFeedFormat)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFeedFormat, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, c := range requiredFeedColumns {
		if _, ok := col[c]; !ok {
			return nil, fmt.Errorf("%w: missing column %q (header: external_ref,full_name,list_type,country)", errFeedFormat, c)
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	var rows []application.WatchlistFeedRow
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errFeedFormat, err)
		}
		line, _ := cr.FieldPos(0)
		rows = append(rows, application.WatchlistFeedRow{
			Line:        line,
			ExternalRef: get(rec, "external_ref"),
			FullName:    get(rec, "full_name"),
			ListType:    get(rec, "list_type"),
			Country:     get(rec, "country"),
		})
	}
	return rows, nil
}
