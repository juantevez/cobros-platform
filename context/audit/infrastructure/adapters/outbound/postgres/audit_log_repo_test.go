package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/juantevez/cobros-platform/context/audit/domain"
)

// fakeRow implementa pgx.Row copiando valores predefinidos en los destinos.
// El orden de vals debe coincidir con el de las columnas del SELECT.
type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *int64:
			*p = r.vals[i].(int64)
		case **string:
			*p, _ = r.vals[i].(*string)
		case *string:
			*p = r.vals[i].(string)
		case *[]byte:
			*p, _ = r.vals[i].([]byte)
		case *time.Time:
			*p = r.vals[i].(time.Time)
		}
	}
	return nil
}

func strp(s string) *string { return &s }

func TestScanEntry(t *testing.T) {
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	t.Run("full row reconstituted", func(t *testing.T) {
		row := fakeRow{vals: []any{
			int64(42),                   // id
			strp("tenant-1"),            // tenant_id
			"actor-1",                   // actor
			"auth.user.login",           // action
			"user",                      // resource_type
			strp("user-1"),              // resource_id
			[]byte(`{"ip":"10.0.0.1"}`), // metadata
			strp("corr-1"),              // correlation_id
			[]byte{0xAA, 0xBB},          // prev_hash
			[]byte{0xCC, 0xDD},          // hash
			createdAt,                   // created_at
		}}

		e, err := scanEntry(row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.ID() != 42 || e.TenantID() != "tenant-1" || e.Actor() != "actor-1" {
			t.Errorf("scalar fields mismatch: %+v", e)
		}
		if e.Action() != domain.ActionLogin || e.ResourceType() != domain.ResourceUser {
			t.Errorf("action/resource mismatch: %v / %v", e.Action(), e.ResourceType())
		}
		if e.ResourceID() != "user-1" || e.CorrelationID() != "corr-1" {
			t.Errorf("resourceID/correlationID mismatch")
		}
		if e.Metadata()["ip"] != "10.0.0.1" {
			t.Errorf("metadata mismatch: %+v", e.Metadata())
		}
		if e.HashHex() != "ccdd" || e.PrevHashHex() != "aabb" {
			t.Errorf("hash mismatch: %s / %s", e.HashHex(), e.PrevHashHex())
		}
		if !e.CreatedAt().Equal(createdAt) || e.CreatedAt().Location() != time.UTC {
			t.Errorf("createdAt mismatch: %v", e.CreatedAt())
		}
	})

	t.Run("null optional columns become empty", func(t *testing.T) {
		row := fakeRow{vals: []any{
			int64(1),
			(*string)(nil), // tenant_id NULL
			"system",
			"ledger.entry.posted",
			"journal_entry",
			(*string)(nil), // resource_id NULL
			[]byte(nil),    // metadata NULL
			(*string)(nil), // correlation_id NULL
			[]byte(nil),    // prev_hash NULL (genesis)
			[]byte{0x01},
			createdAt,
		}}

		e, err := scanEntry(row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e.TenantID() != "" || e.ResourceID() != "" || e.CorrelationID() != "" {
			t.Errorf("expected empty optional fields: %+v", e)
		}
		if e.Metadata() != nil {
			t.Errorf("expected nil metadata, got %+v", e.Metadata())
		}
		if e.PrevHashHex() != "" {
			t.Errorf("expected empty prevHash, got %q", e.PrevHashHex())
		}
	})

	t.Run("scan error propagated", func(t *testing.T) {
		_, err := scanEntry(fakeRow{err: errors.New("no rows")})
		if err == nil {
			t.Fatal("expected scan error")
		}
	})

	t.Run("invalid metadata json rejected", func(t *testing.T) {
		row := fakeRow{vals: []any{
			int64(1), (*string)(nil), "system", "auth.user.login", "user",
			(*string)(nil), []byte(`{bad json`), (*string)(nil),
			[]byte(nil), []byte{0x01}, createdAt,
		}}
		_, err := scanEntry(row)
		if err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
}

func TestNullableStr(t *testing.T) {
	if nullableStr("") != nil {
		t.Error("empty string should map to nil")
	}
	if got := nullableStr("x"); got == nil || *got != "x" {
		t.Errorf("non-empty mapped wrong: %v", got)
	}
}

func TestNullableBytes(t *testing.T) {
	if nullableBytes(nil) != nil {
		t.Error("nil should stay nil")
	}
	if nullableBytes([]byte{}) != nil {
		t.Error("empty slice should map to nil")
	}
	b := []byte{0x01, 0x02}
	if got := nullableBytes(b); len(got) != 2 {
		t.Errorf("non-empty mapped wrong: %v", got)
	}
}

func TestDeref(t *testing.T) {
	if deref(nil) != "" {
		t.Error("nil pointer should deref to empty")
	}
	if deref(strp("v")) != "v" {
		t.Error("pointer should deref to value")
	}
}
