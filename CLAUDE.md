# CLAUDE.md

Guidance for working in this repo. The [README](README.md) is the user-facing
overview (features, config reference, deployment); this file captures the
architecture, conventions, and gotchas needed to change the code safely.

## What this is

A self-hosted reverse proxy for **outbound** API traffic. Clients call the
gateway without a key; for each request the gateway matches a route prefix,
picks an upstream API key from that route's pool, injects it into the upstream
request headers, forwards it, then records the outcome (cooldown on `429`/`401`,
usage increment otherwise). Module path: `github.com/michaelkotor/outbound-api-gateway`.
Go **1.25+**.

## Commands

```bash
# Run from source (needs key secrets exported or in .env)
go run ./cmd/gateway --config config.example.yaml   # --config defaults to config.yaml
                                                    # --env-file defaults to .env

# Unit tests (always with the race detector, matching CI)
go test -race ./...
go test -race -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

# Lint / build — mirror CI before pushing
gofmt -l .            # must print nothing
go vet ./...
golangci-lint run     # config in .golangci.yml
go build ./...

# Integration harness (dockerized: gateway + mock upstream + Redis).
# The leading `down -v` is REQUIRED — a reused gateway keeps key cooldowns
# from the previous run and scenarios flake.
docker compose -f docker-compose.yml -f docker-compose.test.yml down -v
docker compose -f docker-compose.yml -f docker-compose.test.yml run --rm --build harness
docker compose -f docker-compose.yml -f docker-compose.test.yml down -v
```

There is no Makefile; run the Go tooling directly. CI runs lint+build, race
unit tests, then the integration harness — see [.github/workflows/ci.yml](.github/workflows/ci.yml).

## Architecture

Request flow: `cmd/gateway/main.go` wires everything → chi router → per-route
`proxy.Handler` → `selector.KeySelector.Next()` picks a key → key injected into
upstream headers → tracked transport forwards and calls `selector.Feedback()`,
which writes to `storage.Storage`.

| Package | Role |
| --- | --- |
| `cmd/gateway` | Entrypoint: config load, `.env` load, route/selector/storage wiring, HTTP server, graceful shutdown, SIGHUP reload. |
| `internal/config` | YAML schema + `Load` (`${ENV}` expansion) + `watcher` (SIGHUP hot-reload). |
| `internal/keys` | Key resolution from env vars + fingerprinting (`***` + last 4). |
| `internal/proxy` | Key-injecting reverse proxy + usage-tracking transport. |
| `internal/selector` | `KeySelector` interface; `roundrobin/` and `leastused/` impls. |
| `internal/storage` | `Storage` interface + usage types; `memory/` and `redis/` adapters. |
| `internal/api` | `/usage` HTTP handlers and JSON payloads. |
| `internal/metrics` | Prometheus registry + `/metrics` handler. |
| `test/mockapi` | Standalone mock upstream (auth, rate limits, admin surface) — integration only. |
| `test/harness` | End-to-end scenario + race runner; exits non-zero on failure. |

Two key interfaces define the extension points — implement these to add
behavior, and keep both **concurrency-safe**:
- `selector.KeySelector` ([internal/selector/interface.go](internal/selector/interface.go))
- `storage.Storage` ([internal/storage/interface.go](internal/storage/interface.go))

### Config reload (SIGHUP)
`main.go` uses a `swappableHandler` (atomic pointer) to swap the router on
SIGHUP. The **storage backend is reused** across reloads so usage counters
survive; route/key/selector changes apply live. Changing the storage
adapter/address or the listen address requires a full restart. A reload that
fails to build is rejected and the previous config is kept.

## Conventions

- **Descriptive identifiers.** This codebase deliberately avoids short names —
  e.g. `responseWriter`, `loadedConfig`, `resolvedKeys`, `usageStorage`. Match it.
- **Secrets never leak.** Key values are read at startup and used only for header
  injection — never log, serialize, or return them. Everything user-facing uses
  the fingerprint. Preserve this invariant in any new code path.
- **Wrap errors with package context**, e.g. `fmt.Errorf("config: ...: %w", err)`.
- **Sentinel errors** live at the top of the defining package
  (`selector.ErrPoolExhausted`, `storage.ErrKeyNotFound`); compare with `errors.Is`.
- Selectors take `cooldownTTL` as a constructor arg; it is currently the
  hardcoded `cooldownTTL = 60s` const in `main.go`.

## Gotchas & current state

- **The README's "Status & known limitations" section is stale.** SIGHUP
  hot-reload, the Prometheus `/metrics` endpoint, and limit-aware selection
  (`storage.CheckLimits`) are now **implemented in code**, contrary to what the
  README says. When touching those areas, trust the code, and consider updating
  the README.
- `config.Load` comments say key env-var validation "is phase 2" — secrets are
  actually resolved (and fail fast) in `main.go`/`internal/keys`, not in `Load`.
- `config/schema.go` lists `"random"` as a selector option in a comment, but
  `newKeySelector` only supports `round_robin` and `least_used` (default
  `round_robin`). Adding `random` means wiring it in `main.go`.
- `LimitConfig.MaxRequests == 0` means **unlimited**, not "block everything".
- Proxy responses differ from upstream only on: `503 no keys available` (pool
  exhausted/over-limit) and `502 upstream request failed` (upstream unreachable).
- Integration scenarios are flaky if you skip the `down -v` (cooldown carryover).
  Check harness skip messages for the up-to-date list of intentionally-skipped
  scenarios.
</content>
