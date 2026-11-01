package domain

import "errors"

// Errores de dominio del contexto Auth.
//
// Son errores con semántica de negocio, no errores técnicos.
// La capa de application los captura y los mapea a códigos HTTP o mensajes
// de error apropiados. Nunca deben contener detalles de infraestructura.
//
// Uso con errors.Is:
//
//	if errors.Is(err, domain.ErrTenantNotFound) {
//	    // responder 404
//	}
var (
	// ── Tenant ──────────────────────────────────────────────────────────────

	// ErrTenantNotFound se retorna cuando el comercio no existe en el sistema.
	ErrTenantNotFound = errors.New("tenant not found")

	// ErrEmptyLegalName se retorna cuando se intenta crear un tenant sin nombre legal.
	ErrEmptyLegalName = errors.New("legal name cannot be empty")

	// ErrTenantCannotTransition se retorna cuando una transición de estado
	// no está permitida desde el estado actual del tenant.
	// Se usa con fmt.Errorf("%w: ...", ErrTenantCannotTransition) para agregar contexto.
	ErrTenantCannotTransition = errors.New("tenant cannot perform this state transition")

	// ErrTenantSuspended se retorna cuando se intenta operar sobre un tenant suspendido.
	ErrTenantSuspended = errors.New("tenant is suspended")

	// ErrReactivationNotAllowed: quien pide levantar la suspensión no es quien
	// puede hacerlo (la de cumplimiento la levanta un compliance_officer; la
	// del operador, platform_support).
	ErrReactivationNotAllowed = errors.New("this suspension cannot be lifted by your role")

	// ErrTenantNotInProduction: se pidió una API key de producción para un
	// tenant que sigue en test.
	ErrTenantNotInProduction = errors.New("tenant is not approved for production")

	// ErrSuspensionReasonRequired: toda suspensión lleva motivo.
	ErrSuspensionReasonRequired = errors.New("suspension reason is required")

	// ErrTenantNotActive se retorna cuando la operación requiere un tenant activo.
	ErrTenantNotActive = errors.New("tenant is not active")

	// ── User ─────────────────────────────────────────────────────────────────

	// ErrUserNotFound se retorna cuando el usuario no existe.
	ErrUserNotFound = errors.New("user not found")

	// ErrEmailAlreadyExists se retorna en un intento de registro con email duplicado.
	ErrEmailAlreadyExists = errors.New("email already exists in this tenant")

	// ErrInvalidEmail se retorna cuando el formato del email no es válido.
	ErrInvalidEmail = errors.New("invalid email format")

	// ErrInvalidCredentials se retorna cuando la autenticación falla.
	// Deliberadamente genérico: no indica si el email o la contraseña son incorrectos.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrUserSuspended se retorna cuando se intenta autenticar un usuario suspendido.
	ErrUserSuspended = errors.New("user is suspended")

	// ErrEmptyPassword se retorna cuando se intenta establecer una contraseña vacía.
	ErrEmptyPassword = errors.New("password cannot be empty")

	// ErrWeakPassword se retorna cuando la contraseña nueva no cumple la
	// política de longitud (ValidatePassword).
	ErrWeakPassword = errors.New("password must be between 12 and 128 characters")

	// ── ApiKey ───────────────────────────────────────────────────────────────

	// ErrApiKeyNotFound se retorna cuando la API key no existe o el prefix no coincide.
	ErrApiKeyNotFound = errors.New("api key not found")

	// ErrApiKeyRevoked se retorna cuando se usa una API key que fue revocada.
	ErrApiKeyRevoked = errors.New("api key has been revoked")

	// ErrApiKeyNameRequired y ErrApiKeyScopesRequired: datos obligatorios al
	// emitir una key.
	ErrApiKeyNameRequired   = errors.New("api key name is required")
	ErrApiKeyScopesRequired = errors.New("api key must have at least one scope")

	// ErrInvalidScope: el scope no está en el catálogo (AllScopes).
	ErrInvalidScope = errors.New("invalid scope")

	// ErrApiKeyAlreadyRevoked se retorna cuando se intenta revocar una key ya revocada.
	ErrApiKeyAlreadyRevoked = errors.New("api key is already revoked")

	// ── Membership ───────────────────────────────────────────────────────────

	// ErrMembershipNotFound se retorna cuando el vínculo user-tenant-rol no existe.
	ErrMembershipNotFound = errors.New("membership not found")

	// ErrInvalidRole se retorna cuando el rol especificado no existe en el sistema.
	ErrInvalidRole = errors.New("invalid role")

	// ErrRoleNotAssignable se retorna cuando se intenta otorgar desde un tenant
	// un rol reservado a la plataforma (platform_support).
	ErrRoleNotAssignable = errors.New("role cannot be assigned by a tenant")
	// ErrNotPlatformRole se retorna cuando el aprovisionamiento de plataforma
	// recibe un rol de tenant (esos se dan de alta por la API del tenant).
	ErrNotPlatformRole = errors.New("role is not a platform role")

	// ── Value Objects ────────────────────────────────────────────────────────

	// ErrInvalidID se retorna cuando un UUID tiene formato inválido.
	ErrInvalidID = errors.New("invalid id format")

	// ErrInvalidEnvironment se retorna cuando el ambiente especificado no existe.
	ErrInvalidEnvironment = errors.New("invalid environment: must be 'test' or 'production'")
)

// ComplianceReviewReason es el motivo que se registra al suspender un comercio
// por cumplimiento. Es deliberadamente genérico: auth.tenant.suspended.v1 se
// reenvía al comercio por webhook y notificación, y explicitar el motivo real
// (coincidencia con lista de sanciones) sería tipping-off.
const ComplianceReviewReason = "Cuenta en revisión preventiva por controles de seguridad"
