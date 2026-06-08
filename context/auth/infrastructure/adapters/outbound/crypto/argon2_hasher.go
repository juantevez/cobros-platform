package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// params define los parámetros de coste del algoritmo argon2id.
// Ajustar según el hardware objetivo; estos valores son conservadores
// y razonables para un servidor moderno (~80ms por operación).
type params struct {
	memory      uint32 // KiB de memoria usados
	iterations  uint32 // número de pasadas
	parallelism uint8  // grados de paralelismo
	saltLength  uint32 // bytes del salt aleatorio
	keyLength   uint32 // bytes del hash resultante
}

var defaultParams = params{
	memory:      64 * 1024, // 64 MiB
	iterations:  3,
	parallelism: 2,
	saltLength:  16,
	keyLength:   32,
}

// Argon2Hasher implementa application.PasswordHasher con argon2id.
type Argon2Hasher struct {
	p params
}

// NewArgon2Hasher crea un Argon2Hasher con los parámetros por defecto.
func NewArgon2Hasher() *Argon2Hasher {
	return &Argon2Hasher{p: defaultParams}
}

// Hash calcula el hash de plaintext con un salt aleatorio.
// Cada llamada produce un resultado distinto (salt diferente).
func (h *Argon2Hasher) Hash(plaintext string) (string, error) {
	salt := make([]byte, h.p.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2: generate salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(plaintext),
		salt,
		h.p.iterations,
		h.p.memory,
		h.p.parallelism,
		h.p.keyLength,
	)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.p.memory,
		h.p.iterations,
		h.p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// Verify compara plaintext contra un hash almacenado en formato PHC.
// Es de tiempo constante para prevenir timing attacks.
func (h *Argon2Hasher) Verify(plaintext, encoded string) (bool, error) {
	p, salt, storedHash, err := decodeHash(encoded)
	if err != nil {
		return false, fmt.Errorf("argon2: decode hash: %w", err)
	}

	otherHash := argon2.IDKey(
		[]byte(plaintext),
		salt,
		p.iterations,
		p.memory,
		p.parallelism,
		p.keyLength,
	)

	// subtle.ConstantTimeCompare evita que el tiempo de comparación
	// revele información sobre cuántos bytes coinciden.
	return subtle.ConstantTimeCompare(storedHash, otherHash) == 1, nil
}

// decodeHash parsea el formato PHC: $argon2id$v=19$m=X,t=Y,p=Z$salt$hash
func decodeHash(encoded string) (p params, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// Formato: ["", "argon2id", "v=19", "m=X,t=Y,p=Z", "<salt>", "<hash>"]
	if len(parts) != 6 {
		return params{}, nil, nil, errors.New("invalid hash format: expected 6 parts")
	}
	if parts[1] != "argon2id" {
		return params{}, nil, nil, fmt.Errorf("invalid hash algorithm: %q", parts[1])
	}

	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil {
		return params{}, nil, nil, fmt.Errorf("invalid version field: %w", scanErr)
	}
	if version != argon2.Version {
		return params{}, nil, nil, fmt.Errorf("incompatible argon2 version: %d", version)
	}

	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&p.memory, &p.iterations, &p.parallelism); scanErr != nil {
		return params{}, nil, nil, fmt.Errorf("invalid cost params: %w", scanErr)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("decode salt: %w", err)
	}
	p.saltLength = uint32(len(salt))

	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("decode hash: %w", err)
	}
	p.keyLength = uint32(len(hash))

	return p, salt, hash, nil
}
