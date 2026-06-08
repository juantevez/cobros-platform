package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── IDs ──────────────────────────────────────────────────────────────────────
//
// Cada ID es un tipo distinto sobre string con semántica de UUID v4.
// El tipado fuerte evita pasar un UserID donde se espera un TenantID.

// TenantID identifica unívocamente un comercio.
type TenantID string

// NewTenantID genera un nuevo TenantID aleatorio.
func NewTenantID() TenantID { return TenantID(uuid.NewString()) }

// ParseTenantID valida y convierte un string a TenantID.
func ParseTenantID(s string) (TenantID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: tenant id %q", ErrInvalidID, s)
	}
	return TenantID(s), nil
}

func (id TenantID) String() string { return string(id) }
func (id TenantID) IsZero() bool   { return id == "" }

// UserID identifica unívocamente un usuario.
type UserID string

func NewUserID() UserID { return UserID(uuid.NewString()) }

func ParseUserID(s string) (UserID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: user id %q", ErrInvalidID, s)
	}
	return UserID(s), nil
}

func (id UserID) String() string { return string(id) }
func (id UserID) IsZero() bool   { return id == "" }

// ApiKeyID identifica unívocamente una API key.
type ApiKeyID string

func NewApiKeyID() ApiKeyID { return ApiKeyID(uuid.NewString()) }

func ParseApiKeyID(s string) (ApiKeyID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return "", fmt.Errorf("%w: api key id %q", ErrInvalidID, s)
	}
	return ApiKeyID(s), nil
}

func (id ApiKeyID) String() string { return string(id) }
func (id ApiKeyID) IsZero() bool   { return id == "" }

// ── Email ─────────────────────────────────────────────────────────────────────

// Email es un value object que garantiza un email válido y normalizado.
// Se almacena en minúsculas y sin espacios.
type Email string

// NewEmail valida y normaliza un email.
func NewEmail(raw string) (Email, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return "", ErrInvalidEmail
	}
	domain := s[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return Email(s), nil
}

func (e Email) String() string { return string(e) }

// ── Role ──────────────────────────────────────────────────────────────────────

// Role define los roles de un usuario dentro de un tenant (RBAC).
type Role string

const (
	// RoleAdmin tiene acceso completo al tenant: usuarios, configuración, pagos.
	RoleAdmin Role = "admin"
	// RoleOperator puede generar cobros y consultar transacciones.
	RoleOperator Role = "operator"
	// RoleAccountant tiene acceso de lectura a reportes financieros y desembolsos.
	RoleAccountant Role = "accountant"
	// RoleReadOnly puede consultar pero no modificar nada.
	RoleReadOnly Role = "read_only"
	// RolePlatformSupport es un rol interno del operador de la plataforma.
	RolePlatformSupport Role = "platform_support"
)

// ParseRole convierte un string a Role validado.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	switch r {
	case RoleAdmin, RoleOperator, RoleAccountant, RoleReadOnly, RolePlatformSupport:
		return r, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidRole, s)
}

func (r Role) String() string { return string(r) }

// ── Environment ───────────────────────────────────────────────────────────────

// Environment distingue si el tenant opera en modo prueba o producción.
// Un tenant en modo Test no puede mover dinero real.
type Environment string

const (
	EnvironmentTest       Environment = "test"
	EnvironmentProduction Environment = "production"
)

// ParseEnvironment convierte un string a Environment validado.
func ParseEnvironment(s string) (Environment, error) {
	e := Environment(s)
	switch e {
	case EnvironmentTest, EnvironmentProduction:
		return e, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidEnvironment, s)
}

func (e Environment) String() string  { return string(e) }
func (e Environment) IsTest() bool    { return e == EnvironmentTest }
func (e Environment) IsProd() bool    { return e == EnvironmentProduction }

// ── Scope ─────────────────────────────────────────────────────────────────────

// Scope define qué operaciones puede realizar una API key.
type Scope string

const (
	// ScopePaymentsWrite permite crear cobros.
	ScopePaymentsWrite Scope = "payments:write"
	// ScopePaymentsRead permite consultar pagos.
	ScopePaymentsRead Scope = "payments:read"
	// ScopeWebhooks permite gestionar webhooks.
	ScopeWebhooks Scope = "webhooks:write"
	// ScopeReports permite acceder a reportes.
	ScopeReports Scope = "reports:read"
)

// AllScopes retorna todos los scopes disponibles (para API keys con acceso completo).
func AllScopes() []Scope {
	return []Scope{ScopePaymentsWrite, ScopePaymentsRead, ScopeWebhooks, ScopeReports}
}

// ParseScope valida un scope.
func ParseScope(s string) (Scope, error) {
	sc := Scope(s)
	for _, valid := range AllScopes() {
		if sc == valid {
			return sc, nil
		}
	}
	return "", fmt.Errorf("invalid scope: %q", s)
}

func (s Scope) String() string { return string(s) }
