# Integration test harness image. Built and run by docker-compose.test.yml.
# Uses a non-scratch final base because the harness makes outbound HTTP calls to
# the gateway and mock services on the compose network.
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o harness ./test/harness

FROM alpine:3.20
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/harness /harness

ENTRYPOINT ["/harness"]
