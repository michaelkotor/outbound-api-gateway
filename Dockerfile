# syntax=docker/dockerfile:1

# ---- build stage ----------------------------------------------------------
# Pinned to match the go directive in go.mod (go 1.25.0).
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Resolve modules first so this layer caches independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static, stripped, reproducible binary. CGO disabled so it runs on scratch.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o gateway ./cmd/gateway

# ---- runtime stage --------------------------------------------------------
FROM scratch

# Build metadata, populated by CI via --build-arg (see metadata-action outputs).
ARG VERSION=dev
ARG GIT_SHA=unknown

LABEL org.opencontainers.image.title="outbound-api-gateway" \
      org.opencontainers.image.description="Standalone outbound API gateway" \
      org.opencontainers.image.source="https://github.com/michaelkotor/outbound-api-gateway" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_SHA}"

# CA certs for TLS calls to upstream APIs (scratch ships none).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/gateway /gateway

# Run unprivileged. scratch has no /etc/passwd, so use a numeric uid:gid
# (65534 = nobody:nogroup on most distros).
USER 65534:65534

EXPOSE 9080

# No HEALTHCHECK: scratch has no shell or curl. Liveness/readiness is handled by
# the orchestrator (Compose / Kubernetes) probing GET /healthz.
ENTRYPOINT ["/gateway"]
CMD ["--config", "/config.yaml"]
