package domain

import "strings"

// Template define el subject y body de una notificación de email.
// Los placeholders tienen formato {{.key}}.
//
// Están hardcodeados en Fase 3. En Fase 4 se almacenan en BD y
// el operador puede editarlos via API.
type Template struct {
	Subject string
	Body    string // HTML básico o texto plano
}

// Render sustituye los placeholders con los valores del mapa dado.
func (t Template) Render(data map[string]string) (subject, body string) {
	subject = replacePlaceholders(t.Subject, data)
	body = replacePlaceholders(t.Body, data)
	return
}

func replacePlaceholders(s string, data map[string]string) string {
	for k, v := range data {
		s = strings.ReplaceAll(s, "{{."+k+"}}", v)
	}
	return s
}

// ── Catálogo de templates ─────────────────────────────────────────────────────
// La clave es el event type normalizado (sin versión).

var Templates = map[string]Template{
	"payment.captured": {
		Subject: "✅ Pago recibido: {{.amount}} {{.currency}}",
		Body: `<p>Hola,</p>
<p>Se acreditó un nuevo pago en tu cuenta:</p>
<ul>
  <li><strong>Monto:</strong> {{.amount}} {{.currency}}</li>
  <li><strong>Método:</strong> {{.payment_method}}</li>
  <li><strong>Referencia:</strong> {{.payment_id}}</li>
</ul>
<p>El saldo estará disponible para desembolso en el próximo ciclo.</p>`,
	},

	"payment.failed": {
		Subject: "❌ Pago rechazado por {{.amount}} {{.currency}}",
		Body: `<p>Hola,</p>
<p>Un pago fue rechazado:</p>
<ul>
  <li><strong>Monto:</strong> {{.amount}} {{.currency}}</li>
  <li><strong>Motivo:</strong> {{.failure_reason}}</li>
  <li><strong>Referencia:</strong> {{.payment_id}}</li>
</ul>
<p>El pagador puede intentarlo nuevamente.</p>`,
	},

	"payment.refunded": {
		Subject: "↩️ Reembolso procesado: {{.amount}} {{.currency}}",
		Body: `<p>Hola,</p>
<p>Se procesó un reembolso:</p>
<ul>
  <li><strong>Monto reembolsado:</strong> {{.amount}} {{.currency}}</li>
  <li><strong>Referencia:</strong> {{.payment_id}}</li>
</ul>`,
	},

	"payout.confirmed": {
		Subject: "🏦 Desembolso confirmado: {{.amount}} {{.currency}}",
		Body: `<p>Hola,</p>
<p>Tu desembolso fue confirmado por el banco:</p>
<ul>
  <li><strong>Monto:</strong> {{.amount}} {{.currency}}</li>
  <li><strong>Referencia bancaria:</strong> {{.bank_reference}}</li>
</ul>
<p>Los fondos deberían acreditarse en tu cuenta en 1-2 días hábiles.</p>`,
	},

	"payout.failed": {
		Subject: "⚠️ Desembolso fallido: {{.amount}} {{.currency}}",
		Body: `<p>Hola,</p>
<p>Tu desembolso no pudo procesarse:</p>
<ul>
  <li><strong>Monto:</strong> {{.amount}} {{.currency}}</li>
  <li><strong>Motivo:</strong> {{.failure_reason}}</li>
</ul>
<p>Los fondos fueron devueltos a tu saldo disponible. Por favor verificá los datos de tu cuenta bancaria.</p>`,
	},

	"kyc.approved": {
		Subject: "🎉 Tu cuenta fue aprobada",
		Body: `<p>Hola,</p>
<p>¡Buenas noticias! Tu solicitud de verificación fue aprobada.</p>
<p>Tu cuenta está ahora habilitada para procesar pagos en modo producción.</p>`,
	},

	"kyc.rejected": {
		Subject: "Tu solicitud de verificación fue rechazada",
		Body: `<p>Hola,</p>
<p>Lamentablemente tu solicitud de verificación fue rechazada.</p>
<p><strong>Motivo:</strong> {{.rejection_reason}}</p>
<p>Por favor contacta a soporte para más información.</p>`,
	},

	"auth.tenant.suspended": {
		Subject: "Tu cuenta fue suspendida",
		Body: `<p>Hola,</p>
<p>Tu cuenta en la plataforma fue suspendida.</p>
<p>Por favor contacta a soporte para resolver esta situación.</p>`,
	},
}

// FindTemplate busca el template para un event type.
// Soporta subjects con y sin versión: "payment.captured.v1" → "payment.captured".
func FindTemplate(eventType string) (Template, bool) {
	// Intentar con el event type completo.
	if t, ok := Templates[eventType]; ok {
		return t, true
	}
	// Intentar sin el sufijo de versión.
	normalized := stripVersion(eventType)
	t, ok := Templates[normalized]
	return t, ok
}

// EventTypesToTemplates retorna los event types que tienen template registrado.
func EventTypesToTemplates() []string {
	keys := make([]string, 0, len(Templates))
	for k := range Templates {
		keys = append(keys, k)
	}
	return keys
}

func stripVersion(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		if len(last) > 1 && last[0] == 'v' {
			allDigits := true
			for _, c := range last[1:] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return strings.Join(parts[:len(parts)-1], ".")
			}
		}
	}
	return s
}
