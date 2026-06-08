# Dockerfile — Multi-stage build para cobros-platform
#
# Etapas:
#   builder → compila ambos binarios
#   api     → imagen mínima solo con el binario api
#   worker  → imagen mínima solo con el binario worker

# ── Builder ───────────────────────────────────────────────────────────────────

FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cachear dependencias antes de copiar el código fuente.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compilar ambos binarios estáticos (sin CGO para imagen mínima).
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api    ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/worker ./cmd/worker

# ── API ───────────────────────────────────────────────────────────────────────

FROM scratch AS api

# Certificados TLS necesarios para conexiones HTTPS salientes.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/api /api

EXPOSE 8080
ENTRYPOINT ["/api"]

# ── Worker ────────────────────────────────────────────────────────────────────

FROM scratch AS worker

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /bin/worker /worker

ENTRYPOINT ["/worker"]
