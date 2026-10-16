package domain

import (
	"sort"
	"strings"
	"testing"
)

func TestTemplate_Render(t *testing.T) {
	tpl := Template{
		Subject: "Pago {{.amount}} {{.currency}}",
		Body:    "<p>Monto: {{.amount}} {{.currency}}, ref {{.payment_id}}</p>",
	}
	subject, body := tpl.Render(map[string]string{
		"amount":     "1000",
		"currency":   "ARS",
		"payment_id": "pay-1",
	})

	if subject != "Pago 1000 ARS" {
		t.Errorf("subject = %q", subject)
	}
	if body != "<p>Monto: 1000 ARS, ref pay-1</p>" {
		t.Errorf("body = %q", body)
	}
}

func TestTemplate_Render_MissingPlaceholderLeftIntact(t *testing.T) {
	tpl := Template{Subject: "Hola {{.name}}", Body: "{{.missing}}"}
	subject, body := tpl.Render(map[string]string{"name": "Ana"})

	if subject != "Hola Ana" {
		t.Errorf("subject = %q", subject)
	}
	if body != "{{.missing}}" {
		t.Errorf("missing placeholder should stay literal, got %q", body)
	}
}

func TestTemplate_Render_EmptyData(t *testing.T) {
	tpl := Template{Subject: "static", Body: "{{.x}}"}
	subject, body := tpl.Render(map[string]string{})
	if subject != "static" || body != "{{.x}}" {
		t.Errorf("render with empty data changed content: %q / %q", subject, body)
	}
}

func TestFindTemplate(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		tpl, ok := FindTemplate("payment.captured")
		if !ok {
			t.Fatal("expected template for payment.captured")
		}
		if tpl.Subject == "" || tpl.Body == "" {
			t.Error("template should have subject and body")
		}
	})

	t.Run("versioned normalizes to base", func(t *testing.T) {
		tpl, ok := FindTemplate("payment.captured.v1")
		if !ok {
			t.Fatal("expected template for versioned event type")
		}
		base := Templates["payment.captured"]
		if tpl.Subject != base.Subject {
			t.Errorf("versioned lookup did not resolve to base template")
		}
	})

	t.Run("unknown event type", func(t *testing.T) {
		if _, ok := FindTemplate("does.not.exist"); ok {
			t.Error("expected no template for unknown event type")
		}
	})

	t.Run("non-numeric version suffix not stripped", func(t *testing.T) {
		if _, ok := FindTemplate("payment.captured.vX"); ok {
			t.Error("suffix .vX should not be treated as a version")
		}
	})
}

func TestEventTypesToTemplates(t *testing.T) {
	got := EventTypesToTemplates()
	if len(got) != len(Templates) {
		t.Fatalf("got %d keys, want %d", len(got), len(Templates))
	}

	sort.Strings(got)
	for _, k := range got {
		if _, ok := Templates[k]; !ok {
			t.Errorf("returned key %q not in Templates", k)
		}
	}
}

func TestTemplates_RenderAllHaveNonEmptyContent(t *testing.T) {
	for eventType, tpl := range Templates {
		if strings.TrimSpace(tpl.Subject) == "" {
			t.Errorf("%q has empty subject", eventType)
		}
		if strings.TrimSpace(tpl.Body) == "" {
			t.Errorf("%q has empty body", eventType)
		}
	}
}
