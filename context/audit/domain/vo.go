package domain

import (
	"fmt"
	"regexp"
)

// Action es la operación que se audita, con convención <contexto>.<hecho>.
// Coincide con el EventType de los eventos de dominio cuando se origina en uno.
type Action string

const (
	// Auth
	ActionTenantCreated     Action = "auth.tenant.created"
	ActionTenantActivated   Action = "auth.tenant.activated"
	ActionTenantSuspended   Action = "auth.tenant.suspended"
	ActionTenantReactivated Action = "auth.tenant.reactivated"
	ActionUserRegistered    Action = "auth.user.registered"
	ActionUserSuspended     Action = "auth.user.suspended"
	ActionApiKeyIssued      Action = "auth.apikey.issued"
	ActionApiKeyRevoked     Action = "auth.apikey.revoked"
	ActionRoleAssigned      Action = "auth.role.assigned"

	// Ledger
	ActionAccountCreated Action = "ledger.account.created"
	ActionEntryPosted    Action = "ledger.entry.posted"
	ActionEntryReversed  Action = "ledger.entry.reversed"

	// Sistema (acciones directas, no via evento)
	ActionLogin  Action = "auth.user.login"
	ActionLogout Action = "auth.user.logout"
)

func (a Action) String() string { return string(a) }

// ActionUnmappedEvent registra un evento que el catálogo de Audit no conoce
// (o cuyo payload no se pudo leer): se audita igual, nunca se descarta.
const ActionUnmappedEvent Action = "audit.unmapped_event"

// actionFormat: dos o más segmentos en minúscula separados por punto
// (auth.tenant.created, compliance.alert.review_proposed).
var actionFormat = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// ParseAction valida el formato <contexto>.<hecho>. No exige que la acción
// esté en un catálogo cerrado: el catálogo vive en el mapeo de eventos.
func ParseAction(s string) (Action, error) {
	if !actionFormat.MatchString(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidAction, s)
	}
	return Action(s), nil
}

// ResourceType clasifica el recurso sobre el que actuó la acción.
type ResourceType string

const (
	ResourceTenant            ResourceType = "tenant"
	ResourceUser              ResourceType = "user"
	ResourceApiKey            ResourceType = "api_key"
	ResourceEntry             ResourceType = "journal_entry"
	ResourceAccount           ResourceType = "ledger_account"
	ResourceApplication       ResourceType = "application" // solicitud de onboarding (KYC)
	ResourcePayment           ResourceType = "payment"
	ResourcePayout            ResourceType = "payout"
	ResourceBillingPlan       ResourceType = "billing_plan"
	ResourceTenantPlan        ResourceType = "tenant_plan"
	ResourceDispute           ResourceType = "dispute"
	ResourceWebhookEndpoint   ResourceType = "webhook_endpoint"
	ResourceAMLAlert          ResourceType = "aml_alert"
	ResourceReconciliationRun ResourceType = "reconciliation_run"
	ResourceDiscrepancy       ResourceType = "reconciliation_discrepancy"
	ResourceEvent             ResourceType = "event" // evento sin mapeo (resource_id = event_id)
)

func ParseResourceType(s string) (ResourceType, error) {
	rt := ResourceType(s)
	switch rt {
	case ResourceTenant, ResourceUser, ResourceApiKey, ResourceEntry, ResourceAccount,
		ResourceApplication, ResourcePayment, ResourcePayout, ResourceBillingPlan,
		ResourceTenantPlan, ResourceDispute, ResourceWebhookEndpoint, ResourceAMLAlert,
		ResourceReconciliationRun, ResourceDiscrepancy, ResourceEvent:
		return rt, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidResourceType, s)
}

func (r ResourceType) String() string { return string(r) }
