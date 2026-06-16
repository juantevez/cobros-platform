.PHONY: up down up-app down-app migrate migrate-down test lint tidy build

COMPOSE = docker compose -f deploy/docker/docker-compose.yml
DB_URL  ?= postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable

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
	migrate -path ./migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path ./migrations -database "$(DB_URL)" down 1

migrate-drop:
	migrate -path ./migrations -database "$(DB_URL)" drop -f

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

# ── Run local (sin Docker para la app) ───────────────────────────────────────

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker
