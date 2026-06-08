package crypto

import (
	"crypto/sha256"
)

// SHA256Hasher implementa HashComputer usando SHA-256.
// No necesita salt porque el payload incluye datos únicos (timestamps, IDs, prev_hash).
type SHA256Hasher struct{}

func NewSHA256Hasher() *SHA256Hasher { return &SHA256Hasher{} }

// Compute calcula SHA-256 del payload y retorna los 32 bytes del digest.
func (h *SHA256Hasher) Compute(payload string) []byte {
	sum := sha256.Sum256([]byte(payload))
	return sum[:]
}
