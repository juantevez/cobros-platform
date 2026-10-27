package domain

import (
	"fmt"
	"net/mail"
	"strings"
)

// MaxEmailLength es el límite práctico de longitud de un email (RFC 5321
// permite 254 en el path completo; 320 es el máximo histórico del campo).
const MaxEmailLength = 320

// NormalizeEmail canonicaliza una dirección de email:
//   - recorta espacios,
//   - baja el caso (el dominio es case-insensitive y el local-part se trata
//     como tal en la práctica — evita que "Ops@X.com" y "ops@x.com" convivan
//     como overrides distintos),
//   - retorna "" para entrada vacía/espacios.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateRecipientEmail valida y canonicaliza el recipient_email de una
// preferencia. El valor vacío es válido: significa "sin override, usar el
// email del admin del tenant".
func ValidateRecipientEmail(s string) (string, error) {
	email := NormalizeEmail(s)
	if email == "" {
		return "", nil
	}
	if len(email) > MaxEmailLength {
		return "", fmt.Errorf("recipient email: exceeds max length of %d characters", MaxEmailLength)
	}
	// mail.ParseAddress exige una dirección RFC 5322 válida (single @, sin
	// espacios internos, dominio con punto, etc.).
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fmt.Errorf("recipient email: invalid format %q", s)
	}
	return email, nil
}
