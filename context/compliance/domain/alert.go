package domain

import "time"

// Alert es el agregado raíz de Compliance & AML.
//
// Representa una señal de riesgo detectada por el sistema: un match contra la
// watchlist durante el onboarding, o el disparo de una regla de monitoreo
// transaccional. Nace en estado Open y un analista de cumplimiento de la
// plataforma la dispone como Cleared (falso positivo) o Confirmed (verdadero
// positivo).
//
// Four-eyes: las alertas de riesgo alto o de screening (RequiresSecondApproval)
// no se cierran con una sola intervención. La primera disposición queda como
// propuesta (pending_second_approval) y un segundo analista, distinto del
// primero, la aprueba (estado final) o la rechaza (vuelve a open).
//
// FSM:
//
//	open → cleared | confirmed                       (riesgo low/medium)
//	open → pending_second_approval                   (propuesta, four-eyes)
//	pending_second_approval → cleared | confirmed    (aprobación, otro analista)
//	pending_second_approval → open                   (propuesta rechazada)
type Alert struct {
	id         AlertID
	tenantID   TenantID
	alertType  AlertType
	riskLevel  RiskLevel
	status     AlertStatus
	subject    string            // legal_name, payment_id o bucket de ventana
	score      int               // 0..100
	details    map[string]string // contexto adicional (regla, monto, lista, etc.)
	note       string            // nota del analista que dispone (o propone)
	resolvedBy string            // analista que cerró la alerta (estado final)
	createdAt  time.Time
	resolvedAt *time.Time

	// Four-eyes: propuesta del primer analista y nota del segundo.
	proposedStatus AlertStatus
	proposedBy     string
	proposedAt     *time.Time
	approvalNote   string

	events []Event
}

// NewAlert crea una alerta en estado Open y registra AlertRaisedEvent.
// Si es un match exacto contra una lista de sanciones registra además
// SanctionMatchedEvent, que dispara el hold preventivo del comercio.
func NewAlert(
	id AlertID,
	tenantID TenantID,
	alertType AlertType,
	riskLevel RiskLevel,
	subject string,
	score int,
	details map[string]string,
	now time.Time,
) *Alert {
	if details == nil {
		details = map[string]string{}
	}
	a := &Alert{
		id:        id,
		tenantID:  tenantID,
		alertType: alertType,
		riskLevel: riskLevel,
		status:    StatusOpen,
		subject:   subject,
		score:     score,
		details:   details,
		createdAt: now,
	}
	a.record(AlertRaisedEvent{
		baseEvent: newBase(tenantID.String()),
		AlertID:   id.String(),
		TenantID:  tenantID.String(),
		AlertType: alertType.String(),
		RiskLevel: riskLevel.String(),
		Subject:   subject,
		Score:     score,
	})
	if a.isSanctionsListMatch() && score >= ScoreExactMatch {
		a.record(SanctionMatchedEvent{
			baseEvent:   newBase(tenantID.String()),
			AlertID:     id.String(),
			TenantID:    tenantID.String(),
			MatchedName: details["matched_name"],
			ListType:    details["list_type"],
			Source:      details["source"],
			Score:       score,
		})
	}
	return a
}

// RequiresSecondApproval indica si la alerta exige four-eyes: riesgo alto o
// screening contra watchlist.
func (a *Alert) RequiresSecondApproval() bool {
	return a.riskLevel == RiskHigh || a.alertType == AlertSanctionsMatch
}

// Resolve es la disposición del primer analista (cleared|confirmed), solo
// desde Open. Si la alerta exige four-eyes queda como propuesta en
// pending_second_approval; si no, se cierra.
func (a *Alert) Resolve(status AlertStatus, note, analyst string, now time.Time) error {
	if status != StatusCleared && status != StatusConfirmed {
		return ErrInvalidDisposition
	}
	switch a.status {
	case StatusOpen:
	case StatusPendingSecondApproval:
		return ErrAlertPendingApproval
	default:
		return ErrAlertNotOpen
	}
	a.note = note
	if !a.RequiresSecondApproval() {
		a.close(status, analyst, now)
		return nil
	}
	a.status = StatusPendingSecondApproval
	a.proposedStatus = status
	a.proposedBy = analyst
	a.proposedAt = &now
	a.record(AlertReviewProposedEvent{
		baseEvent:      newBase(a.tenantID.String()),
		AlertID:        a.id.String(),
		TenantID:       a.tenantID.String(),
		ProposedStatus: status.String(),
		ProposedBy:     analyst,
	})
	return nil
}

// ApproveProposal es la segunda intervención four-eyes: un analista distinto
// del que propuso confirma la disposición propuesta y cierra la alerta.
func (a *Alert) ApproveProposal(note, analyst string, now time.Time) error {
	if err := a.checkSecondAnalyst(analyst); err != nil {
		return err
	}
	a.approvalNote = note
	a.close(a.proposedStatus, analyst, now)
	return nil
}

// RejectProposal es la segunda intervención four-eyes cuando el segundo
// analista no coincide: descarta la propuesta y la alerta vuelve a Open.
func (a *Alert) RejectProposal(note, analyst string, now time.Time) error {
	if err := a.checkSecondAnalyst(analyst); err != nil {
		return err
	}
	a.record(AlertProposalRejectedEvent{
		baseEvent:      newBase(a.tenantID.String()),
		AlertID:        a.id.String(),
		TenantID:       a.tenantID.String(),
		ProposedStatus: a.proposedStatus.String(),
		ProposedBy:     a.proposedBy,
		RejectedBy:     analyst,
		Note:           note,
	})
	a.status = StatusOpen
	a.note = ""
	a.proposedStatus = ""
	a.proposedBy = ""
	a.proposedAt = nil
	return nil
}

func (a *Alert) checkSecondAnalyst(analyst string) error {
	if a.status != StatusPendingSecondApproval {
		return ErrAlertNotPendingApproval
	}
	if analyst == "" || analyst == a.proposedBy {
		return ErrSameAnalyst
	}
	return nil
}

// close lleva la alerta a su estado final y registra los eventos de cierre.
// En alertas de lista de sanciones emite además el evento que levanta el hold
// (cleared) o lo convierte en suspensión (confirmed).
func (a *Alert) close(status AlertStatus, analyst string, now time.Time) {
	a.status = status
	a.resolvedBy = analyst
	a.resolvedAt = &now
	a.record(AlertResolvedEvent{
		baseEvent:  newBase(a.tenantID.String()),
		AlertID:    a.id.String(),
		TenantID:   a.tenantID.String(),
		Status:     status.String(),
		ResolvedBy: analyst,
		ProposedBy: a.proposedBy,
	})
	if !a.isSanctionsListMatch() {
		return
	}
	base := newBase(a.tenantID.String())
	if status == StatusCleared {
		a.record(SanctionClearedEvent{baseEvent: base, AlertID: a.id.String(), TenantID: a.tenantID.String()})
	} else {
		a.record(SanctionConfirmedEvent{baseEvent: base, AlertID: a.id.String(), TenantID: a.tenantID.String()})
	}
}

// isSanctionsListMatch: screening contra una lista de sanciones (no PEP).
// Solo estas alertas tienen consecuencias de enforcement (hold/suspensión).
func (a *Alert) isSanctionsListMatch() bool {
	return a.alertType == AlertSanctionsMatch && a.details["list_type"] == ListTypeSanctions
}

// AlertSnapshot es el estado persistido de una alerta, para reconstituirla.
type AlertSnapshot struct {
	ID             AlertID
	TenantID       TenantID
	Type           AlertType
	RiskLevel      RiskLevel
	Status         AlertStatus
	Subject        string
	Score          int
	Details        map[string]string
	Note           string
	ResolvedBy     string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
	ProposedStatus AlertStatus
	ProposedBy     string
	ProposedAt     *time.Time
	ApprovalNote   string
}

// ReconstituteAlert reconstruye una alerta desde la base sin emitir eventos.
func ReconstituteAlert(s AlertSnapshot) *Alert {
	if s.Details == nil {
		s.Details = map[string]string{}
	}
	return &Alert{
		id:             s.ID,
		tenantID:       s.TenantID,
		alertType:      s.Type,
		riskLevel:      s.RiskLevel,
		status:         s.Status,
		subject:        s.Subject,
		score:          s.Score,
		details:        s.Details,
		note:           s.Note,
		resolvedBy:     s.ResolvedBy,
		createdAt:      s.CreatedAt,
		resolvedAt:     s.ResolvedAt,
		proposedStatus: s.ProposedStatus,
		proposedBy:     s.ProposedBy,
		proposedAt:     s.ProposedAt,
		approvalNote:   s.ApprovalNote,
	}
}

func (a *Alert) ID() AlertID                 { return a.id }
func (a *Alert) TenantID() TenantID          { return a.tenantID }
func (a *Alert) Type() AlertType             { return a.alertType }
func (a *Alert) RiskLevel() RiskLevel        { return a.riskLevel }
func (a *Alert) Status() AlertStatus         { return a.status }
func (a *Alert) Subject() string             { return a.subject }
func (a *Alert) Score() int                  { return a.score }
func (a *Alert) Details() map[string]string  { return a.details }
func (a *Alert) Note() string                { return a.note }
func (a *Alert) ResolvedBy() string          { return a.resolvedBy }
func (a *Alert) CreatedAt() time.Time        { return a.createdAt }
func (a *Alert) ResolvedAt() *time.Time      { return a.resolvedAt }
func (a *Alert) ProposedStatus() AlertStatus { return a.proposedStatus }
func (a *Alert) ProposedBy() string          { return a.proposedBy }
func (a *Alert) ProposedAt() *time.Time      { return a.proposedAt }
func (a *Alert) ApprovalNote() string        { return a.approvalNote }

func (a *Alert) PullEvents() []Event {
	evs := a.events
	a.events = nil
	return evs
}

func (a *Alert) record(e Event) { a.events = append(a.events, e) }
