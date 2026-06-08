package application

// commands.go define los comandos (entrada) y resultados (salida) de cada caso de uso.
//
// Son structs planos con tipos primitivos o de stdlib. No usan tipos del dominio
// para que los adaptadores de entrada (HTTP handlers, consumers NATS) no necesiten
// importar el paquete domain. La conversión a tipos de dominio ocurre dentro
// de cada caso de uso.

// ── Tenant ────────────────────────────────────────────────────────────────────

type RegisterTenantCmd struct {
	LegalName string
}

type RegisterTenantResult struct {
	TenantID string
}

type ActivateTenantCmd struct {
	TenantID    string
	Environment string // "test" | "production"
}

type SuspendTenantCmd struct {
	TenantID string
	Reason   string
}

// ── User ──────────────────────────────────────────────────────────────────────

type RegisterUserCmd struct {
	TenantID string
	Email    string
	Password string
	Role     string // rol inicial del usuario en el tenant
}

type RegisterUserResult struct {
	UserID string
}

type SuspendUserCmd struct {
	TenantID  string
	UserID    string
	SuspendedBy string
}

// ── Auth ──────────────────────────────────────────────────────────────────────

type AuthenticateCmd struct {
	TenantID string
	Email    string
	Password string
}

type RefreshTokenCmd struct {
	RawRefreshToken string // el token en claro presentado por el cliente
}

type LogoutCmd struct {
	RawRefreshToken string
}

// ── ApiKey ────────────────────────────────────────────────────────────────────

type IssueApiKeyCmd struct {
	TenantID    string
	Name        string   // nombre descriptivo, ej: "Integración WooCommerce"
	Environment string   // "test" | "production"
	Scopes      []string // ej: ["payments:write", "payments:read"]
	IssuedBy    string   // UserID del administrador que crea la key
}

type IssueApiKeyResult struct {
	ApiKeyID string
	// FullKey es la clave completa en formato "<env>_<prefix>_<secret>".
	// Solo se retorna en este momento. No puede recuperarse después.
	FullKey string
	Prefix  string
}

type RevokeApiKeyCmd struct {
	TenantID  string
	ApiKeyID  string
	RevokedBy string // UserID del administrador
}

// ── Membership ────────────────────────────────────────────────────────────────

type AssignRoleCmd struct {
	TenantID   string
	UserID     string
	Role       string
	AssignedBy string // UserID del administrador
}
