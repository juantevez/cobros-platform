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

	// ── ApiKey ───────────────────────────────────────────────────────────────

	// ErrApiKeyNotFound se retorna cuando la API key no existe o el prefix no coincide.
	ErrApiKeyNotFound = errors.New("api key not found")

	// ErrApiKeyRevoked se retorna cuando se usa una API key que fue revocada.
	ErrApiKeyRevoked = errors.New("api key has been revoked")

	// ErrApiKeyAlreadyRevoked se retorna cuando se intenta revocar una key ya revocada.
	ErrApiKeyAlreadyRevoked = errors.New("api key is already revoked")

	// ── Membership ───────────────────────────────────────────────────────────

	// ErrMembershipNotFound se retorna cuando el vínculo user-tenant-rol no existe.
	ErrMembershipNotFound = errors.New("membership not found")

	// ErrInvalidRole se retorna cuando el rol especificado no existe en el sistema.
	ErrInvalidRole = errors.New("invalid role")

	// ── Value Objects ────────────────────────────────────────────────────────

	// ErrInvalidID se retorna cuando un UUID tiene formato inválido.
	ErrInvalidID = errors.New("invalid id format")

	// ErrInvalidEnvironment se retorna cuando el ambiente especificado no existe.
	ErrInvalidEnvironment = errors.New("invalid environment: must be 'test' or 'production'")
)
