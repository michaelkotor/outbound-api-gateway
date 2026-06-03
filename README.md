# Outbound API Gateway

[![CI](https://github.com/michaelkotor/outbound-api-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/michaelkotor/outbound-api-gateway/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.25-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

A lightweight, self-hosted **reverse proxy for outbound API traffic**. Point your
services at the gateway instead of directly at a third-party API and it transparently injects a managed pool of API keys,
spreads load across them, backs off keys that get rate-limited, and tracks
per-key usage — without any client-side changes.

```
                          ┌──────────────────────────────────────┐
                          │            Outbound Gateway           │
   your service           │                                       │       upstream API
  ───────────────►  /api/v1/...  ──►  key selector ──► inject  │  ──►  api.api.com
   (no API key)           │             (round-robin /            │       (real key)
                          │              least-used)              │
   GET /usage  ◄──────────│   usage store (memory | redis)        │
                          └──────────────────────────────────────┘
```

---

## Table of contents

- [Features](#features)
- [How it works](#how-it-works)
- [Quick start (Docker)](#quick-start-docker)
- [Running locally (from source)](#running-locally-from-source)
- [Configuration](#configuration)
- [HTTP API](#http-api)
- [Testing](#testing)
- [Building & deployment](#building--deployment)
- [Project layout](#project-layout)
- [Status & known limitations](#status--known-limitations)
- [Contributing](#contributing)
- [License](#license)

---

## Features

- **Transparent reverse proxy** — forwards path, query, and body to the configured
  upstream; sets `X-Forwarded-For`; strips the route prefix.
- **Managed key pools** — resolve secrets from environment variables at startup;
  inject them into upstream requests via configurable header templates
  (`Authorization: "Bearer {key}"`).
- **Pluggable key selection** — `round_robin` or `least_used`, chosen per route.
- **Limit- and rate-limit-aware selection** — the selector skips keys that are
  over their configured usage limits or cooled down; a `429`/`401` from the
  upstream cools the offending key down. When every key is unavailable the
  gateway returns `503`.
- **Per-key usage tracking** — fixed-window counters exposed through a `/usage`
  API. Responses contain **fingerprints only**, never raw secrets.
- **Pluggable storage** — in-memory (default) or Redis, selected in config.
- **Prometheus metrics** — request, selection, cooldown, and pool-exhaustion
  counters exposed at `/metrics`.
- **Config hot-reload** — `SIGHUP` re-reads the config and swaps routes, keys,
  and selectors with zero downtime; usage counters survive the reload.
- **Operationally boring** — single static binary, `/healthz` endpoint, graceful
  shutdown on `SIGINT`/`SIGTERM`, runs as an ~12 MB `scratch` container.

## How it works

For each incoming request the gateway:

1. Matches the request path against a configured route prefix (e.g. `/api`).
2. Asks the route's **selector** for an eligible key — one that is neither cooled
   down nor over its configured limits.
3. Rewrites the URL to the upstream and **injects** the key into the configured
   headers (`{key}` is replaced with the secret).
4. Forwards the request, then records the outcome: a `429`/`401` triggers a
   cooldown on that key; any other response increments its usage counters.

If no key is available the gateway responds `503 no keys available` instead of
calling the upstream.

---

## Quick start (Docker)

The fastest way to see it running is the bundled Compose stack (gateway + Redis):

```bash
# 1. Provide your upstream secrets
cp .env.example .env
$EDITOR .env                       # fill in API_KEY_PROD_1, API_KEY_PROD_2

# 2. Point config.example.yaml at the upstream you want to proxy
#    (defaults to a placeholder upstream — edit the `routes:` section)

# 3. Bring up the stack
docker compose up --build
```

The gateway listens on **`http://localhost:9080`**:

```bash
# Health check
curl -s http://localhost:9080/healthz
# {"status":"ok"}

# Proxy a call — no API key from the client; the gateway injects one
curl -s http://localhost:9080/api/v1/target

# Inspect per-key usage
curl -s http://localhost:9080/usage | jq
```

Tear down with `docker compose down`.

---

## Running locally (from source)

Requires **Go 1.25+**.

```bash
# Install dependencies
go mod download

# Provide secrets referenced by your config's `keys[].env`
cp .env.example .env
set -a; source .env; set +a          # export the vars into your shell

# Run against the example config
go run ./cmd/gateway --config config.example.yaml
```

Or build a binary:

```bash
go build -o gateway ./cmd/gateway
./gateway --config config.example.yaml
```

The `--config` flag defaults to `config.yaml`.

---

## Configuration

Configuration is a single YAML file. String fields support `${ENV_VAR}`
expansion, so secrets and environment-specific values stay out of the file.

```yaml
server:
  addr: ":9080"          # listen address (override with GATEWAY_ADDR)
  read_timeout: 30s

storage:
  adapter: memory        # "memory" (default) or "redis"
  redis_url: ""          # e.g. "redis://localhost:6379/0" when adapter = redis

routes:
  - name: api                       # stable name, used in logs and /usage
    prefix: /api                    # incoming path prefix to match
    upstream: https://api.api.com   # absolute upstream base URL
    selector: round_robin              # "round_robin" or "least_used"
    headers:
      inject:
        Authorization: "Bearer {key}"  # {key} is replaced with the chosen secret
      strip:                           # optional headers to remove before forwarding
        - X-Internal-Token
    keys:
      - name: prod-primary             # stable key name (shown in /usage)
        env: API_KEY_PROD_1         # env var holding the secret
        limits:
          - window: 24h
            max_requests: 10000
          - window: 1h
            max_requests: 1000
```

### Environment variables

| Variable        | Purpose                                                                  |
| --------------- | ------------------------------------------------------------------------ |
| `GATEWAY_ADDR`  | Overrides `server.addr`. Takes precedence over the config file.          |
| `<keys[].env>`  | Each key's secret, e.g. `API_KEY_PROD_1`. The gateway fails to start if a referenced var is unset or empty. |

> **Secrets never leave the proxy.** Key values are read at startup, used only to
> inject upstream headers, and never logged or serialized. The `/usage` API
> exposes a fingerprint (`***` + last 4 chars) instead.

---

## HTTP API

| Method & path           | Description                                                        |
| ----------------------- | ----------------------------------------------------------------- |
| `GET /healthz`          | Liveness probe. Returns `200 {"status":"ok"}`.                    |
| `GET /metrics`          | Prometheus metrics (requests, selections, cooldowns, exhaustion). |
| `ANY /<prefix>/*`       | Proxied to the route's upstream with a key injected.              |
| `GET /usage`            | Usage snapshot for every key.                                     |
| `GET /usage?route=NAME` | Usage filtered to a single route.                                 |
| `GET /usage/{key_name}` | Usage for a single key.                                           |

Example `/usage` response:

```json
{
  "generated_at": "2026-06-02T12:00:00Z",
  "keys": [
    {
      "name": "prod-primary",
      "fingerprint": "***a1b2",
      "route": "api",
      "windows": [
        { "window": "1h", "used": 42, "limit": 1000, "resets_at": "2026-06-02T13:00:00Z" }
      ],
      "last_used": "2026-06-02T12:00:00Z",
      "cooled_until": null
    }
  ]
}
```

A proxied request returns the upstream's response unchanged, except:

- `503 no keys available` — every key in the pool is cooled down or over limit.
- `502 upstream request failed` — the upstream could not be reached.

---

## Testing

The project has two layers of tests.

### Unit tests

Standard Go tests for config loading, key resolution, storage adapters
(in-memory **and** Redis, via in-process [miniredis](https://github.com/alicebob/miniredis)),
and the selectors.

```bash
# All unit tests, with the race detector
go test -race ./...

# With coverage
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration tests

A dockerized harness brings up the gateway plus a **mock upstream API**
(`test/mockapi`) that authenticates injected keys, enforces its own rate limits,
and exposes an admin surface for assertions. The harness
(`test/harness`) drives end-to-end scenarios — key injection, round-robin
distribution, cooldown on `429`, pool exhaustion → `503`, and `/usage` accuracy —
and exits non-zero if any fails.

```bash
# Run the full integration stack (gateway + mock upstream + Redis), then tear down
docker compose -f docker-compose.yml -f docker-compose.test.yml down -v
docker compose -f docker-compose.yml -f docker-compose.test.yml run --rm --build harness
docker compose -f docker-compose.yml -f docker-compose.test.yml down -v
```

> The leading `down -v` guarantees a fresh gateway: `compose run` leaves
> dependency containers running between invocations, and a reused gateway retains
> key cooldowns from a previous run.

Both layers run in CI on every push and pull request — see
[`.github/workflows/ci.yml`](.github/workflows/ci.yml).

---

## Building & deployment

### Binary

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o gateway ./cmd/gateway
```

### Container image

The [`Dockerfile`](Dockerfile) produces a static, non-root `scratch` image
(~12 MB) with CA certificates for TLS upstreams:

```bash
docker build -t outbound-api-gateway:dev .
docker run --rm -p 9080:9080 \
  -e API_KEY_PROD_1=sk-... \
  -v "$PWD/config.example.yaml:/config.yaml:ro" \
  outbound-api-gateway:dev
```

CI publishes images to **GitHub Container Registry**
(`ghcr.io/michaelkotor/outbound-api-gateway`) on pushes to `main` and on `v*`
tags. The `scratch` base has no shell, so liveness should be probed via
`GET /healthz` at the orchestrator level (Compose/Kubernetes) rather than a
Docker `HEALTHCHECK`.

---

## Project layout

```
cmd/gateway/            entrypoint: config load, route wiring, HTTP server
internal/
  config/               YAML schema + loader (${ENV} expansion)
  keys/                 key resolution and fingerprinting
  proxy/                key-injecting reverse proxy + tracked transport
  selector/             KeySelector interface
    roundrobin/         round-robin selector
    leastused/          least-used selector
  storage/              Storage interface + usage types
    memory/             in-memory adapter
    redis/              Redis adapter
  api/                  /usage HTTP handlers and payloads
  metrics/              Prometheus registry + /metrics handler
test/
  mockapi/              standalone mock upstream API (integration only)
  harness/              end-to-end scenario + race runner
```

---

## Status & known limitations

The proxy, key injection, selectors, limit- and cooldown-aware selection, usage
tracking, the `/usage` API, Prometheus `/metrics`, and `SIGHUP` config
hot-reload are implemented and covered by tests. A few rough edges are tracked
and intentionally left for follow-up:

- **Cooldown TTL is fixed at 60s** and not yet configurable.
- **No upstream/request timeout** is configured on the proxy transport (it uses
  `http.DefaultTransport`).
- **Hot-reload is scoped to routes/keys/selectors.** Changing the storage
  adapter/address or the listen address still requires a full restart.

See the integration harness skip messages for the precise, up-to-date list.

---

## Contributing

1. Fork and create a feature branch.
2. Keep the build green: `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and
   the integration stack above.
3. Open a pull request — CI runs the lint, unit, and integration phases
   automatically.

---

## License

Released under the MIT License. See [LICENSE](LICENSE).
