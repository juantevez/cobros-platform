package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type DisputeID string
type EvidenceID string
type TenantID string

func NewDisputeID() DisputeID   { return DisputeID(uuid.NewString()) }
func NewEvidenceID() EvidenceID { return EvidenceID(uuid.NewString()) }

func ParseDisputeID(s string) (DisputeID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid dispute id: %w", err)
	}
	return DisputeID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id DisputeID) String() string  { return string(id) }
func (id EvidenceID) String() string { return string(id) }
func (id TenantID) String() string   { return string(id) }

// ── DisputeStatus ─────────────────────────────────────────────────────────────

// DisputeStatus representa el estado del ciclo de vida de una disputa.
type DisputeStatus string

const (
	// StatusOpen: disputa abierta por el banco/pagador. El comercio debe responder.
	StatusOpen DisputeStatus = "open"
	// StatusUnderReview: el comercio contestó con evidencia; el banco la está revisando.
	StatusUnderReview DisputeStatus = "under_review"
	// StatusWon: el banco resolvió a favor del comercio. Fondos liberados.
	StatusWon DisputeStatus = "won"
	// StatusLost: el banco resolvió a favor del pagador. Fondos retirados del comercio.
	StatusLost DisputeStatus = "lost"
	// StatusAccepted: el comercio aceptó la disputa voluntariamente (sin contestar).
	StatusAccepted DisputeStatus = "accepted"
	// StatusExpired: el comercio no respondió antes del deadline. Equivale a Lost.
	StatusExpired DisputeStatus = "expired"
)

func (s DisputeStatus) String() string { return string(s) }
func (s DisputeStatus) IsFinal() bool {
	return s == StatusWon || s == StatusLost ||
		s == StatusAccepted || s == StatusExpired
}

// ── DisputeReason ─────────────────────────────────────────────────────────────

// DisputeReason es el motivo declarado por el pagador al abrir la disputa.
type DisputeReason string

const (
	ReasonFraudulent           DisputeReason = "fraudulent"            // el pagador dice que no autorizó el pago
	ReasonProductNotReceived   DisputeReason = "product_not_received"  // no recibió lo comprado
	ReasonProductUnacceptable  DisputeReason = "product_unacceptable"  // no era lo prometido
	ReasonDuplicate            DisputeReason = "duplicate"             // cobrado dos veces
	ReasonCreditNotProcessed   DisputeReason = "credit_not_processed"  // pidió reembolso y no llegó
	ReasonGeneral              DisputeReason = "general"               // otro motivo
)

func ParseDisputeReason(s string) (DisputeReason, error) {
	r := DisputeReason(s)
	switch r {
	case ReasonFraudulent, ReasonProductNotReceived, ReasonProductUnacceptable,
		ReasonDuplicate, ReasonCreditNotProcessed, ReasonGeneral:
		return r, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidDisputeReason, s)
}

func (r DisputeReason) String() string { return string(r) }

// ── ResolutionOutcome ─────────────────────────────────────────────────────────

type ResolutionOutcome string

const (
	OutcomeWon      ResolutionOutcome = "won"
	OutcomeLost     ResolutionOutcome = "lost"
	OutcomeAccepted ResolutionOutcome = "accepted"
	OutcomeExpired  ResolutionOutcome = "expired"
)

func ParseResolutionOutcome(s string) (ResolutionOutcome, error) {
	o := ResolutionOutcome(s)
	switch o {
	case OutcomeWon, OutcomeLost, OutcomeAccepted, OutcomeExpired:
		return o, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidResolutionOutcome, s)
}

func (o ResolutionOutcome) String() string { return string(o) }

// ── Evidence ──────────────────────────────────────────────────────────────────

// Evidence es un documento o información enviada al banco para contestar la disputa.
// La referencia es un identificador externo (URL de S3, ID de documento).
type Evidence struct {
	id          EvidenceID
	evidenceType string  // "receipt", "tracking", "communication", "other"
	reference   string  // URL o ID externo del documento
	description string
	submittedAt time.Time
}

func NewEvidence(id EvidenceID, evidenceType, reference, description string) Evidence {
	return Evidence{
		id:           id,
		evidenceType: evidenceType,
		reference:    reference,
		description:  description,
		submittedAt:  time.Now().UTC(),
	}
}

func ReconstituteEvidence(id EvidenceID, evidenceType, reference, description string, submittedAt time.Time) Evidence {
	return Evidence{id: id, evidenceType: evidenceType, reference: reference,
		description: description, submittedAt: submittedAt}
}

func (e Evidence) ID() EvidenceID         { return e.id }
func (e Evidence) EvidenceType() string   { return e.evidenceType }
func (e Evidence) Reference() string      { return e.reference }
func (e Evidence) Description() string    { return e.description }
func (e Evidence) SubmittedAt() time.Time { return e.submittedAt }
