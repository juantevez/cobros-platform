package readers

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/reconciliation/application"
	"github.com/juantevez/cobros-platform/context/reconciliation/domain"
)

func parse(t *testing.T, csv string) ([]application.PSPRecord, error) {
	t.Helper()
	return NewCSVReportParser().Parse([]byte(csv))
}

func TestCSVReportParser_Parse(t *testing.T) {
	t.Run("parses rows and normalizes values", func(t *testing.T) {
		recs, err := parse(t, "transaction_id,amount,currency,status,processed_at\n"+
			"PSP-1,10000,ars,CAPTURED,2026-06-01T12:00:00Z\n"+
			" PSP-2 , 5000 ,ARS, Rejected ,2026-06-01T09:05:00-03:00\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []struct {
			rec application.PSPRecord
			at  time.Time
		}{
			{application.PSPRecord{TransactionID: "PSP-1", Amount: 10000, Currency: "ARS", Status: "captured"}, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
			{application.PSPRecord{TransactionID: "PSP-2", Amount: 5000, Currency: "ARS", Status: "rejected"}, time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC)},
		}
		if len(recs) != len(want) {
			t.Fatalf("got %d records, want %d", len(recs), len(want))
		}
		for i, w := range want {
			got := recs[i]
			if got.ProcessedAt == nil || !got.ProcessedAt.Equal(w.at) || got.ProcessedAt.Location() != time.UTC {
				t.Errorf("record %d processedAt = %v, want %v UTC", i, got.ProcessedAt, w.at)
			}
			got.ProcessedAt = nil
			if got != w.rec {
				t.Errorf("record %d = %+v\nwant %+v", i, got, w.rec)
			}
		}
	})

	t.Run("headers are case-insensitive, any order, extra columns ignored", func(t *testing.T) {
		recs, err := parse(t, " Status ,FEE,Currency,Amount,Transaction_ID\ncaptured,12,USD,700,X-1\n")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(recs) != 1 || recs[0].TransactionID != "X-1" || recs[0].Amount != 700 || recs[0].Currency != "USD" || recs[0].Status != "captured" {
			t.Errorf("unexpected records: %+v", recs)
		}
	})

	for name, csv := range map[string]string{
		"missing processed_at column": "transaction_id,amount,currency,status\nA,1,ARS,captured\n",
		"empty processed_at":          "transaction_id,amount,currency,status,processed_at\nA,1,ARS,captured,\n",
	} {
		t.Run(name+" leaves it nil", func(t *testing.T) {
			recs, err := parse(t, csv)
			if err != nil || len(recs) != 1 || recs[0].ProcessedAt != nil {
				t.Fatalf("got %+v, %v", recs, err)
			}
		})
	}

	t.Run("unparseable processed_at invalidates the report", func(t *testing.T) {
		_, err := parse(t, "transaction_id,amount,currency,status,processed_at\nA,1,ARS,captured,yesterday\n")
		if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), "row 2") || !strings.Contains(err.Error(), `"yesterday"`) {
			t.Fatalf("expected ErrInvalidReportFormat at row 2, got %v", err)
		}
	})

	t.Run("headers only returns no records", func(t *testing.T) {
		recs, err := parse(t, "transaction_id,amount,currency,status\n")
		if err != nil || len(recs) != 0 {
			t.Fatalf("got %v, %v", recs, err)
		}
	})

	t.Run("empty data returns ErrEmptyReport", func(t *testing.T) {
		if _, err := NewCSVReportParser().Parse(nil); !errors.Is(err, domain.ErrEmptyReport) {
			t.Fatalf("expected ErrEmptyReport, got %v", err)
		}
	})

	t.Run("unreadable header returns ErrInvalidReportFormat", func(t *testing.T) {
		_, err := parse(t, "\"transaction_id,amount\n")
		if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), "cannot read headers") {
			t.Fatalf("expected ErrInvalidReportFormat, got %v", err)
		}
	})

	for _, col := range []string{"transaction_id", "amount", "currency", "status"} {
		t.Run("missing required column "+col, func(t *testing.T) {
			headers := strings.Replace("transaction_id,amount,currency,status", col, "other", 1)
			_, err := parse(t, headers+"\nA,1,ARS,captured\n")
			if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), `"`+col+`"`) {
				t.Fatalf("expected ErrInvalidReportFormat naming %q, got %v", col, err)
			}
		})
	}

	malformed := []struct{ name, csv, row string }{
		{"row with fewer columns", "transaction_id,amount,currency,status\nA,1,ARS,captured\nB,2,ARS\nC,3,ARS,captured\n", "row 3"},
		{"row with extra columns", "transaction_id,amount,currency,status\nA,1,ARS,captured,extra\nB,2,ARS,captured\n", "row 2"},
		{"unterminated quote", "transaction_id,amount,currency,status\nA,1,ARS,captured\n\"B,2,ARS,captured\nC,3,ARS,captured\n", "row 3"},
	}
	for _, tc := range malformed {
		// Antes el parser cortaba en el primer error de lectura y devolvía las
		// filas previas como si el informe estuviera completo.
		t.Run(tc.name+" invalidates the whole report", func(t *testing.T) {
			recs, err := parse(t, tc.csv)
			if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), tc.row) {
				t.Fatalf("expected ErrInvalidReportFormat at %s, got %v", tc.row, err)
			}
			if recs != nil {
				t.Errorf("no partial records should be returned, got %d", len(recs))
			}
		})
	}

	for _, amount := range []string{"12.50", "0", "-100", "abc"} {
		t.Run("invalid amount "+amount+" reports row number", func(t *testing.T) {
			_, err := parse(t, "transaction_id,amount,currency,status\nA,1,ARS,captured\nB,"+amount+",ARS,captured\n")
			if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), "row 3") || !strings.Contains(err.Error(), `"`+amount+`"`) {
				t.Fatalf("expected ErrInvalidReportFormat at row 3, got %v", err)
			}
		})
	}
}

func TestParseRow_TooFewColumns(t *testing.T) {
	// No alcanzable vía Parse (csv.Reader exige el mismo número de campos que
	// el header), pero parseRow se protege igual.
	col := columnMapping{transactionID: 0, amount: 3, currency: 1, status: 2, processedAt: -1}
	_, err := parseRow([]string{"A", "ARS"}, col, 7)
	if !errors.Is(err, domain.ErrInvalidReportFormat) || !strings.Contains(err.Error(), "row 7") {
		t.Fatalf("expected ErrInvalidReportFormat for row 7, got %v", err)
	}
}
