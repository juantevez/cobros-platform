// Package crypto provee generadores de secretos criptográficos para Webhooks.
package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// HexSecretGenerator genera secrets HMAC para endpoints webhook.
//
// Formato: "whsec_<64 hex chars>" (32 bytes aleatorios en hex)
// Ejemplo: "whsec_a3f1b2c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2"
//
// El prefijo "whsec_" sigue la convención de la industria (Stripe, etc.)
// y permite al comercio identificar visualmente el tipo de secreto.
type HexSecretGenerator struct{}

func NewHexSecretGenerator() *HexSecretGenerator { return &HexSecretGenerator{} }

// Generate crea un secret criptográficamente seguro de 32 bytes.
func (g *HexSecretGenerator) Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(b), nil
}
