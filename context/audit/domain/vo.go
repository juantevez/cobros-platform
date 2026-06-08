package domain

import "fmt"

// Action es la operación que se audita, con convención <contexto>.<hecho>.
// Coincide con el EventType de los eventos de dominio cuando se origina en uno.
type Action string

const (
	// Auth
	ActionTenantCreated   Action = "auth.tenant.created"
	ActionTenantActivated Action = "auth.tenant.activated"
	ActionTenantSuspended Action = "auth.tenant.suspended"
	ActionUserRegistered  Action = "auth.user.registered"
	ActionUserSuspended   Action = "auth.user.suspended"
	ActionApiKeyIssued    Action = "auth.apikey.issued"
	ActionApiKeyRevoked   Action = "auth.apikey.revoked"
	ActionRoleAssigned    Action = "auth.role.assigned"

	// Ledger
	ActionAccountCreated  Action = "ledger.account.created"
	ActionEntryPosted     Action = "ledger.entry.posted"
	ActionEntryReversed   Action = "ledger.entry.reversed"

	// Sistema (acciones directas, no via evento)
	ActionLogin           Action = "auth.user.login"
	ActionLogout          Action = "auth.user.logout"
)

func (a Action) String() string { return string(a) }

func ParseAction(s string) (Action, error) {
	if s == "" {
		return "", ErrInvalidAction
	}
	return Action(s), nil
}

// ResourceType clasifica el recurso sobre el que actuó la acción.
type ResourceType string

const (
	ResourceTenant  ResourceType = "tenant"
	ResourceUser    ResourceType = "user"
	ResourceApiKey  ResourceType = "api_key"
	ResourceEntry   ResourceType = "journal_entry"
	ResourceAccount ResourceType = "ledger_account"
)

func ParseResourceType(s string) (ResourceType, error) {
	rt := ResourceType(s)
	switch rt {
	case ResourceTenant, ResourceUser, ResourceApiKey, ResourceEntry, ResourceAccount:
		return rt, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidResourceType, s)
}

func (r ResourceType) String() string { return string(r) }
