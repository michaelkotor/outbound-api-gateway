# TEST_CASES.md — Test Specification

Three layers. Each layer has a clear scope, runner, and pass/fail contract.
Tests are listed as named cases with inputs, actions, and exact assertions.
Claude Code implements these — do not invent additional cases until all
specified cases pass.

---

## Layer 1 — Unit tests

**Runner:** `go test -race ./internal/...`
**Location:** `_test.go` files alongside each package
**External deps:** `miniredis/v2` for Redis adapter only. No network, no Docker.
**Requirement:** All cases pass with `-race` flag. Any data race = test failure.

---

### Package: `store` (run same cases against both adapters)

Create `internal/store/store_test.go`. Use a constructor helper:

```go
func newTestStore(t *testing.T, adapter string) store.Store {
    switch adapter {
    case "memory":
        return memory.New()
    case "redis":
        mr := miniredis.RunT(t)
        return redis.New(mr.Addr())
    }
}
```

Run each case in a `t.Run` loop over `[]string{"memory", "redis"}`.

---

#### `store/incr_usage`

**case: single_key_single_window**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
usage := store.GetUsage(ctx, "key-a", "api")
assert: usage.Windows[0].Count == 3
assert: usage.Windows[0].Window == 1h
assert: usage.Windows[0].ResetsAt is within now+1h ± 2s
```

**case: multiple_windows_incremented_together**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h, 24h})
store.IncrUsage(ctx, "key-a", "api", []Duration{1h, 24h})
usage := store.GetUsage(ctx, "key-a", "api")
assert: len(usage.Windows) == 2
assert: window[1h].Count == 2
assert: window[24h].Count == 2
```

**case: separate_keys_independent**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-b", "api", []Duration{1h})
store.IncrUsage(ctx, "key-b", "api", []Duration{1h})
usageA := store.GetUsage(ctx, "key-a", "api")
usageB := store.GetUsage(ctx, "key-b", "api")
assert: usageA.Windows[0].Count == 1
assert: usageB.Windows[0].Count == 2
```

**case: separate_routes_independent**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-a", "anthropic", []Duration{1h})
usag"api    := store.GetUsage(ctx, "key-a", "api")
usageAnthropic := store.GetUsage(ctx, "key-a", "anthropic")
assert: usag"api.Windows[0].Count == 1
assert: usageAnthropic.Windows[0].Count == 1
```

**case: concurrent_increments_no_loss**
```
// 50 goroutines, each calling IncrUsage 20 times = 1000 total
var wg sync.WaitGroup
for i := 0; i < 50; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        for j := 0; j < 20; j++ {
            store.IncrUsage(ctx, "key-a", "api
          ", []Duration{1h})
        }
    }()
}
wg.Wait()
usage := store.GetUsage(ctx, "key-a", "api")
assert: usage.Windows[0].Count == 1000
// -race flag must not report any data race
```

**case: get_usage_unknown_key**
```
_, err := store.GetUsage(ctx, "nonexistent", "api")
assert: errors.Is(err, store.ErrKeyNotFound)
```

---

#### `store/check_limits`

**case: under_limit_returns_ok**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
ok, exceeded, err := store.CheckLimits(ctx, "key-a", []Limit{{Window:1h, MaxRequests:10}})
assert: err == nil
assert: ok == true
assert: exceeded == nil
```

**case: at_limit_returns_exceeded**
```
for i := 0; i < 10; i++ {
    store.IncrUsage(ctx, "key-a", "api
  ", []Duration{1h})
}
ok, exceeded, err := store.CheckLimits(ctx, "key-a", []Limit{{Window:1h, MaxRequests:10}})
assert: err == nil
assert: ok == false
assert: exceeded.Window == 1h
assert: exceeded.MaxRequests == 10
```

**case: zero_limit_means_unlimited**
```
for i := 0; i < 1000; i++ {
    store.IncrUsage(ctx, "key-a", "api
  ", []Duration{1h})
}
ok, _, err := store.CheckLimits(ctx, "key-a", []Limit{{Window:1h, MaxRequests:0}})
assert: err == nil
assert: ok == true
```

**case: first_exceeded_window_returned**
```
// Two windows configured. Hourly limit hit, daily not.
for i := 0; i < 5; i++ {
    store.IncrUsage(ctx, "key-a", "api
  ", []Duration{1h, 24h})
}
limits := []Limit{{Window:1h, MaxRequests:5}, {Window:24h, MaxRequests:100}}
ok, exceeded, _ := store.CheckLimits(ctx, "key-a", limits)
assert: ok == false
assert: exceeded.Window == 1h
```

**case: key_with_no_usage_is_under_limit**
```
ok, exceeded, err := store.CheckLimits(ctx, "key-never-used", []Limit{{Window:1h, MaxRequests:10}})
assert: err == nil
assert: ok == true
assert: exceeded == nil
```

---

#### `store/cooldown`

**case: mark_then_check**
```
until := time.Now().Add(30 * time.Second)
store.MarkCooldown(ctx, "key-a", until)
cooled, err := store.IsCooledDown(ctx, "key-a")
assert: err == nil
assert: cooled == true
```

**case: expired_cooldown_returns_false**
```
// Use miniredis FastForward for Redis; use a short duration + real sleep for memory
until := time.Now().Add(100 * time.Millisecond)
store.MarkCooldown(ctx, "key-a", until)
time.Sleep(200 * time.Millisecond)
// For Redis adapter: mr.FastForward(200 * time.Millisecond) instead of sleep
cooled, _ := store.IsCooledDown(ctx, "key-a")
assert: cooled == false
```

**case: uncooled_key_returns_false**
```
cooled, err := store.IsCooledDown(ctx, "key-never-cooled")
assert: err == nil
assert: cooled == false
```

**case: overwrite_cooldown_with_later_time**
```
store.MarkCooldown(ctx, "key-a", time.Now().Add(10*time.Second))
store.MarkCooldown(ctx, "key-a", time.Now().Add(60*time.Second))
cooled, _ := store.IsCooledDown(ctx, "key-a")
assert: cooled == true
// 10s later (simulated) — still cooled because of the 60s mark
```

---

#### `store/list_usage`

**case: returns_all_keys**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-b", "api", []Duration{1h})
store.IncrUsage(ctx, "key-c", "api", []Duration{1h})
all, _ := store.ListUsage(ctx, "")
assert: len(all) == 3
assert: names in all contain "key-a", "key-b", "key-c"
```

**case: filters_by_route**
```
store.IncrUsage(ctx, "key-a", "api", []Duration{1h})
store.IncrUsage(ctx, "key-a", "anthropic", []Duration{1h})
results, _ := store.ListUsage(ctx, "anthropic")
assert: len(results) == 1
assert: results[0].Route == "anthropic"
```

---

### Package: `selector/roundrobin`

**case: cycles_through_all_keys**
```
keys := []Key{{Name:"a"}, {Name:"b"}, {Name:"c"}}
sel := roundrobin.New(mockStore, keys, 60*time.Second)
// mockStore.CheckLimits always returns ok=true, IsCooledDown always false
results := collect(10 calls to sel.Next(ctx, "api"))
assert: key "a" appears 4 times (positions 0,3,6,9)
assert: key "b" appears 3 times (positions 1,4,7)
assert: key "c" appears 3 times (positions 2,5,8)
```

**case: skips_cooled_key**
```
keys := []Key{{Name:"a"}, {Name:"b"}, {Name:"c"}}
// mockStore returns IsCooledDown=true for "b" only
sel := roundrobin.New(mockStore, keys, 60*time.Second)
results := collect(6 calls to sel.Next(ctx, "api"))
assert: "b" never appears in results
assert: "a" and "c" each appear 3 times
```

**case: skips_over_limit_key**
```
// mockStore returns CheckLimits ok=false for "a"
results := collect(6 calls to sel.Next(ctx, "api"))
assert: "a" never appears
```

**case: returns_pool_exhausted_when_all_blocked**
```
// mockStore: all keys either cooled or over limit
_, err := sel.Next(ctx, "api")
assert: errors.Is(err, selector.ErrPoolExhausted)
```

**case: concurrent_next_no_race**
```
// 20 goroutines each calling Next 50 times simultaneously
// -race flag must not report any data race
// all returned keys must be valid key names (no zero values)
```

**case: feedback_429_triggers_cooldown**
```
key := keys[0]
sel.Feedback(key, 429, nil)
// assert mockStore.MarkCooldown was called with key.Name and a future time
assert: mockStore.cooldownCalls["a"] is set and until > time.Now()
```

**case: feedback_401_triggers_cooldown**
```
sel.Feedback(key, 401, nil)
assert: mockStore.cooldownCalls["a"] is set
```

**case: feedback_200_does_not_cooldown**
```
sel.Feedback(key, 200, nil)
assert: mockStore.cooldownCalls is empty
```

---

### Package: `selector/leastused`

**case: picks_key_with_lowest_window_count**
```
// mockStore.GetUsage returns:
//   "a" → window[1h].Count = 50
//   "b" → window[1h].Count = 10   ← lowest
//   "c" → window[1h].Count = 30
key, _ := sel.Next(ctx, "api")
assert: key.Name == "b"
```

**case: ties_broken_by_stable_order**
```
// all keys have Count == 0
// call Next 3 times
// assert each key returned exactly once (stable tiebreak, not random)
```

**case: falls_back_to_available_key_when_least_used_is_cooled**
```
// "b" has lowest count but IsCooledDown=true
// "c" has second lowest
key, _ := sel.Next(ctx, "api")
assert: key.Name == "c"
```

**case: concurrent_next_no_race**
```
// same as roundrobin concurrent case — different impl, same contract
```

---

### Package: `keys`

**case: resolve_reads_env_var**
```
t.Setenv("TEST_KEY_VAR", "sk-abc123")
key, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "TEST_KEY_VAR"})
assert: err == nil
assert: key.Name == "test"
assert: key.Value == "sk-abc123"
assert: key.EnvVar == "TEST_KEY_VAR"
assert: key.Fingerprint == "***123"
```

**case: resolve_missing_env_var_returns_error**
```
_, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "DEFINITELY_NOT_SET"})
assert: err != nil
assert: err message contains "DEFINITELY_NOT_SET"
```

**case: resolve_empty_env_var_returns_error**
```
t.Setenv("EMPTY_KEY", "")
_, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "EMPTY_KEY"})
assert: err != nil
```

**case: fingerprint_last_four**
```
fp := keys.Fingerprint("sk-abcdefghij1234")
assert: fp == "***1234"
```

**case: fingerprint_short_value_panics**
```
assert: panics when len(value) < 4
```

---

### Package: `config`

**case: load_valid_yaml**
```
write valid config.example.yaml to a temp file
cfg, err := config.Load(tempPath)
assert: err == nil
assert: cfg.Server.Addr == ":8080"
assert: len(cfg.Routes) == 1
assert: cfg.Routes[0].Name == "api"
assert: cfg.Routes[0].Keys[0].Env == "api_KEY_PROD_1"
```

**case: env_expansion_in_string_fields**
```
t.Setenv("MY_UPSTREAM", "https://api.example.com")
write yaml with upstream: "${MY_UPSTREAM}"
cfg, _ := config.Load(tempPath)
assert: cfg.Routes[0].Upstream == "https://api.example.com"
```

**case: missing_file_returns_error**
```
_, err := config.Load("/nonexistent/path.yaml")
assert: err != nil
```

**case: malformed_yaml_returns_error**
```
write ":: invalid: yaml: [" to temp file
_, err := config.Load(tempPath)
assert: err != nil
```

**case: invalid_upstream_url_returns_error**
```
write yaml with upstream: "not a url"
_, err := config.Load(tempPath)
assert: err != nil
assert: err message contains route name
```

---

## Layer 2 — Integration tests

**Runner:** `docker compose -f docker-compose.yml -f docker-compose.test.yml run --rm harness`
**Location:** `test/harness/main.go` + `test/harness/scenarios/`
**Pass/fail:** harness exits 0 (all pass) or 1 (any failure). Failure output
includes scenario name, assertion description, expected value, actual value.

Each scenario calls `POST /admin/reset` on the mock API before running.
Scenarios run sequentially. Parallelism is only in the race layer (layer 3).

Gateway config for integration tests (`test/config.test.yaml`):

```yaml
server:
  addr: ":8080"
  read_timeout: 30s
storage:
  adapter: memory
routes:
  - name:"api

    prefix: "api
  
    upstream: http://mockapi:9090
    selector: round_robin
    headers:
      inject:
        Authorization: "Bearer {key}"
    keys:
      - name: prod-primary
        env: TEST_KEY_A
        limits:
          - window: 1m
            max_requests: 100
      - name: prod-secondary
        env: TEST_KEY_B
        limits:
          - window: 1m
            max_requests: 100
      - name: dev-key
        env: TEST_KEY_C
        limits:
          - window: 1m
            max_requests: 100
```

---

### Scenario: `healthz`

```
GET http://gateway:8080/healthz
assert: status == 200
assert: body.status == "ok"
```

---

### Scenario: `transparent_proxy`

Validates basic phase-1 proxy behavior before key injection.

```
GET http://gateway:8080"api/v1/status
  (no Authorization header — gateway forwards as-is in phase 1)
assert: status == 401   (mock returns 401 for missing token — request reached mock)
assert: response came from mock (not a gateway-level error)
```

In phase 2, this scenario is updated to assert status == 200 after key injection
is implemented.

---

### Scenario: `key_injection`

*(Phase 2 — skip in phase 1, mark as pending)*

```
GET http://gateway:8080"api/v1/status
  (no client Authorization header)
assert: status == 200
assert: body.key_name is one of ["prod-primary", "prod-secondary", "dev-key"]
```

---

### Scenario: `round_robin_distribution`

*(Phase 2)*

```
POST /admin/reset on mockapi
send 30 requests to GET http://gateway:8080"api/v1/status sequentially
collect body.key_name from each 200 response
assert: "prod-primary" appears 10 ± 1 times
assert: "prod-secondary" appears 10 ± 1 times
assert: "dev-key" appears 10 ± 1 times
GET http://gateway:8080/usage
assert: each key's window[1m].used == 10 ± 1
```

---

### Scenario: `limit_exhaustion_triggers_swap`

*(Phase 2)*

```
POST /admin/reset on mockapi
POST /admin/set-limit {"key_name":"prod-primary", "limit":5, "window":"1m"}

send 10 requests to gateway sequentially
collect key_name from each response

assert: among first 5 responses, "prod-primary" appears (may not be all 5 due to RR)
assert: in the response set after prod-primary is exhausted, "prod-primary" does NOT appear
assert: all 10 responses are HTTP 200 (gateway rotated away from exhausted key)

GET /admin/state on mockapi
assert: prod-primary window_count == 5 (exactly at limit, not over)
assert: prod-secondary + dev-key total == 5
```

---

### Scenario: `cooldown_applied_on_429`

*(Phase 2)*

```
POST /admin/reset
POST /admin/set-limit {"key_name":"prod-primary", "limit":2}

send 3 requests sequentially
assert: first 2 responses: 200 with key_name varying (RR)
  — if prod-primary was selected once in the first 2, it may hit 429 on 3rd
note: exact 429 trigger depends on RR position; adjust limit to guarantee hit

alternative setup:
  force RR to start at prod-primary by resetting + sending exactly 1 request
  set prod-primary limit to 1 (so second selection triggers 429)
  send 1 more request — prod-primary selected, mock returns 429
  gateway receives 429 → selector.Feedback(key, 429, nil) → store.MarkCooldown
  send 5 more requests
  assert: "prod-primary" does not appear in any of the 5 responses
GET http://gateway:8080/usage
assert: prod-primary.cooled_until is non-null and in the future
```

---

### Scenario: `cooldown_recovery`

*(Phase 2)*

```
Configure gateway with cooldown_ttl: 2s (add to test config)
Exhaust prod-primary (trigger 429 from mock)
assert: prod-primary absent from next 5 requests

sleep 3 seconds

send 10 more requests
assert: prod-primary appears at least once (recovered from cooldown)
GET /usage
assert: prod-primary.cooled_until is null
```

---

### Scenario: `pool_exhausted_returns_503`

*(Phase 2)*

```
POST /admin/reset
POST /admin/set-limit for all three keys: limit=1

send 1 request per key (3 total) — exhausts all keys
send 1 more request
assert: gateway returns HTTP 503
assert: response body contains "pool_exhausted" or equivalent error code
```

---

### Scenario: `usage_api_accuracy`

*(Phase 2)*

```
POST /admin/reset
send exactly 7 requests to gateway for"api route

GET http://gateway:8080/usage
assert: status == 200
assert: sum of all keys' window[1m].used == 7
assert: each KeyPayload has non-zero last_used
assert: no KeyPayload contains raw token values (assert fingerprint format "***X")

GET http://gateway:8080/usage?route"api
assert: all returned keys have route == "api"

GET http://gateway:8080/usage/prod-primary
assert: returns only prod-primary's record
assert: body.windows is non-empty
```

---

### Scenario: `unknown_route_returns_404`

```
GET http://gateway:8080/nonexistent/path
assert: status == 404
```

---

### Scenario: `upstream_timeout_propagated`

*(Phase 2)*

```
POST /admin/set-latency {"latency_ms": 5000}
configure gateway read_timeout: 1s for this scenario

GET http://gateway:8080"api/v1/status
assert: gateway returns 502 or 504 (not a hang, not a 200)
assert: response received within 2 seconds (gateway did not wait full 5s)

POST /admin/set-latency {"latency_ms": 0}  (restore)
```

---

## Layer 3 — Race / load tests

**Runner:** runs inside the harness binary after all layer 2 scenarios pass.
Invoked as a distinct phase: harness exits only after both layers complete.
**Goal:** detect behavioral races (counter drift) and data races (`-race` on
the harness binary itself catches any harness-side issues).

---

### Race case: `concurrent_requests_no_counter_drift`

```
POST /admin/reset on mockapi

launch 50 goroutines
each goroutine sends 20 requests to GET http://gateway:8080"api/v1/status
all goroutines run simultaneously (use sync.WaitGroup, no staggering)
collect all responses

local_total_200s = count of HTTP 200 responses received by harness

GET http://gateway:8080/usage
gateway_total = sum of window[1m].used across all keys

GET /admin/state on mockapi
mock_total = sum of window_count across all keys

assert: local_total_200s == gateway_total
  rationale: every request the gateway claims it processed must have been received
assert: local_total_200s == mock_total
  rationale: every request the mock claims it processed must match the gateway count
assert: gateway_total == 1000  (50 × 20, all keys have limit=100 default, none exhausted)
  rationale: no increments lost under concurrency
```

---

### Race case: `concurrent_requests_no_key_reuse_after_exhaustion`

```
POST /admin/reset
POST /admin/set-limit {"key_name":"prod-primary", "limit":20}
POST /admin/set-limit {"key_name":"prod-secondary", "limit":20}
POST /admin/set-limit {"key_name":"dev-key", "limit":20}
// total budget: 60 requests before all keys exhausted

launch 30 goroutines each sending 3 requests = 90 total attempts

collect responses:
  count_200 = number of HTTP 200 responses
  count_503 = number of HTTP 503 responses (pool exhausted)

assert: count_200 == 60  (exactly the budget)
assert: count_503 == 30  (the overflow)
assert: count_200 + count_503 == 90  (no other status codes)

GET /admin/state
for each key: assert window_count == 20 (not 21 or more — no overrun in mock)

GET /usage
for each key: assert window[1m].used <= 20 + 3
  (gateway documented overrun: pool_size = 3, so max overrun = 3 per key)
  rationale: gateway may overrun by pool_size, mock must not
```

---

### Race case: `concurrent_feedback_no_double_cooldown`

```
POST /admin/reset
POST /admin/set-limit {"key_name":"prod-primary", "limit":1}

launch 10 goroutines simultaneously, each sending 1 request
// first request to prod-primary succeeds, subsequent get 429 from mock
// gateway selector.Feedback called with 429 multiple times concurrently for same key

sleep 100ms for all goroutines to complete

GET /usage
assert: prod-primary.cooled_until is non-null (cooldown was applied)
assert: prod-primary.window[1m].used <= 1 + 3  (documented overrun, not unbounded)
// primary assertion: no panic, no nil pointer, cooldown applied at least once
```

---

### Race case: `window_reset_under_load`

*(Requires mock with short window — use a separate test config)*

```
Configure test gateway route with limits: [{window: 2s, max_requests: 10}]
POST /admin/set-limit all keys: limit=10, window=2s

Phase 1: send 10 requests in 500ms → all succeed, all keys near limit
Phase 2: sleep 2.5 seconds → window resets
Phase 3: send 10 more requests simultaneously (10 goroutines, 1 each)

assert: all 10 phase-3 requests return 200
  rationale: window reset must make keys available again
GET /usage
assert: each key's window[2s].used reflects only phase-3 requests
assert: ResetsAt is updated to reflect new window start
```

---

## Harness implementation notes

### Structure

```
test/harness/
├── main.go              orchestrator: run layer2 then layer3, exit with code
├── client.go            typed HTTP client for gateway and mockapi
├── assert.go            assertion helpers: assertEqual, assertRange, assertContains
├── scenarios/
│   ├── healthz.go
│   ├── transparent_proxy.go
│   ├── key_injection.go       (pending — skipped until phase 2)
│   ├── round_robin.go
│   ├── limit_exhaustion.go
│   ├── cooldown.go
│   ├── pool_exhausted.go
│   ├── usage_api.go
│   └── upstream_timeout.go
└── race/
    ├── counter_drift.go
    ├── no_overrun.go
    ├── double_cooldown.go
    └── window_reset.go
```

### Scenario contract

Each scenario file exports a single function:

```go
func Run(t *testing.T, gw *client.GatewayClient, mock *client.MockClient)
```

`main.go` calls each in sequence. Uses `testing.T` so assertion failures print
clearly and the harness exits 1 on any failure without running subsequent cases
(fail-fast per scenario, not per assertion within a scenario).

### Pending / skip mechanism

Scenarios marked `*(Phase 2)*` in this document must be registered but skipped:

```go
func Run(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
    t.Skip("phase 2 — key injection not yet implemented")
}
```

Skipped scenarios are listed in harness output as SKIP, not FAIL.
The harness exits 0 even with skipped scenarios, as long as no scenario fails.

### Retry / wait helpers

Gateway may take up to 5 seconds to be ready. Harness must poll `/healthz`
with 500ms intervals and a 10s timeout before running any scenario.
If gateway never becomes healthy, exit 1 with message "gateway did not become
healthy within 10s".

Same polling for mockapi `/healthz` with a 5s timeout.
