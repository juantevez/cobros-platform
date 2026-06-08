package domain

import (
	"fmt"
	"time"
)

// UserStatus representa el estado de la cuenta de un usuario.
type UserStatus string

const (
	// UserStatusActive: el usuario puede autenticarse y operar.
	UserStatusActive UserStatus = "active"

	// UserStatusSuspended: el usuario fue bloqueado por el administrador del tenant.
	UserStatusSuspended UserStatus = "suspended"
)

// User es el agregado que representa a una persona con acceso a la plataforma.
//
// El User existe siempre dentro de un Tenant. El hash de contraseña se almacena
// aquí pero nunca se calcula en el dominio: el caso de uso usa el puerto
// PasswordHasher (en application/ports.go) y pasa el hash resultante.
//
// Invariantes:
//  1. El email es único dentro del tenant (garantizado por la base de datos).
//  2. El passwordHash nunca es vacío en un user persistido.
//  3. Un usuario suspendido no puede autenticarse.
type User struct {
	id           UserID
	tenantID     TenantID
	email        Email
	passwordHash string // almacenado; nunca en claro
	status       UserStatus
	createdAt    time.Time
	updatedAt    time.Time

	events []Event
}

// NewUser crea un User activo.
//
// passwordHash debe ser el hash ya calculado por el PasswordHasher del caso de uso.
// Emite UserRegisteredEvent.
func NewUser(id UserID, tenantID TenantID, email Email, passwordHash string) (*User, error) {
	if passwordHash == "" {
		return nil, ErrEmptyPassword
	}

	now := time.Now().UTC()
	u := &User{
		id:           id,
		tenantID:     tenantID,
		email:        email,
		passwordHash: passwordHash,
		status:       UserStatusActive,
		createdAt:    now,
		updatedAt:    now,
	}

	u.record(UserRegisteredEvent{
		baseEvent: newBase(tenantID.String()),
		TenantID:  tenantID.String(),
		UserID:    id.String(),
		Email:     email.String(),
	})

	return u, nil
}

// ReconstituteUser reconstruye un User desde el repositorio sin emitir eventos.
func ReconstituteUser(
	id UserID,
	tenantID TenantID,
	email Email,
	passwordHash string,
	status UserStatus,
	createdAt, updatedAt time.Time,
) *User {
	return &User{
		id:           id,
		tenantID:     tenantID,
		email:        email,
		passwordHash: passwordHash,
		status:       status,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
	}
}

// CanAuthenticate verifica si el usuario está en condiciones de autenticarse.
// La comparación del password es responsabilidad del caso de uso (puerto PasswordHasher).
func (u *User) CanAuthenticate() error {
	if u.status == UserStatusSuspended {
		return ErrUserSuspended
	}
	return nil
}

// UpdatePasswordHash reemplaza el hash de contraseña.
// Recibe el nuevo hash ya calculado por el PasswordHasher.
func (u *User) UpdatePasswordHash(newHash string) error {
	if newHash == "" {
		return ErrEmptyPassword
	}
	u.passwordHash = newHash
	u.updatedAt = time.Now().UTC()
	return nil
}

// Suspend bloquea al usuario. Solo puede hacerlo el admin del tenant.
func (u *User) Suspend() error {
	if u.status == UserStatusSuspended {
		return fmt.Errorf("user is already suspended")
	}
	u.status = UserStatusSuspended
	u.updatedAt = time.Now().UTC()

	u.record(UserSuspendedEvent{
		baseEvent: newBase(u.tenantID.String()),
		TenantID:  u.tenantID.String(),
		UserID:    u.id.String(),
	})

	return nil
}

// ── Getters ───────────────────────────────────────────────────────────────────

func (u *User) ID() UserID           { return u.id }
func (u *User) TenantID() TenantID   { return u.tenantID }
func (u *User) Email() Email         { return u.email }
func (u *User) PasswordHash() string { return u.passwordHash }
func (u *User) Status() UserStatus   { return u.status }
func (u *User) CreatedAt() time.Time { return u.createdAt }
func (u *User) UpdatedAt() time.Time { return u.updatedAt }

func (u *User) PullEvents() []Event {
	evs := u.events
	u.events = nil
	return evs
}

func (u *User) record(e Event) {
	u.events = append(u.events, e)
}
