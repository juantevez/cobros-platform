package application

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// hashRefreshToken calcula el hash con el que se persiste y busca un refresh token.
//
// Debe ser determinístico: FindByHash busca por igualdad, por lo que un hash con
// salt aleatorio (argon2id del PasswordHasher) nunca encontraría el token.
// SHA-256 es suficiente porque el token es un secreto aleatorio de 256 bits,
// no una contraseña elegida por una persona: no hay diccionario que atacar.
func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
