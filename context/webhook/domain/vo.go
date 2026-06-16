package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ── IDs ───────────────────────────────────────────────────────────────────────

type EndpointID string
type DeliveryID string
type AttemptID string
type TenantID string

func NewEndpointID() EndpointID { return EndpointID(uuid.NewString()) }
func NewDeliveryID() DeliveryID { return DeliveryID(uuid.NewString()) }
func NewAttemptID() AttemptID   { return AttemptID(uuid.NewString()) }

func ParseEndpointID(s string) (EndpointID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid endpoint id: %w", err)
	}
	return EndpointID(s), nil
}

func ParseDeliveryID(s string) (DeliveryID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid delivery id: %w", err)
	}
	return DeliveryID(s), nil
}

func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("invalid tenant id: %w", err)
	}
	return TenantID(s), nil
}

func (id EndpointID) String() string { return string(id) }
func (id DeliveryID) String() string { return string(id) }
func (id AttemptID) String() string  { return string(id) }
func (id TenantID) String() string   { return string(id) }

// ── DeliveryStatus ────────────────────────────────────────────────────────────

type DeliveryStatus string

const (
	// StatusPending: esperando ser despachado (primer intento o reintento programado).
	StatusPending DeliveryStatus = "pending"
	// StatusDelivered: el endpoint respondió con 2xx. Estado final exitoso.
	StatusDelivered DeliveryStatus = "delivered"
	// StatusFailed: el último intento falló; hay un reintento programado.
	StatusFailed DeliveryStatus = "failed"
	// StatusExhausted: se agotaron todos los reintentos. Estado final fallido.
	StatusExhausted DeliveryStatus = "exhausted"
)

func (s DeliveryStatus) String() string { return string(s) }
func (s DeliveryStatus) IsFinal() bool {
	return s == StatusDelivered || s == StatusExhausted
}
func (s DeliveryStatus) IsRetryable() bool {
	return s == StatusPending || s == StatusFailed
}

// ── RetrySchedule ─────────────────────────────────────────────────────────────

// RetrySchedule define los intervalos entre reintentos (backoff escalonado).
// Intento 1 es inmediato. Los siguientes siguen este schedule.
var RetrySchedule = []time.Duration{
	30 * time.Second, // reintento 2
	2 * time.Minute,  // reintento 3
	10 * time.Minute, // reintento 4
	1 * time.Hour,    // reintento 5
}

// MaxAttempts es el número máximo de intentos antes de marcar como exhausted.
// const MaxAttempts = len(RetrySchedule) + 1 // 5
const MaxAttempts = 5

// NextRetryAt calcula cuándo hacer el siguiente reintento dado el número
// de intentos ya realizados. Retorna nil si se agotaron los reintentos.
func NextRetryAt(attemptCount int, now time.Time) *time.Time {
	idx := attemptCount - 1 // attemptCount=1 → idx=0 → 30s
	if idx < 0 || idx >= len(RetrySchedule) {
		return nil
	}
	t := now.Add(RetrySchedule[idx])
	return &t
}

// ── DeliveryAttempt ───────────────────────────────────────────────────────────

// DeliveryAttempt registra el resultado de un intento de entrega HTTP.
type DeliveryAttempt struct {
	id           AttemptID
	attemptNum   int
	httpStatus   int    // 0 si fue error de red/timeout
	responseBody string // primeros 500 chars de la respuesta
	errMsg       string // mensaje de error si no hubo respuesta HTTP
	durationMs   int64
	attemptedAt  time.Time
}

func NewDeliveryAttempt(num, httpStatus int, responseBody, errMsg string, durationMs int64) DeliveryAttempt {
	body := responseBody
	if len(body) > 500 {
		body = body[:500]
	}
	return DeliveryAttempt{
		id:           NewAttemptID(),
		attemptNum:   num,
		httpStatus:   httpStatus,
		responseBody: body,
		errMsg:       errMsg,
		durationMs:   durationMs,
		attemptedAt:  time.Now().UTC(),
	}
}

func ReconstituteAttempt(id AttemptID, num, httpStatus int, body, errMsg string, durationMs int64, at time.Time) DeliveryAttempt {
	return DeliveryAttempt{id: id, attemptNum: num, httpStatus: httpStatus,
		responseBody: body, errMsg: errMsg, durationMs: durationMs, attemptedAt: at}
}

func (a DeliveryAttempt) ID() AttemptID          { return a.id }
func (a DeliveryAttempt) AttemptNum() int        { return a.attemptNum }
func (a DeliveryAttempt) HTTPStatus() int        { return a.httpStatus }
func (a DeliveryAttempt) ResponseBody() string   { return a.responseBody }
func (a DeliveryAttempt) ErrMsg() string         { return a.errMsg }
func (a DeliveryAttempt) DurationMs() int64      { return a.durationMs }
func (a DeliveryAttempt) AttemptedAt() time.Time { return a.attemptedAt }
func (a DeliveryAttempt) Succeeded() bool        { return a.httpStatus >= 200 && a.httpStatus < 300 }
