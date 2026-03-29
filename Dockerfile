# Multi-stage build — one Dockerfile for all services.
# Build argument SERVICE selects which cmd/ subdirectory to compile.
#
# Example:
#   docker build --build-arg SERVICE=config-service -t eq-config-service .

ARG SERVICE=config-service

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

ARG SERVICE

WORKDIR /app

# Download dependencies first (layer-cached unless go.mod changes)
COPY go.mod go.sum ./
RUN go mod download

# Copy the full source tree
COPY . .

# Compile the selected service binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/service ./cmd/${SERVICE}

# ── Stage 2: minimal runtime ──────────────────────────────────────────────────
FROM alpine:3.19

# ca-certificates required for TLS calls to APNs/FCM in production
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /bin/service /service

ENTRYPOINT ["/service"]
