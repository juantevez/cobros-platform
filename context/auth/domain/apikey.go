package domain

import (
	"fmt"
	"time"
)

// ApiKey es el agregado que representa una credencial servidor-a-servidor.
//
// El secreto nunca se almacena en claro: solo se guarda el prefix (para
// identificar la key sin exponerla) y el hash del secreto (para verificar).
//
// Formato de una API key completa (solo visible al momento de creación):
//
//	<env>_<prefix>_<secret>
//	Ejemplo: test_Xk3mPQ_7fGhJ9kLpQrStUvWx...
//
// Proceso de verificación:
//  1. El cliente envía la key completa en el header X-Api-Key.
//  2. El sistema extrae el prefix (parseando por "_").
//  3. Busca la ApiKey por prefix en el repositorio.
//  4. Usa el PasswordHasher para verificar secret contra keyHash.
//
// Invariantes:
//  1. Una key revocada no puede ser usada ni re-activada.
//  2. El keyHash nunca está vacío en una key persistida.
//  3. El prefix es único dentro del tenant.
type ApiKey struct {
	id          ApiKeyID
	tenantID    TenantID
	name        string      // nombre descriptivo, ej: "Integración WooCommerce"
	prefix      string      // 8 chars, visible para identificación
	keyHash     string      // hash del secreto; calculado por el caso de uso
	environment Environment
	scopes      []Scope
	revokedAt   *time.Time
	createdAt   time.Time

	events []Event
}

// NewApiKey crea una nueva ApiKey.
//
// prefix y keyHash son calculados por el caso de uso IssueApiKey:
//   - prefix: primeros 8 chars del secreto en base62
//   - keyHash: resultado de PasswordHasher.Hash(secret)
func NewApiKey(
	id ApiKeyID,
	tenantID TenantID,
	name string,
	prefix string,
	keyHash string,
	env Environment,
	scopes []Scope,
) (*ApiKey, error) {
	if name == "" {
		return nil, fmt.Errorf("api key name cannot be empty")
	}
	if prefix == "" {
		return nil, fmt.Errorf("api key prefix cannot be empty")
	}
	if keyHash == "" {
		return nil, fmt.Errorf("api key hash cannot be empty")
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("api key must have at least one scope")
	}

	k := &ApiKey{
		id:          id,
		tenantID:    tenantID,
		name:        name,
		prefix:      prefix,
		keyHash:     keyHash,
		environment: env,
		scopes:      scopes,
		createdAt:   time.Now().UTC(),
	}

	k.record(ApiKeyIssuedEvent{
		baseEvent:   newBase(tenantID.String()),
		TenantID:    tenantID.String(),
		ApiKeyID:    id.String(),
		Prefix:      prefix,
		Environment: env.String(),
	})

	return k, nil
}

// ReconstituteApiKey reconstruye una ApiKey desde el repositorio.
func ReconstituteApiKey(
	id ApiKeyID,
	tenantID TenantID,
	name, prefix, keyHash string,
	env Environment,
	scopes []Scope,
	revokedAt *time.Time,
	createdAt time.Time,
) *ApiKey {
	return &ApiKey{
		id:          id,
		tenantID:    tenantID,
		name:        name,
		prefix:      prefix,
		keyHash:     keyHash,
		environment: env,
		scopes:      scopes,
		revokedAt:   revokedAt,
		createdAt:   createdAt,
	}
}

// Revoke revoca la API key. Una key revocada no puede usarse ni re-activarse.
func (k *ApiKey) Revoke() error {
	if k.IsRevoked() {
		return ErrApiKeyAlreadyRevoked
	}
	now := time.Now().UTC()
	k.revokedAt = &now

	k.record(ApiKeyRevokedEvent{
		baseEvent: newBase(k.tenantID.String()),
		TenantID:  k.tenantID.String(),
		ApiKeyID:  k.id.String(),
	})

	return nil
}

// HasScope retorna true si la key tiene el scope requerido.
func (k *ApiKey) HasScope(s Scope) bool {
	for _, sc := range k.scopes {
		if sc == s {
			return true
		}
	}
	return false
}

// IsRevoked retorna true si la key fue revocada.
func (k *ApiKey) IsRevoked() bool { return k.revokedAt != nil }

// ── Getters ───────────────────────────────────────────────────────────────────

func (k *ApiKey) ID() ApiKeyID          { return k.id }
func (k *ApiKey) TenantID() TenantID    { return k.tenantID }
func (k *ApiKey) Name() string          { return k.name }
func (k *ApiKey) Prefix() string        { return k.prefix }
func (k *ApiKey) KeyHash() string       { return k.keyHash }
func (k *ApiKey) Environment() Environment { return k.environment }
func (k *ApiKey) Scopes() []Scope       { return k.scopes }
func (k *ApiKey) RevokedAt() *time.Time { return k.revokedAt }
func (k *ApiKey) CreatedAt() time.Time  { return k.createdAt }

func (k *ApiKey) PullEvents() []Event {
	evs := k.events
	k.events = nil
	return evs
}

func (k *ApiKey) record(e Event) {
	k.events = append(k.events, e)
}

// ── Parser de raw API key ─────────────────────────────────────────────────────

// ParsedApiKey contiene las partes de una API key tal como la envía el cliente.
type ParsedApiKey struct {
	Environment string // "test" o "production"
	Prefix      string // 8 chars para buscar en la base
	Secret      string // para verificar contra el hash
}

// ParseRawApiKey descompone una API key en sus partes.
//
// Formato esperado: "<env>_<prefix>_<secret>"
// Ejemplo:          "test_Xk3mPQrS_7fGhJ9kL..."
func ParseRawApiKey(raw string) (ParsedApiKey, error) {
	// Dividimos en exactamente 3 partes por "_" con límite
	// para no romper si el secret contiene "_".
	// Usamos índices de string para evitar importar strings.
	first := -1
	second := -1
	for i, c := range raw {
		if c == '_' {
			if first == -1 {
				first = i
			} else if second == -1 {
				second = i
				break
			}
		}
	}

	if first == -1 || second == -1 {
		return ParsedApiKey{}, fmt.Errorf("invalid api key format")
	}

	env := raw[:first]
	prefix := raw[first+1 : second]
	secret := raw[second+1:]

	if env == "" || prefix == "" || secret == "" {
		return ParsedApiKey{}, fmt.Errorf("invalid api key format: empty parts")
	}

	return ParsedApiKey{
		Environment: env,
		Prefix:      prefix,
		Secret:      secret,
	}, nil
}
