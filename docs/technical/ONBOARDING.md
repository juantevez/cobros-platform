# Onboarding (KYC/KYB) — Documentación técnico-funcional

> **Bounded Context:** `context/onboarding`
> **Migraciones:** [`000005_onboarding_init`](../../migrations/000005_onboarding_init.up.sql), [`000017_onboarding_version_operator_rls`](../../migrations/000017_onboarding_version_operator_rls.up.sql), cuentas de solicitudes aprobadas verificadas en [`000047_onboarding_verify_approved_bank_accounts`](../../migrations/000047_onboarding_verify_approved_bank_accounts.up.sql)
> **Stream NATS:** `ONBOARDING` (`onboarding.>`)

---

## 1. Propósito

Onboarding gestiona la **validación de identidad del comercio** (KYC/KYB) antes de que pueda operar en producción. Recolecta los datos legales del negocio, la documentación respaldatoria, las personas relevantes (titulares, directores, beneficiarios finales) y la cuenta bancaria de desembolso, y permite que un operador de la plataforma tome una decisión sobre la solicitud.

La aprobación es el **disparador del alta operativa del comercio**: a partir del evento `onboarding.application.approved.v1`, Auth activa el tenant en modo producción y Ledger crea sus cuentas contables.

### Fuera de alcance (hoy)

- Almacenamiento de archivos: el BC solo guarda una **referencia** externa (URL de S3, ID de otro servicio, etc.).
- Verificación automática de documentos, identidad o CBU/CVU contra terceros (AFIP, RENAPER, bancos).
- Screening AML/sanciones: lo hace el BC **Compliance** reaccionando a `onboarding.application.submitted.v1`.

---

## 2. Lenguaje ubicuo

| Término | Significado |
|---|---|
| **Solicitud** (`OnboardingApplication`) | Expediente de KYC/KYB de un comercio. Hay **una por tenant**. |
| **Comercio** | El tenant que completa la solicitud. Actúa con su propio JWT. |
| **Operador** | Usuario de plataforma (`platform_support`) que revisa y decide. |
| **Datos del negocio** (`BusinessInfo`) | Razón social, CUIT/Tax ID, rubro, domicilio fiscal, web y teléfono. |
| **Documento** (`BusinessDocument`) | Comprobante cargado por el comercio (DNI, constancia AFIP, estatuto, etc.). |
| **Persona** (`Person`) | Titular (`owner`), director/apoderado (`director`) o beneficiario final (`ubo`). |
| **Cuenta bancaria** (`BankAccount`) | Destino de los desembolsos (CBU, CVU o IBAN). Una por solicitud. |
| **Enviar a revisión** | El comercio declara la solicitud completa y la pasa a `in_review`. |
| **Pedir más información** | El operador devuelve la solicitud al comercio con notas. |
| **Reabrir** | El operador devuelve una solicitud **rechazada** al comercio (`requires_more_info`). Única salida de `rejected`. |

---

## 3. Actores y flujo funcional

```mermaid
sequenceDiagram
    autonumber
    actor C as Comercio (JWT tenant)
    actor O as Operador (platform_support)
    participant OB as Onboarding API
    participant DB as PostgreSQL + Outbox
    participant N as NATS (ONBOARDING)
    participant X as Auth / Ledger / Compliance / Notification / Webhook

    C->>OB: POST /onboarding (datos del negocio)
    OB->>DB: INSERT solicitud (pending) + outbox submitted.v1
    DB-->>N: relay → onboarding.application.submitted.v1
    N-->>X: Compliance hace screening

    C->>OB: POST /onboarding/documents (1..n)
    C->>OB: POST /onboarding/persons (1..n)
    C->>OB: PUT  /onboarding/bank-account
    C->>OB: POST /onboarding/submit
    OB->>DB: UPDATE → in_review + outbox sent_for_review.v1

    O->>OB: POST /onboarding/applications/:id/review
    alt approve
        OB->>DB: UPDATE → approved + outbox approved.v1
        N-->>X: Auth activa tenant (production) · Ledger crea 5 cuentas · Notification envía email
    else reject
        OB->>DB: UPDATE → rejected + outbox rejected.v1
        opt reopen (más tarde)
            O->>OB: POST /onboarding/applications/:id/review {reopen}
            OB->>DB: UPDATE → requires_more_info + outbox reopened.v1
        end
    else request_more_info
        OB->>DB: UPDATE → requires_more_info + outbox more_info.v1
        Note over C: El comercio completa y vuelve a enviar (submit)
    end
```

---

## 4. Modelo de dominio

### 4.1 Agregado raíz: `OnboardingApplication`

Archivo: [domain/application.go](../../context/onboarding/domain/application.go)

| Campo | Tipo | Notas |
|---|---|---|
| `id` | `ApplicationID` (UUID) | Generado al crear. |
| `tenantID` | `TenantID` (UUID) | Tomado del JWT; nunca del body. |
| `status` | `ApplicationStatus` | Ver máquina de estados. |
| `businessInfo` | `BusinessInfo` (VO) | Inmutable después de la creación (no hay caso de uso para editarlo). |
| `bankAccount` | `*BankAccount` | Opcional hasta el envío a revisión. Se **reemplaza** si se vuelve a setear. |
| `documents` | `[]BusinessDocument` | Append-only. |
| `persons` | `[]Person` | Append-only. |
| `reviewNotes` | `string` | Notas de `approve`, `request_more_info` o `reopen`. |
| `rejectionReason` | `string` | Obligatorio en `reject`. Se conserva al reabrir (historial del último rechazo). |
| `submittedAt` | `*time.Time` | Se actualiza en **cada** envío a revisión (incluye reenvíos). |
| `reviewedAt` | `*time.Time` | Se setea en `approve` y `reject` (no en `request_more_info`). |
| `createdAt` / `updatedAt` | `time.Time` | UTC. |
| `version` | `int64` | Optimistic locking: `Update` solo aplica si la fila sigue en esta versión (§8.2). |

Las entidades internas (`BusinessDocument`, `Person`, `BankAccount`) solo se crean y modifican a través del agregado.

### 4.2 Máquina de estados

```mermaid
stateDiagram-v2
    [*] --> pending: SubmitApplication
    pending --> in_review: SubmitForReview
    requires_more_info --> in_review: SubmitForReview
    in_review --> requires_more_info: RequestMoreInfo
    in_review --> approved: Approve
    in_review --> rejected: Reject
    rejected --> requires_more_info: Reopen (operador)
    approved --> [*]
```

| Estado | ¿Editable por el comercio? | ¿Acción del operador? | Final |
|---|---|---|---|
| `pending` | Sí | No | No |
| `in_review` | No | Sí | No |
| `requires_more_info` | Sí | No | No |
| `approved` | No | No | **Sí** |
| `rejected` | No | Sí (`reopen`) | No |

"Editable" (`ApplicationStatus.IsEditable()`) habilita: agregar documentos, agregar personas, setear cuenta bancaria y enviar a revisión.

`rejected` deja de ser terminal, pero solo el **operador** puede reabrir: el comercio no puede auto-reabrir ni crear otra solicitud (sigue `UNIQUE (tenant_id)`), lo que evita reintentos indiscriminados de solicitudes rechazadas por fraude.

### 4.3 Invariantes

1. **Una solicitud por tenant** — validado en el caso de uso y garantizado por `UNIQUE (tenant_id)` en DB.
2. **Razón social obligatoria** al crear.
3. **Completitud para enviar a revisión** (`validateCompleteness`):
   - `BusinessInfo` completo: `LegalName`, `TaxID`, `BusinessCategory` y domicilio con `Street`, `City` y `Country`.
   - Al menos **1 documento**.
   - Al menos **1 persona**.
   - **Cuenta bancaria** cargada.
4. **Decisiones del operador solo desde `in_review`**, salvo `Reopen`, que solo aplica desde `rejected`.
5. `Reject` exige motivo; `RequestMoreInfo` y `Reopen` exigen notas. `Approve` acepta notas vacías.
6. `Approve` exige cuenta bancaria con moneda: es la moneda del comercio en `approved.v1` (no hay default).
7. `Approve` **verifica la cuenta bancaria** (`verified = true`): el operador la revisa como parte de la solicitud. Payout solo desembolsa a cuentas verificadas. Si después se reabre y se reemplaza la cuenta, la nueva nace sin verificar hasta la próxima aprobación.

### 4.4 Value Objects y catálogos

Archivo: [domain/vo.go](../../context/onboarding/domain/vo.go)

| VO | Valores / reglas |
|---|---|
| `BusinessCategory` | `retail`, `services`, `food_beverage`, `technology`, `healthcare`, `education`, `marketplace`, `other` |
| `DocumentType` | `id_card`, `passport`, `business_registration`, `tax_certificate`, `bank_statement`, `proof_of_address`, `ownership_proof` |
| `PersonRole` | `owner`, `director`, `ubo` |
| `BankAccountType` | `CBU`, `CVU`, `IBAN` (case-insensitive en la entrada, se normaliza a mayúsculas) |
| `TaxID` | Validado según el país del domicilio (`ParseTaxIDForCountry`). Se eliminan `-`, `.`, `/` y espacios. **`AR`:** CUIT/CUIL de 11 dígitos con dígito verificador (módulo 11, pesos `5,4,3,2,7,6,5,4,3,2`). **Otros países:** alfanumérico de 5 a 20 caracteres, en mayúsculas (RFC, RUT, EIN, VAT…). Se persiste normalizado (`30-71234567-1` → `30712345671`). |
| `Currency` | ISO 4217 de 3 letras, normalizado a mayúsculas (`ParseCurrency`); se valida al setear la cuenta bancaria. |
| `ReviewDecision` | `approve`, `reject`, `request_more_info`, `reopen`. |
| `Address` | Completo si tiene `Street`, `City` y `Country` (ISO 3166-1 alpha-2). |
| `DocumentStatus` | `pending`, `verified`, `rejected` (hoy todos nacen `pending`; no hay caso de uso que los cambie). |

> La verificación contra el organismo fiscal (AFIP, SII, RFB) sigue fuera de alcance: solo se valida el formato.

### 4.5 Errores de dominio

Archivo: [domain/errors.go](../../context/onboarding/domain/errors.go)

| Error | HTTP |
|---|---|
| `ErrApplicationNotFound` | 404 |
| `ErrApplicationExists`, `ErrConcurrentModification` | 409 |
| `ErrInvalidTransition`, `ErrIncompleteApplication` | 422 |
| `ErrInvalidTaxID`, `ErrInvalidBusinessCat`, `ErrInvalidDocumentType`, `ErrInvalidPersonRole`, `ErrInvalidAccountType`, `ErrRejectionReasonEmpty`, `ErrReviewNotesEmpty`, `ErrInvalidDecision`, `ErrInvalidID` (UUID inválido), `ErrRequiredField`, `ErrInvalidCurrency` | 400 |
| Cualquier otro (fallas de DB/tx) | 500 (`internal server error`) |

`ErrConcurrentModification` indica que otra escritura modificó la solicitud entre la lectura y el `UPDATE`: el cliente debe releer (`GET`) y reintentar.

---

## 5. Casos de uso

Archivo: [application/use_cases.go](../../context/onboarding/application/use_cases.go)

| Caso de uso | Actor | Precondición | Resultado | Evento |
|---|---|---|---|---|
| `SubmitApplication` | Comercio | No existe solicitud para el tenant | Crea la solicitud en `pending` | `submitted.v1` |
| `UploadDocument` | Comercio | Solicitud editable | Agrega un documento (`pending`) | — |
| `AddPerson` | Comercio | Solicitud editable | Agrega una persona | — |
| `SetBankAccount` | Comercio | Solicitud editable, moneda ISO 4217 válida | Crea o reemplaza la cuenta bancaria | — |
| `SubmitForReview` | Comercio | Solicitud editable **y completa** | `in_review`, setea `submittedAt` | `sent_for_review.v1` |
| `ReviewApplication` | Operador | `in_review` (o `rejected` para `reopen`) | `approved` / `rejected` / `requires_more_info` | `approved.v1` / `rejected.v1` / `more_info.v1` / `reopened.v1` |
| `GetApplication` | Comercio | Existe solicitud | `ApplicationView` (resumen, sin detalle de colecciones) | — |

### Patrón transaccional

Todos los comandos siguen el mismo esquema:

```
1. Parsear/validar input (VOs)            → errores 400
2. repo.FindByTenantID / FindByID         → fuera de la tx, con RLS fijado (§9)
3. Mutar el agregado                      → reglas de dominio (422)
4. txManager.RunInTx {
       repo.Save / repo.Update            → Update con optimistic lock (409)
       publisher.Publish(app.PullEvents()) → outbox_messages (misma tx)
   }
```

El estado y sus eventos se confirman atómicamente (Transactional Outbox). El relay de `cmd/worker` los publica luego en JetStream. Como la lectura ocurre fuera de la tx, el `UPDATE` verifica la `version` leída: si otra escritura (otro operador, o comercio y operador en paralelo) se adelantó, falla con `ErrConcurrentModification` y no se persiste nada ni se publican eventos.

---

## 6. API HTTP

Archivo: [infrastructure/adapters/inbound/http/handlers.go](../../context/onboarding/infrastructure/adapters/inbound/http/handlers.go)

Todas las rutas cuelgan de `/api/v1` y requieren `Authorization: Bearer <JWT>`. El `tenant_id` se obtiene del token (`JWTMiddleware` → `context.Context`), nunca del body.

| Método | Ruta | Actor | Éxito |
|---|---|---|---|
| `POST` | `/onboarding` | Comercio | `201 {"application_id": "..."}` |
| `GET` | `/onboarding` | Comercio | `200 ApplicationView` |
| `POST` | `/onboarding/documents` | Comercio | `201` (sin body) |
| `POST` | `/onboarding/persons` | Comercio | `201` (sin body) |
| `PUT` | `/onboarding/bank-account` | Comercio | `204` |
| `POST` | `/onboarding/submit` | Comercio | `204` |
| `POST` | `/onboarding/applications/:id/review` | Operador (`platform_support`) | `204` (`409` si hubo una escritura concurrente) |

Un body que no pasa el binding de Gin (JSON inválido o campo `required` ausente) responde `400 {"error": "invalid request body"}`.

### 6.1 Crear solicitud — `POST /onboarding`

```json
{
  "legal_name": "Panadería El Sol S.R.L.",
  "tax_id": "30-71234567-1",
  "business_category": "food_beverage",
  "street": "Av. Corrientes 1234",
  "city": "CABA",
  "state": "Buenos Aires",
  "country": "AR",
  "postal_code": "C1043",
  "website": "https://elsol.com.ar",
  "phone_number": "+54 11 5555-5555"
}
```

Requeridos por HTTP: `legal_name`, `tax_id`, `business_category`, `city`, `country`.

> ⚠️ `street` es **opcional en HTTP pero obligatorio para enviar a revisión**. Si se omite, la solicitud se crea pero `POST /onboarding/submit` responde `422 business info is incomplete`, y no existe endpoint para corregir los datos del negocio.

### 6.2 Cargar documento — `POST /onboarding/documents`

```json
{ "document_type": "tax_certificate", "reference": "s3://kyc-docs/tenant-123/afip.pdf" }
```

### 6.3 Agregar persona — `POST /onboarding/persons`

```json
{
  "full_name": "María González",
  "role": "owner",
  "identity_doc_type": "DNI",
  "identity_doc_number": "30123456",
  "nationality": "AR"
}
```

Requeridos: `full_name`, `role`.

### 6.4 Cuenta bancaria — `PUT /onboarding/bank-account`

```json
{
  "account_type": "CBU",
  "account_number": "0170099220000067797370",
  "bank_name": "BBVA",
  "holder_name": "Panadería El Sol S.R.L.",
  "currency": "ARS"
}
```

Requeridos: `account_type`, `account_number`, `holder_name`, `currency` (ISO 4217, 3 letras; si no → `400`). Idempotente: un nuevo `PUT` reemplaza la cuenta anterior. No se valida el formato del número (largo/dígito verificador de CBU).

### 6.5 Enviar a revisión — `POST /onboarding/submit`

Sin body. Valida completitud (§4.3).

### 6.6 Revisión del operador — `POST /onboarding/applications/:id/review`

```json
{ "decision": "request_more_info", "notes": "Falta constancia de domicilio fiscal" }
```

| `decision` | `notes` | Estado resultante |
|---|---|---|
| `approve` | opcional | `approved` |
| `reject` | **requerido** (motivo) | `rejected` |
| `request_more_info` | **requerido** | `requires_more_info` |
| `reopen` | **requerido** (qué corregir) | `requires_more_info` (solo desde `rejected`) |

Requiere rol `platform_support`; si no → `403 insufficient permissions`. El guard se inyecta en `RegisterRoutes(..., reviewGuard)` desde `cmd/api` (`authttp.RequireRole(authdomain.RolePlatformSupport)`), así Onboarding no importa Auth. `RegisterRoutes` entra en pánico si el guard es `nil`. Detrás del guard, `platformOperatorScope` marca el request como acción de operador para RLS (§9).

Una `decision` desconocida o un `:id` que no es UUID responden `400`.

### 6.7 Consulta — `GET /onboarding`

```json
{
  "id": "6f1c…",
  "tenant_id": "a2b4…",
  "status": "requires_more_info",
  "legal_name": "Panadería El Sol S.R.L.",
  "tax_id": "30712345671",
  "business_category": "food_beverage",
  "document_count": 2,
  "person_count": 1,
  "has_bank_account": true,
  "review_notes": "Falta constancia de domicilio fiscal",
  "submitted_at": "2026-10-20T14:03:11Z",
  "created_at": "2026-10-20T13:40:02Z",
  "updated_at": "2026-10-21T09:12:45Z"
}
```

`review_notes`, `rejection_reason`, `submitted_at` y `reviewed_at` se omiten cuando están vacíos. La vista expone **conteos**, no el detalle de documentos, personas ni cuenta bancaria.

---

## 7. Eventos de dominio

Archivo: [domain/events.go](../../context/onboarding/domain/events.go)

Se publican vía Outbox en el stream `ONBOARDING`. El subject es el `EventType()`. El payload JSON contiene solo los campos exportados; los metadatos viajan como headers:

| Header / campo NATS | Origen |
|---|---|
| `Nats-Msg-Id` | `EventID()` (UUID) — deduplicación en JetStream |
| `event-type` | `EventType()` |
| `content-type` | `application/json` |
| `occurred-at` | `OccurredAt()` (RFC3339Nano, UTC) — agregado por el relay |

| Subject | Cuándo | Payload |
|---|---|---|
| `onboarding.application.submitted.v1` | Se crea la solicitud | `application_id`, `tenant_id`, `legal_name` |
| `onboarding.application.sent_for_review.v1` | El comercio envía a revisión (cada vez) | `application_id`, `tenant_id` |
| `onboarding.application.approved.v1` | El operador aprueba | `application_id`, `tenant_id`, `business_category`, `currency` |
| `onboarding.application.rejected.v1` | El operador rechaza | `application_id`, `tenant_id`, `rejection_reason` |
| `onboarding.application.more_info.v1` | El operador pide más información | `application_id`, `tenant_id`, `notes` |
| `onboarding.application.reopened.v1` | El operador reabre una solicitud rechazada | `application_id`, `tenant_id`, `notes` |

`currency` en `approved.v1` es la moneda de la cuenta bancaria (obligatoria para aprobar; no hay default).

> ⚠️ El nombre `submitted` corresponde a la **creación** de la solicitud, no al envío a revisión (que es `sent_for_review`). Tenerlo en cuenta al suscribirse.

### 7.1 Consumidores

Todos se ejecutan en `cmd/worker`.

| Consumidor | Durable | Filtro | Acción | `MaxDeliver` |
|---|---|---|---|---|
| **Auth** | `auth-onboarding-consumer` | `onboarding.application.approved.v1` | `ActivateTenant` con `Environment: production` | 5 |
| **Ledger** | `ledger-onboarding-consumer` | `onboarding.application.approved.v1` | Crea 7 cuentas en `currency`: `merchant_balance`, `reserve`, `dispute_hold`, `payout_transit`, `payout_sent`, `in_transit`, `platform_fees` | 5 |
| **Compliance** | `compliance-onboarding-consumer` | `onboarding.application.submitted.>` | `ScreenApplication` (screening por razón social) | 5 |
| **Notification** | `notification-onboarding-consumer` | `onboarding.application.>` | Envía email solo para `approved.v1` y `rejected.v1` (con `rejection_reason`); el resto (incluido `reopened.v1`) se ignora | 3 |
| **Webhook** | `webhook-onboarding-consumer` | `onboarding.>` | Crea deliveries a los endpoints suscriptos del comercio | — |

> Ledger crea las 5 cuentas en un loop y devuelve error si falla alguna; en el reintento intentará crear de nuevo las ya creadas. La idempotencia depende de `CreateAccount` en Ledger.

### 7.2 Lectura directa desde Payout

[`payout/.../outbound/onboarding/bank_account_provider.go`](../../context/payout/infrastructure/adapters/outbound/onboarding/bank_account_provider.go) consulta **directamente** las tablas `onboarding_bank_accounts` + `onboarding_applications` (solo solicitudes `approved`, cuentas `verified` y en la moneda del payout) para obtener la cuenta de desembolso. Es la única integración síncrona con este BC y acopla Payout al esquema de Onboarding: **cualquier cambio en esas tablas debe coordinarse con Payout**. No filtra por `verified`.

---

## 8. Persistencia

Archivo: [infrastructure/adapters/outbound/postgres/application_repo.go](../../context/onboarding/infrastructure/adapters/outbound/postgres/application_repo.go)

### 8.1 Tablas

```mermaid
erDiagram
    onboarding_applications ||--o{ onboarding_documents : tiene
    onboarding_applications ||--o{ onboarding_persons : tiene
    onboarding_applications ||--o| onboarding_bank_accounts : tiene

    onboarding_applications {
        uuid id PK
        uuid tenant_id UK
        text status
        text legal_name
        text tax_id
        text business_category
        text addr_street_city_state_country_postal
        text review_notes
        text rejection_reason
        timestamptz submitted_at
        timestamptz reviewed_at
        bigint version
    }
    onboarding_documents {
        uuid id PK
        uuid application_id FK
        uuid tenant_id
        text document_type
        text reference
        text status
    }
    onboarding_persons {
        uuid id PK
        uuid application_id FK
        uuid tenant_id
        text full_name
        text role
    }
    onboarding_bank_accounts {
        uuid id PK
        uuid application_id FK,UK
        uuid tenant_id
        text account_type
        text account_number
        char3 currency
        bool verified
    }
```

Restricciones relevantes:

- `onboarding_applications.status` con `CHECK` sobre los 5 estados.
- `UNIQUE (tenant_id)` en solicitudes → una por tenant, para siempre.
- `UNIQUE (application_id)` en cuentas bancarias → una por solicitud.
- `CHECK` en `onboarding_persons.role`, `onboarding_documents.status` y `onboarding_bank_accounts.account_type`.
- Las 4 tablas tienen **RLS** con dos políticas permisivas (se combinan con OR):
  - **Tenant:** `tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::uuid`. El `NULLIF` ([000017](../../migrations/000017_onboarding_version_operator_rls.up.sql)) evita el error `invalid input syntax for type uuid: ""` en conexiones del pool donde una tx previa dejó la variable vacía.
  - **Operador:** `current_setting('app.platform_operator', true) = 'on'`.
- `version BIGINT NOT NULL DEFAULT 0` (000017) para optimistic locking.

### 8.2 Estrategia de escritura

| Operación | SQL |
|---|---|
| `Save` | `INSERT` solo de la cabecera (`onboarding_applications`). Las colecciones están vacías al crear. |
| `Update` | `UPDATE … SET …, version=version+1 WHERE id=$1 AND version=$n` (0 filas → `ErrConcurrentModification`) + `INSERT … ON CONFLICT (id) DO NOTHING` por cada documento y persona + upsert de cuenta bancaria `ON CONFLICT (application_id) DO UPDATE`. |
| `FindByID` / `FindByTenantID` | 1 query de cabecera + 1 de documentos + 1 de personas + 1 de cuenta bancaria, dentro de `RunWithAppTenant` (tx efímera con las variables RLS si no hay una activa). |

Consecuencias:

- Documentos y personas son **append-only** en DB: no se pueden editar ni borrar.
- `Update` reescribe todas las colecciones en cada llamada (N inserts idempotentes). Aceptable con volúmenes de KYC.
- Al reemplazar la cuenta bancaria, el upsert **conserva el `id` original** de la fila (el `id` nuevo del dominio se descarta) y escribe el `verified` del dominio: una cuenta nueva vuelve a `false`, y la aprobación la pasa a `true`.

---

## 9. Seguridad y multi-tenancy

- El comercio solo opera sobre **su** solicitud: todos los endpoints del comercio resuelven la solicitud por el `tenant_id` del JWT.
- Las escrituras corren dentro de `RunInTx`, que setea `app.current_tenant` (y las variables de `WithAppTenant` / `WithPlatformOperator`) para RLS.
- Las lecturas fuera de tx también fijan las variables: `FindByTenantID` usa el tenant buscado y `FindByID` el del contexto, vía `pkg/postgres.RunWithAppTenant`.
- **Operador:** la solicitud revisada pertenece a **otro** tenant. Las rutas de operador pasan por `platformOperatorScope` (después del guard de rol), que fija `app.platform_operator='on'`; la política de operador expone la fila sin depender del tenant del JWT. Sin ese flag, un `FindByID` de otro tenant devuelve `404`.
- El aislamiento **no depende del rol de DB**: los tests de integración ([rls_test.go](../../context/onboarding/infrastructure/adapters/outbound/postgres/rls_test.go)) corren con un rol sin `BYPASSRLS` (`onboarding_rls_probe`, creado por el test) y verifican lectura propia fuera de tx, aislamiento entre tenants y revisión cross-tenant del operador. Hoy la app conecta como `cobros` (superuser), que saltea RLS; pasar a un rol sin bypass no requiere cambios en este BC.

---

## 10. Limitaciones conocidas y deuda técnica

| # | Tema | Detalle | Impacto |
|---|---|---|---|
| 1 | `BusinessInfo` no editable | No hay caso de uso para corregir razón social, CUIT, rubro o domicilio. Combinado con `street` opcional en HTTP (§6.1) puede dejar una solicitud imposible de enviar. | Funcional |
| 2 | Sin verificación de documentos | `DocumentStatus` existe en el modelo y en DB pero ningún caso de uso lo modifica. (La cuenta bancaria se verifica al aprobar la solicitud.) | Funcional |
| 3 | Sin vista de operador | No hay endpoint para listar solicitudes `in_review` ni para ver el detalle (documentos, personas, cuenta). El operador necesita el `application_id` por otro medio (p. ej. evento `sent_for_review.v1`). | Funcional |
| 4 | Acoplamiento Payout ↔ esquema | Payout lee las tablas de Onboarding directamente (§7.2). | Arquitectura |

---

## 11. Testing

| Capa | Archivos | Tipo |
|---|---|---|
| Dominio | `domain/*_test.go` | Unitarios: transiciones, completitud, VOs, eventos. |
| Aplicación | `application/use_cases_test.go`, `error_paths_test.go` (+ `mocks_test.go`) | Unitarios con repositorio, TxManager y publisher en memoria. |
| HTTP | `infrastructure/adapters/inbound/http/handlers_test.go` | Handlers Gin con `httptest`; incluye el mapeo de errores. |
| Postgres | `infrastructure/adapters/outbound/postgres/application_repo_test.go`, `rls_test.go` | Integración contra PostgreSQL real (`DATABASE_URL`, por defecto `postgres://cobros:cobros@localhost:5432/cobros`): persistencia, optimistic lock y RLS efectivo con el rol `onboarding_rls_probe` (sin `BYPASSRLS`; el test lo crea si no existe). **Se saltan** si la DB no está disponible. |

```bash
go test ./context/onboarding/...
# Con PostgreSQL migrado levantado (docker compose) corren también los de integración.
```

---

## 12. Mapa de código

```
context/onboarding/
├── domain/
│   ├── application.go      # Agregado OnboardingApplication + máquina de estados
│   ├── entities.go         # BusinessDocument, Person, BankAccount
│   ├── vo.go               # IDs, Status, Category, DocumentType, Role, TaxID (CUIT), Currency, ReviewDecision, Address, BusinessInfo
│   ├── events.go           # 6 eventos de dominio
│   └── errors.go           # Errores sentinela
├── application/
│   ├── commands.go         # Comandos, query y ApplicationView
│   ├── ports.go            # ApplicationRepository, TxManager, EventPublisher
│   └── use_cases.go        # 7 casos de uso
└── infrastructure/adapters/
    ├── inbound/http/handlers.go             # OnboardingHandler, ReviewHandler, RegisterRoutes
    └── outbound/postgres/application_repo.go  # optimistic lock + lecturas con RLS
```

Cableado: [cmd/api/main.go](../../cmd/api/main.go) (sección `Onboarding`). El publisher es el genérico `outbox.NewEventPublisher[onboardingdomain.Event]`; el BC no tiene adaptador de eventos propio.
