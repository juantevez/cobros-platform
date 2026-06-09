# context/auth — Auth & Multi-Tenant

Gestiona la identidad, autenticación y autorización de comercios y sus usuarios. Es el primer contexto que se construye porque todos los demás dependen de él para el aislamiento de datos por tenant.

---

## Responsabilidades

- Registro y ciclo de vida de comercios (**Tenant**) en la plataforma
- Registro y autenticación de usuarios con JWT + refresh tokens
- Autorización por roles (RBAC): `admin`, `operator`, `accountant`, `read_only`, `platform_support`
- Emisión y verificación de **API keys** para integraciones server-to-server
- Aislamiento de datos multi-tenant via **Row Level Security** en PostgreSQL

---

## Estructura

```
context/auth/
├── domain/
│   ├── errors.go        # Errores de negocio tipados
│   ├── vo.go            # Value Objects: TenantID, UserID, Email, Role, Scope, Environment
│   ├── tenant.go        # Agregado Tenant (FSM: pending → active → suspended)
│   ├── user.go          # Agregado User
│   ├── membership.go    # Entidad Membership (user-tenant-role) + tabla de permisos
│   ├── apikey.go        # Agregado ApiKey + ParseRawApiKey
│   └── events.go        # Eventos de dominio
├── application/
│   ├── ports.go         # Interfaces: repos, TokenIssuer, PasswordHasher, Clock
│   ├── token.go         # RefreshToken, AccessTokenClaims, TokenPair
│   ├── commands.go      # DTOs de entrada y salida de cada caso de uso
│   ├── crypto.go        # generateSecret (base62)
│   ├── authenticate.go
│   ├── refresh_token.go
│   ├── register_tenant.go
│   ├── activate_tenant.go
│   ├── suspend_tenant.go
│   ├── register_user.go
│   ├── issue_apikey.go
│   ├── revoke_apikey.go
│   └── assign_role.go
└── infrastructure/adapters/
    ├── inbound/
    │   ├── http/
    │   │   ├── router.go          # Registro de rutas en Gin
    │   │   ├── middleware.go      # JWTMiddleware, ApiKeyMiddleware, RequireRole
    │   │   ├── response.go        # Mapeo domain errors → HTTP status
    │   │   ├── auth_handler.go    # /auth/login, /auth/refresh, /auth/logout
    │   │   ├── tenant_handler.go
    │   │   ├── user_handler.go
    │   │   └── apikey_handler.go
    │   └── nats/
    │       └── onboarding_consumer.go  # Activa tenant al aprobarse el KYC
    └── outbound/
        ├── postgres/              # Implementaciones de repositorios
        ├── token/jwt_issuer.go    # TokenIssuer con HMAC-SHA256
        └── crypto/argon2_hasher.go  # PasswordHasher con Argon2id
```

---

## Dominio

### Agregados

#### `Tenant`
Representa un comercio. Es la raíz del aislamiento multi-tenant.

| Estado | Descripción |
|---|---|
| `pending` | Registrado, pendiente de KYC. Solo opera en modo test. |
| `active` | Habilitado. El ambiente (`test`/`production`) determina si mueve dinero real. |
| `suspended` | Bloqueado por el operador. No puede realizar ninguna operación. |

```
pending ──── Activate(env) ──→ active
pending ──── Suspend()    ──→ suspended
active  ──── Suspend()    ──→ suspended
```

#### `User`
Persona con credenciales de acceso. Puede pertenecer a múltiples tenants con roles distintos.

- El hash de contraseña se calcula **fuera del dominio** (puerto `PasswordHasher`)
- `CanAuthenticate()` verifica el estado; la comparación del password es responsabilidad del caso de uso

#### `ApiKey`
Credencial para integraciones server-to-server.

Formato: `<env>_<prefix>_<secret>`  
Ejemplo: `test_Xk3mPQrS_7fGhJ9kL...`

- Solo el `prefix` (visible) y el `keyHash` se almacenan
- La key completa se entrega **una única vez** al crear
- `ParseRawApiKey()` descompone la key recibida del cliente

#### `Membership`
Vínculo entre un `User` y un `Tenant` con un `Role` asignado.

Contiene la tabla de permisos (`HasPermission(Action)`) que es la única fuente de verdad de autorización en el sistema.

### Eventos de dominio

| Evento | Subject NATS | Descripción |
|---|---|---|
| `TenantCreatedEvent` | `auth.tenant.created.v1` | Comercio registrado |
| `TenantActivatedEvent` | `auth.tenant.activated.v1` | Comercio activado post-KYC |
| `TenantSuspendedEvent` | `auth.tenant.suspended.v1` | Comercio suspendido |
| `UserRegisteredEvent` | `auth.user.registered.v1` | Usuario registrado |
| `ApiKeyIssuedEvent` | `auth.apikey.issued.v1` | API key generada |
| `ApiKeyRevokedEvent` | `auth.apikey.revoked.v1` | API key revocada |
| `RoleAssignedEvent` | `auth.role.assigned.v1` | Rol asignado o modificado |

---

## Casos de uso

| Caso de uso | Descripción |
|---|---|
| `RegisterTenant` | Crea un comercio en estado `pending` |
| `ActivateTenant` | Activa el comercio en modo `test` o `production` |
| `SuspendTenant` | Suspende el comercio (requiere motivo) |
| `RegisterUser` | Registra un usuario en un tenant con un rol inicial |
| `Authenticate` | Valida credenciales y emite `AccessToken` + `RefreshToken` |
| `RefreshToken` | Rota el refresh token y emite nuevos tokens |
| `Logout` | Revoca el refresh token activo |
| `IssueApiKey` | Genera una API key; retorna la key completa solo esta vez |
| `RevokeApiKey` | Revoca una API key de forma irreversible |
| `AssignRole` | Upsert del rol de un usuario en un tenant |

---

## API

### Endpoints públicos

```
POST /api/v1/tenants                  Registrar comercio
POST /api/v1/auth/login               Autenticar usuario → AccessToken + RefreshToken
POST /api/v1/auth/refresh             Rotar tokens
POST /api/v1/auth/logout              Revocar refresh token
```

### Endpoints protegidos (requieren JWT)

```
POST   /api/v1/tenants/:id/users               Registrar usuario       [admin]
PUT    /api/v1/tenants/:id/members/:uid/role   Asignar rol             [admin]
POST   /api/v1/tenants/:id/api-keys            Emitir API key          [admin]
DELETE /api/v1/tenants/:id/api-keys/:kid       Revocar API key         [admin]
POST   /api/v1/tenants/:id/activate            Activar tenant          [platform_support]
POST   /api/v1/tenants/:id/suspend             Suspender tenant        [platform_support]
```

### Autenticación de requests

**Bearer JWT** (usuarios humanos vía HTTP):
```
Authorization: Bearer <access_token>
```

**X-Api-Key** (integraciones server-to-server):
```
X-Api-Key: test_Xk3mPQrS_7fGhJ9kL...
```

---

## Flujos clave

### Login y refresh tokens

```
POST /auth/login
  → Authenticate.Execute()
  → Verifica tenant activo → Busca user por email
  → Verifica password con Argon2id (Verify)
  → IssueAccessToken (JWT HS256, 15 min)
  → IssueRefreshToken → hashea con Argon2id → persiste
  → Retorna { access_token, refresh_token, expires_in: 900 }

POST /auth/refresh
  → RefreshToken.Execute()
  → Hashea el token recibido → busca por hash
  → Valida: no revocado, no expirado, tenant y user activos
  → Rota: emite nuevos tokens, revoca el anterior (replaced_by)
  → Si se presenta un token ya revocado → ErrInvalidCredentials (posible robo)
```

### Verificación de API key

```
X-Api-Key: test_Xk3mPQrS_7fGhJ9kL...
  → ApiKeyMiddleware
  → ParseRawApiKey → extrae env, prefix, secret
  → ApiKeyRepository.FindByPrefix(prefix)   ← búsqueda global sin RLS
  → apiKey.IsRevoked()
  → Argon2id.Verify(secret, apiKey.KeyHash())
  → Inyecta tenantID en context.Context
```

---

## Multi-tenancy con Row Level Security

El `TxManager` ejecuta al inicio de cada transacción:

```sql
SELECT set_config('app.current_tenant', $1, true)  -- equivale a SET LOCAL
```

Todas las tablas con datos de comercio tienen RLS habilitado:

```sql
CREATE POLICY users_tenant_isolation ON users
    USING (tenant_id = current_setting('app.current_tenant', true)::uuid);
```

Las tablas `api_keys` y `refresh_tokens` **no tienen RLS** porque necesitan búsquedas globales (por prefix / por hash). El aislamiento se garantiza en código comparando `tenantID` del token con el del recurso.

---

## Seguridad

| Aspecto | Decisión |
|---|---|
| Hash de contraseñas | Argon2id (64 MiB, 3 iteraciones, 2 hilos) |
| JWT signing | HS256 — secreto de ≥ 32 chars, rotable |
| Access token TTL | 15 minutos |
| Refresh token TTL | 7 días, rotación en cada uso |
| API key secret | 32 bytes aleatorios en base62 |
| API key hash | Argon2id del secreto |
| Error de credenciales | Siempre `ErrInvalidCredentials` — nunca revelar si el email existe |

---

## Dependencias del contexto

```
Produce eventos → stream AUTH (NATS JetStream)
Consume eventos → stream ONBOARDING (onboarding.application.approved.v1)
                  → reacciona activando el Tenant en modo production

Depende de → pkg/postgres (pool, TxManager, RLS)
             pkg/outbox   (EventPublisher)
             pkg/eventbus (Consumer)
```

---

## Tests

Las capas `domain/` y `application/` son testeables **sin base de datos ni NATS**:

```go
// Ejemplo: testear RegisterTenant con dobles
repo   := &mockTenantRepo{}
pub    := &mockPublisher{}
uc     := application.NewRegisterTenantUseCase(repo, mockTxManager{}, pub)
result, err := uc.Execute(ctx, application.RegisterTenantCmd{LegalName: "Acme SA"})
```

Los adapters de Postgres se testean con una base de datos real (test containers o instancia local via `make up`).
