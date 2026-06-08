.PHONY: up down migrate migrate-down test lint tidy

# ── Infraestructura local ────────────────────────────────────────────────────

up:
	docker compose -f deploy/docker/docker-compose.yml up -d

down:
	docker compose -f deploy/docker/docker-compose.yml down

# ── Migraciones (golang-migrate) ─────────────────────────────────────────────

DB_URL ?= postgres://cobros:cobros@localhost:5432/cobros?sslmode=disable

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

# ── Build ────────────────────────────────────────────────────────────────────

build-api:
	go build -o bin/api ./cmd/api

build-worker:
	go build -o bin/worker ./cmd/worker

build: build-api build-worker
