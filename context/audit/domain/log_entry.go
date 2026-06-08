package domain

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AuditLogEntry es un registro inmutable de una acción sensible del sistema.
//
// Forma una cadena de hash tamper-evident:
//
//	hash_n = SHA-256( hex(hash_{n-1}) | tenantID | actor | action | resourceType | resourceID | createdAt )
//
// Si alguien modifica cualquier campo de la entrada k, su hash cambia y deja
// de coincidir con el prev_hash de k+1: la manipulación queda evidenciada.
//
// El ID es BIGSERIAL (entero secuencial) para garantizar el orden de la cadena
// sin depender de timestamps que pueden repetirse.
type AuditLogEntry struct {
	id            int64  // BIGSERIAL asignado por la base al insertar
	tenantID      string // vacío para acciones de plataforma
	actor         string // userID, "system", o "relay"
	action        Action
	resourceType  ResourceType
	resourceID    string
	metadata      map[string]string
	correlationID string
	prevHash      []byte // nil para el primer registro del sistema
	hash          []byte // calculado al construir
	createdAt     time.Time
}

// NewAuditLogEntry construye una entrada calculando su hash.
// prevHash es el hash del último registro existente (nil si es el primero).
// La función computeHash es inyectada para no acoplar el dominio a crypto.
func NewAuditLogEntry(
	tenantID string,
	actor string,
	action Action,
	resourceType ResourceType,
	resourceID string,
	metadata map[string]string,
	correlationID string,
	prevHash []byte,
	createdAt time.Time,
	computeHash func(payload string) []byte,
) (*AuditLogEntry, error) {
	if actor == "" {
		actor = "system"
	}

	e := &AuditLogEntry{
		tenantID:      tenantID,
		actor:         actor,
		action:        action,
		resourceType:  resourceType,
		resourceID:    resourceID,
		metadata:      metadata,
		correlationID: correlationID,
		prevHash:      prevHash,
		createdAt:     createdAt.UTC(),
	}

	e.hash = computeHash(e.canonicalPayload())
	return e, nil
}

// ReconstituteAuditLogEntry reconstruye una entrada desde la base sin recalcular.
func ReconstituteAuditLogEntry(
	id int64,
	tenantID, actor string,
	action Action,
	resourceType ResourceType,
	resourceID string,
	metadata map[string]string,
	correlationID string,
	prevHash, hash []byte,
	createdAt time.Time,
) *AuditLogEntry {
	return &AuditLogEntry{
		id:            id,
		tenantID:      tenantID,
		actor:         actor,
		action:        action,
		resourceType:  resourceType,
		resourceID:    resourceID,
		metadata:      metadata,
		correlationID: correlationID,
		prevHash:      prevHash,
		hash:          hash,
		createdAt:     createdAt.UTC(),
	}
}

// VerifyHash verifica que el hash almacenado coincide con el recalculado.
// Usa la misma función de hash que NewAuditLogEntry.
func (e *AuditLogEntry) VerifyHash(computeHash func(payload string) []byte) bool {
	expected := computeHash(e.canonicalPayload())
	if len(expected) != len(e.hash) {
		return false
	}
	// Comparación byte a byte (no necesita ser de tiempo constante: es verificación, no autenticación).
	for i := range expected {
		if expected[i] != e.hash[i] {
			return false
		}
	}
	return true
}

// canonicalPayload construye el string determinista sobre el que se calcula el hash.
// El orden y formato de los campos es fijo y no debe cambiar.
func (e *AuditLogEntry) canonicalPayload() string {
	prevHashHex := ""
	if len(e.prevHash) > 0 {
		prevHashHex = hex.EncodeToString(e.prevHash)
	}
	return strings.Join([]string{
		prevHashHex,
		e.tenantID,
		e.actor,
		e.action.String(),
		e.resourceType.String(),
		e.resourceID,
		e.createdAt.UTC().Format(time.RFC3339Nano),
	}, "|")
}

// HashHex retorna el hash como string hexadecimal (para mostrar en APIs).
func (e *AuditLogEntry) HashHex() string {
	if len(e.hash) == 0 {
		return ""
	}
	return hex.EncodeToString(e.hash)
}

func (e *AuditLogEntry) PrevHashHex() string {
	if len(e.prevHash) == 0 {
		return ""
	}
	return hex.EncodeToString(e.prevHash)
}

// ChainValid verifica que este entry enlaza correctamente con el anterior.
func (e *AuditLogEntry) ChainLinksTo(prev *AuditLogEntry) bool {
	if prev == nil {
		return len(e.prevHash) == 0
	}
	if len(e.prevHash) != len(prev.hash) {
		return false
	}
	for i := range prev.hash {
		if e.prevHash[i] != prev.hash[i] {
			return false
		}
	}
	return true
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (e *AuditLogEntry) ID() int64                   { return e.id }
func (e *AuditLogEntry) TenantID() string            { return e.tenantID }
func (e *AuditLogEntry) Actor() string               { return e.actor }
func (e *AuditLogEntry) Action() Action              { return e.action }
func (e *AuditLogEntry) ResourceType() ResourceType  { return e.resourceType }
func (e *AuditLogEntry) ResourceID() string          { return e.resourceID }
func (e *AuditLogEntry) Metadata() map[string]string { return e.metadata }
func (e *AuditLogEntry) CorrelationID() string       { return e.correlationID }
func (e *AuditLogEntry) PrevHash() []byte            { return e.prevHash }
func (e *AuditLogEntry) Hash() []byte                { return e.hash }
func (e *AuditLogEntry) CreatedAt() time.Time        { return e.createdAt }

func (e *AuditLogEntry) String() string {
	return fmt.Sprintf("AuditLogEntry{id=%d action=%s resource=%s/%s hash=%s}",
		e.id, e.action, e.resourceType, e.resourceID, e.HashHex()[:8])
}
