package domain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// AuditLogEntry es un registro inmutable de una acción sensible del sistema.
//
// Forma una cadena de hash tamper-evident:
//
//	hash_n = SHA-256( JSON[hex(hash_{n-1}), tenantID, actor, action, resourceType,
//	                       resourceID, createdAt, correlationID, metadata, eventID, occurredAt] )
//
// (ver canonicalPayload). Si alguien modifica cualquier campo de la entrada k, su hash cambia y deja
// de coincidir con el prev_hash de k+1: la manipulación queda evidenciada.
//
// El ID es BIGSERIAL (entero secuencial) para garantizar el orden de la cadena
// sin depender de timestamps que pueden repetirse.
type AuditLogEntry struct {
	id            int64  // BIGSERIAL asignado por la base al insertar
	tenantID      string // vacío para acciones de plataforma
	actor         string // userID, "apikey:<id>" o "system"
	action        Action
	resourceType  ResourceType
	resourceID    string
	metadata      map[string]string
	correlationID string
	eventID       string    // ID del evento de dominio que originó la entrada (idempotencia)
	occurredAt    time.Time // cuándo ocurrió el hecho (hora del evento)
	prevHash      []byte    // nil para el primer registro del sistema
	hash          []byte    // calculado al construir
	createdAt     time.Time // cuándo se registró en la bitácora
}

// EntryInput son los datos de una acción a registrar.
type EntryInput struct {
	TenantID      string
	Actor         string // vacío → "system"
	Action        Action
	ResourceType  ResourceType
	ResourceID    string
	Metadata      map[string]string
	CorrelationID string
	EventID       string    // vacío si la acción no viene de un evento
	OccurredAt    time.Time // cero → createdAt
}

// NewAuditLogEntry construye una entrada calculando su hash.
// prevHash es el hash del último registro existente (nil si es el primero);
// createdAt es el momento del registro. La función computeHash es inyectada
// para no acoplar el dominio a crypto.
func NewAuditLogEntry(
	in EntryInput,
	prevHash []byte,
	createdAt time.Time,
	computeHash func(payload string) []byte,
) (*AuditLogEntry, error) {
	actor := in.Actor
	if actor == "" {
		actor = "system"
	}
	occurredAt := in.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = createdAt
	}

	e := &AuditLogEntry{
		tenantID:      in.TenantID,
		actor:         actor,
		action:        in.Action,
		resourceType:  in.ResourceType,
		resourceID:    in.ResourceID,
		metadata:      in.Metadata,
		correlationID: in.CorrelationID,
		eventID:       in.EventID,
		occurredAt:    TruncateToStorage(occurredAt),
		prevHash:      prevHash,
		createdAt:     TruncateToStorage(createdAt),
	}

	e.hash = computeHash(e.canonicalPayload())
	return e, nil
}

// StoragePrecision es la precisión de created_at en PostgreSQL (TIMESTAMPTZ
// guarda microsegundos). El hash se calcula sobre el timestamp ya truncado:
// si se calculara con los nanosegundos de time.Now(), al releer la fila el
// payload canónico cambiaría y VerifyHash fallaría siempre.
const StoragePrecision = time.Microsecond

// TruncateToStorage lleva un instante a la precisión con que se persiste.
func TruncateToStorage(t time.Time) time.Time { return t.UTC().Truncate(StoragePrecision) }

// EntrySnapshot es el estado persistido de una entrada.
type EntrySnapshot struct {
	ID            int64
	TenantID      string
	Actor         string
	Action        Action
	ResourceType  ResourceType
	ResourceID    string
	Metadata      map[string]string
	CorrelationID string
	EventID       string
	OccurredAt    time.Time
	PrevHash      []byte
	Hash          []byte
	CreatedAt     time.Time
}

// ReconstituteAuditLogEntry reconstruye una entrada desde la base sin recalcular.
func ReconstituteAuditLogEntry(s EntrySnapshot) *AuditLogEntry {
	occurredAt := s.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.CreatedAt
	}
	return &AuditLogEntry{
		id:            s.ID,
		tenantID:      s.TenantID,
		actor:         s.Actor,
		action:        s.Action,
		resourceType:  s.ResourceType,
		resourceID:    s.ResourceID,
		metadata:      s.Metadata,
		correlationID: s.CorrelationID,
		eventID:       s.EventID,
		occurredAt:    occurredAt.UTC(),
		prevHash:      s.PrevHash,
		hash:          s.Hash,
		createdAt:     s.CreatedAt.UTC(),
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

// canonicalPayload construye el string determinista sobre el que se calcula
// el hash: un array JSON con TODOS los campos de la entrada, en orden fijo.
//
//	["<hex prev_hash>", tenant_id, actor, action, resource_type, resource_id,
//	 "<created_at RFC3339Nano UTC>", correlation_id, {metadata}, event_id,
//	 "<occurred_at RFC3339Nano UTC>"]
//
// JSON y no un join con separador: un valor que contenga el separador no
// puede hacer colisionar dos entradas distintas. metadata se serializa con
// las claves ordenadas (encoding/json ordena los mapas) y nil equivale a {}:
// así el payload es el mismo antes de persistir y después de leer de JSONB.
// El orden y el formato no deben cambiar: invalidarían la cadena existente.
func (e *AuditLogEntry) canonicalPayload() string {
	prevHashHex := ""
	if len(e.prevHash) > 0 {
		prevHashHex = hex.EncodeToString(e.prevHash)
	}
	metadata := e.metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload, err := json.Marshal([]any{
		prevHashHex,
		e.tenantID,
		e.actor,
		e.action.String(),
		e.resourceType.String(),
		e.resourceID,
		e.createdAt.UTC().Format(time.RFC3339Nano),
		e.correlationID,
		metadata,
		e.eventID,
		e.occurredAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		// Solo strings y map[string]string: Marshal no puede fallar.
		panic(fmt.Sprintf("audit: canonical payload: %v", err))
	}
	return string(payload)
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
func (e *AuditLogEntry) EventID() string             { return e.eventID }
func (e *AuditLogEntry) OccurredAt() time.Time       { return e.occurredAt }
func (e *AuditLogEntry) PrevHash() []byte            { return e.prevHash }
func (e *AuditLogEntry) Hash() []byte                { return e.hash }
func (e *AuditLogEntry) CreatedAt() time.Time        { return e.createdAt }

func (e *AuditLogEntry) String() string {
	return fmt.Sprintf("AuditLogEntry{id=%d action=%s resource=%s/%s hash=%s}",
		e.id, e.action, e.resourceType, e.resourceID, e.HashHex()[:8])
}
