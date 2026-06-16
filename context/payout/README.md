# context/payout — Payouts / Desembolsos

Gestiona la transferencia de fondos desde la plataforma hacia la cuenta bancaria del comercio. Cierra el ciclo del dinero iniciado en Payment Processing: captura → Ledger acredita → Payout transfiere al banco.

---

## Responsabilidades

- Calcular el saldo disponible del comercio en el Ledger
- Ejecutar la transferencia bancaria vía un adaptador de banco (`BankTransferAdapter`)
- Registrar cada movimiento contable en el Ledger via eventos de dominio
- Revertir el asiento automáticamente si la transferencia falla

---

## Estructura

```
context/payout/
├── domain/
│   ├── errors.go       # Errores de negocio
│   ├── vo.go           # PayoutID, TenantID, Money, BankAccountInfo, PayoutStatus
│   ├── payout.go       # Agregado Payout (FSM)
│   └── events.go       # PayoutInitiatedEvent, PayoutConfirmedEvent, PayoutFailedEvent
├── application/
│   ├── ports.go        # PayoutRepository, BalanceChecker, BankAccountProvider,
│   │                   # BankTransferAdapter, EventPublisher, TxManager
│   ├── commands.go     # DTOs de entrada y salida
│   ├── initiate_payout.go   # Caso de uso principal
│   └── query_payouts.go     # GetPayout, ListPayouts
└── infrastructure/adapters/
    ├── inbound/http/
    │   └── payout_handler.go  # POST /payouts, GET /payouts, GET /payouts/:id
    └── outbound/
        ├── postgres/
        │   └── payout_repo.go
        ├── transfer/mock/
        │   └── adapter.go         # Mock que siempre confirma
        ├── ledger/
        │   └── balance_checker.go # Query a account_balances del Ledger
        └── onboarding/
            └── bank_account_provider.go  # Query a onboarding_bank_accounts
```

---

## Dominio

### FSM del Payout

```
initiated → processing → confirmed   (camino feliz)
                       → failed      (banco rechaza)
```

| Estado | Descripción |
|---|---|
| `initiated` | Calculado, asiento en Ledger registrado. Listo para transferir. |
| `processing` | Transferencia enviada al banco, esperando respuesta. |
| `confirmed` | Banco confirmó la acreditación. Estado final exitoso. |
| `failed` | Transferencia rechazada. El Ledger revierte el asiento. |

### Eventos de dominio

| Evento | Subject NATS | Acción en Ledger |
|---|---|---|
| `PayoutInitiatedEvent` | `payout.initiated.v1` | `merchant_balance CREDIT` + `payout_transit DEBIT` |
| `PayoutConfirmedEvent` | `payout.confirmed.v1` | `payout_transit CREDIT` + `payout_sent DEBIT` |
| `PayoutFailedEvent` | `payout.failed.v1` | Reversa: `payout_transit CREDIT` + `merchant_balance DEBIT` |

---

## Flujo contable completo

```
Captura de pago (Payment Processing):
  in_transit        CREDIT  $100
  merchant_balance  DEBIT   $ 97   ← saldo disponible del comercio
  platform_fees     DEBIT   $  3

Desembolso iniciado (Payout):
  merchant_balance  CREDIT  $ 97   ← sale del saldo del comercio
  payout_transit    DEBIT   $ 97   ← en tránsito al banco

Desembolso confirmado:
  payout_transit    CREDIT  $ 97   ← salen del tránsito
  payout_sent       DEBIT   $ 97   ← enviados definitivamente
```

En todo momento: `sum(débitos) == sum(créditos)`. El Ledger siempre balancea.

---

## API

```
POST /api/v1/payouts              Iniciar desembolso      [JWT + admin]
GET  /api/v1/payouts              Listar payouts          [JWT]
GET  /api/v1/payouts/:payoutID    Consultar estado        [JWT]
```

### Iniciar desembolso

```json
POST /api/v1/payouts
{
  "amount": 9700,    // centavos; 0 = usar saldo disponible completo
  "currency": "ARS"
}
```

Respuesta exitosa:
```json
{
  "payout_id": "...",
  "amount": 9700,
  "currency": "ARS",
  "status": "confirmed",
  "bank_reference": "MOCK-BANK-a1b2c3d4"
}
```

---

## Puertos clave

### `BalanceChecker`
Consulta el saldo de `merchant_balance` en la tabla `account_balances` del Ledger.  
En el monolith: query directa a Postgres.  
En microservicios: llamada HTTP al servicio Ledger.

### `BankAccountProvider`
Obtiene la cuenta bancaria registrada en el Onboarding (`onboarding_bank_accounts`).  
Solo retorna cuentas de aplicaciones en estado `approved`.

### `BankTransferAdapter`
Abstrae la ejecución de la transferencia. Implementaciones:

| Adaptador | Uso | Estado |
|---|---|---|
| `mock.Adapter` | Desarrollo y tests | ✅ Implementado |
| Banco real / MP Transfers | Producción | ⏳ Fase 4 |

---

## Consistencia con el Ledger

El Payout **no llama directamente al Ledger**. Publica eventos y el Ledger tiene un consumer (`ledger-payout-consumer`) que los procesa de forma asíncrona.

Esto garantiza que si el Ledger está temporalmente no disponible, el evento se reintenta. La idempotencia del Ledger (`UNIQUE(tenant_id, idempotency_key)`) garantiza que el mismo payout nunca genera dos asientos.

---

## Dependencias del contexto

```
Produce eventos → stream PAYOUT (NATS JetStream)
Consume eventos → ninguno

Lee de        → account_balances (del Ledger, misma BD)
               → onboarding_bank_accounts (del Onboarding, misma BD)

Depende de    → pkg/postgres
               → pkg/outbox (EventPublisher)
               → pkg/eventbus
```
