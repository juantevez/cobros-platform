package domain

import "time"

// Alert es el agregado raíz de Compliance & AML.
//
// Representa una señal de riesgo detectada por el sistema: un match contra la
// watchlist durante el onboarding, o el disparo de una regla de monitoreo
// transaccional. Nace en estado Open y un analista la dispone como Cleared
// (falso positivo) o Confirmed (verdadero positivo).
//
// FSM:
//
//	open → cleared
//	open → confirmed
type Alert struct {
	id         AlertID
	tenantID   TenantID
	alertType  AlertType
	riskLevel  RiskLevel
	status     AlertStatus
	subject    string            // legal_name o payment_id que disparó la alerta
	score      int               // 0..100
	details    map[string]string // contexto adicional (regla, monto, lista, etc.)
	note       string            // nota del analista al disponer
	createdAt  time.Time
	resolvedAt *time.Time

	events []Event
}

// NewAlert crea una alerta en estado Open y registra AlertRaisedEvent.
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
	return a
}

// Resolve dispone la alerta (cleared|confirmed). Solo desde Open.
func (a *Alert) Resolve(status AlertStatus, note string, now time.Time) error {
	if a.status != StatusOpen {
		return ErrAlertNotOpen
	}
	a.status = status
	a.note = note
	a.resolvedAt = &now
	a.record(AlertResolvedEvent{
		baseEvent: newBase(a.tenantID.String()),
		AlertID:   a.id.String(),
		TenantID:  a.tenantID.String(),
		Status:    status.String(),
	})
	return nil
}

// ReconstituteAlert reconstruye una alerta desde la base sin emitir eventos.
func ReconstituteAlert(
	id AlertID,
	tenantID TenantID,
	alertType AlertType,
	riskLevel RiskLevel,
	status AlertStatus,
	subject string,
	score int,
	details map[string]string,
	note string,
	createdAt time.Time,
	resolvedAt *time.Time,
) *Alert {
	if details == nil {
		details = map[string]string{}
	}
	return &Alert{
		id:         id,
		tenantID:   tenantID,
		alertType:  alertType,
		riskLevel:  riskLevel,
		status:     status,
		subject:    subject,
		score:      score,
		details:    details,
		note:       note,
		createdAt:  createdAt,
		resolvedAt: resolvedAt,
	}
}

func (a *Alert) ID() AlertID              { return a.id }
func (a *Alert) TenantID() TenantID       { return a.tenantID }
func (a *Alert) Type() AlertType          { return a.alertType }
func (a *Alert) RiskLevel() RiskLevel     { return a.riskLevel }
func (a *Alert) Status() AlertStatus      { return a.status }
func (a *Alert) Subject() string          { return a.subject }
func (a *Alert) Score() int               { return a.score }
func (a *Alert) Details() map[string]string { return a.details }
func (a *Alert) Note() string             { return a.note }
func (a *Alert) CreatedAt() time.Time     { return a.createdAt }
func (a *Alert) ResolvedAt() *time.Time   { return a.resolvedAt }

func (a *Alert) PullEvents() []Event {
	evs := a.events
	a.events = nil
	return evs
}

func (a *Alert) record(e Event) { a.events = append(a.events, e) }
