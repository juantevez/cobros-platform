package readers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

// CSVReportParser parsea el informe del PSP en formato CSV.
//
// Formato esperado (primera fila = headers):
//   transaction_id,amount,currency,status,processed_at
//   PSP-REF-001,10000,ARS,captured,2026-06-01T12:00:00Z
//   PSP-REF-002,5000,ARS,rejected,2026-06-01T12:05:00Z
//
// - transaction_id: equivale al psp_reference almacenado en payments
// - amount: en centavos (unidades mínimas)
// - status: "captured" | "rejected" | "refunded" | "pending"
// - processed_at: RFC3339
type CSVReportParser struct{}

func NewCSVReportParser() *CSVReportParser { return &CSVReportParser{} }

func (p *CSVReportParser) Parse(data []byte) ([]application.PSPRecord, error) {
	if len(data) == 0 {
		return nil, domain.ErrEmptyReport
	}

	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true

	// Leer headers.
	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read headers: %v", domain.ErrInvalidReportFormat, err)
	}

	// Mapear nombre de columna → índice.
	colIdx, err := mapColumns(headers)
	if err != nil {
		return nil, err
	}

	// Leer registros.
	var records []application.PSPRecord
	lineNum := 1
	for {
		row, err := r.Read()
		if err != nil {
			break // EOF
		}
		lineNum++

		rec, err := parseRow(row, colIdx, lineNum)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}

	return records, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// columnMapping mapea nombres de columna a índices.
type columnMapping struct {
	transactionID int
	amount        int
	currency      int
	status        int
	processedAt   int
}

func mapColumns(headers []string) (columnMapping, error) {
	idx := make(map[string]int)
	for i, h := range headers {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	required := []string{"transaction_id", "amount", "currency", "status"}
	for _, col := range required {
		if _, ok := idx[col]; !ok {
			return columnMapping{}, fmt.Errorf("%w: missing required column %q", domain.ErrInvalidReportFormat, col)
		}
	}

	processedAtIdx := -1
	if i, ok := idx["processed_at"]; ok {
		processedAtIdx = i
	}

	return columnMapping{
		transactionID: idx["transaction_id"],
		amount:        idx["amount"],
		currency:      idx["currency"],
		status:        idx["status"],
		processedAt:   processedAtIdx,
	}, nil
}

func parseRow(row []string, col columnMapping, line int) (application.PSPRecord, error) {
	if col.transactionID >= len(row) || col.amount >= len(row) {
		return application.PSPRecord{}, fmt.Errorf("%w: row %d has too few columns", domain.ErrInvalidReportFormat, line)
	}

	amount, err := strconv.ParseInt(strings.TrimSpace(row[col.amount]), 10, 64)
	if err != nil {
		return application.PSPRecord{}, fmt.Errorf("row %d: invalid amount %q: %w", line, row[col.amount], err)
	}

	processedAt := time.Now().UTC()
	if col.processedAt >= 0 && col.processedAt < len(row) {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(row[col.processedAt])); err == nil {
			processedAt = t.UTC()
		}
	}

	return application.PSPRecord{
		TransactionID: strings.TrimSpace(row[col.transactionID]),
		Amount:        amount,
		Currency:      strings.ToUpper(strings.TrimSpace(row[col.currency])),
		Status:        strings.ToLower(strings.TrimSpace(row[col.status])),
		ProcessedAt:   processedAt,
	}, nil
}
