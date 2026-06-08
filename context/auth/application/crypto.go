package application

import (
	"crypto/rand"
	"fmt"
)

// base62Alphabet son los caracteres usados para codificar secretos.
// Solo alfanuméricos: legibles, sin ambigüedad visual, URL-safe.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// generateSecret genera un secreto aleatorio de n bytes codificado en base62.
//
// Nota sobre el bias: 256 % 62 != 0, por lo que los primeros 4 caracteres del
// alfabeto tienen una probabilidad marginalmente mayor. Para secretos de API key
// (no criptografía de alta seguridad) esto es aceptable. Si se requiere
// uniformidad estricta, usar rejection sampling.
func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}

	result := make([]byte, n)
	for i, byteVal := range b {
		result[i] = base62Alphabet[int(byteVal)%62]
	}
	return string(result), nil
}
