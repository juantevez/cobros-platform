.PHONY: up down up-app down-app migrate migrate-down seed-dev provision test lint tidy build

COMPOSE = docker compose -f deploy/docker/docker-compose.yml
# URL vista desde la red de Compose: las migraciones y el seed corren en
# contenedores, sin requerir los binarios migrate/psql en el host.
DB_URL  ?= postgres://cobros:cobros@postgres:5432/cobros?sslmode=disable
MIGRATE  = $(COMPOSE) run --rm migrate -path=/migrations -database="$(DB_URL)"

# ── Infraestructura local (Postgres + NATS) ──────────────────────────────────

up:
	$(COMPOSE) up -d
	@echo "✓ Postgres en localhost:5432, NATS en localhost:4222"

down:
	$(COMPOSE) down

# Con los binarios de la app en Docker
up-app:
	$(COMPOSE) --profile app up -d --build

down-app:
	$(COMPOSE) --profile app down

# ── Migraciones (golang-migrate) ─────────────────────────────────────────────

migrate:
	$(MIGRATE) up

migrate-down:
	$(MIGRATE) down 1

migrate-drop:
	$(MIGRATE) drop -f

# Datos de ejemplo SOLO para desarrollo local (watchlist AML ficticia).
# Corre psql dentro del contenedor de Postgres.
seed-dev:
	$(COMPOSE) exec -T postgres psql -U cobros -d cobros -v ON_ERROR_STOP=1 < deploy/seed/compliance_watchlist.sql

# Alta de usuarios de plataforma (compliance_officer / platform_support), que
# la API no permite otorgar. La contraseña va por entorno, nunca por argumento:
#   PROVISION_PASSWORD=... make provision TENANT=<uuid> EMAIL=ana@x.com [ROLE=platform_support]
ROLE ?= compliance_officer
provision:
	go run ./cmd/provision -tenant "$(TENANT)" -email "$(EMAIL)" -role "$(ROLE)"

# ── Calidad de código ────────────────────────────────────────────────────────

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

test:
	go test -race -count=1 ./...

test-verbose:
	go test -race -count=1 -v ./...

# ── Build local ──────────────────────────────────────────────────────────────

build:
	go build -o bin/api    ./cmd/api
	go build -o bin/worker ./cmd/worker
	go build -o bin/provision ./cmd/provision

# ── Run local (sin Docker para la app) ───────────────────────────────────────

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker
