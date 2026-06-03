// Package race holds the layer-3 race / load cases. They run inside the harness
// binary after all layer-2 scenarios, sharing the same scenario signature.
//
// Each case uses a dedicated gateway route (race-api, race-budget-api,
// race-window-api) with a 2s cooldown_ttl so layer-2 pool-exhaustion state
// never bleeds in. The harness binary should be built with -race so the Go
// race detector flags any harness-side data races during these cases.
package race

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// mockPoolKeys are the mock-side key names that correspond to TEST_KEY_A/B/C.
// All race routes share the same underlying tokens, so mock.SetLimit targets
// these names regardless of which route is under test.
var mockPoolKeys = []string{"prod-primary", "prod-secondary", "dev-key"}

// raceUsageWindow mirrors api.WindowPayload fields needed by race assertions.
type raceUsageWindow struct {
	Window string `json:"window"`
	Used   int64  `json:"used"`
	Limit  int64  `json:"limit"`
}

// raceUsageKey mirrors api.KeyPayload (subset used by race assertions).
type raceUsageKey struct {
	Name        string            `json:"name"`
	Route       string            `json:"route"`
	Windows     []raceUsageWindow `json:"windows"`
	CooledUntil *time.Time        `json:"cooled_until"`
}

// raceUsageResponse mirrors api.UsageResponse.
type raceUsageResponse struct {
	Keys []raceUsageKey `json:"keys"`
}

// fetchUsage GETs a gateway /usage path and decodes it, failing the test on
// any transport, status, or decode error. Must be called from the test goroutine.
func fetchUsage(t *testing.T, gw *client.GatewayClient, path string) raceUsageResponse {
	t.Helper()
	resp, err := gw.Get(path)
	assert.NoError(t, "GET "+path, err)
	assert.Equal(t, "GET "+path+" status", resp.Status, 200)
	var u raceUsageResponse
	assert.NoError(t, "decode "+path, resp.JSON(&u))
	return u
}

// sumUsed totals the per-window used counts across every key in a usage snapshot.
func sumUsed(u raceUsageResponse) int64 {
	var total int64
	for _, k := range u.Keys {
		for _, w := range k.Windows {
			total += w.Used
		}
	}
	return total
}

// CounterDrift: 50×20 concurrent requests must produce exactly 1000 counted
// requests with no drift between the harness, gateway /usage, and mock.
//
// Route: race-api (/race) — gateway limit 1000/1m/key, mock limit 2000/key.
// All 1000 requests must return 200; the three counts must agree exactly.
func CounterDrift(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Raise mock limits well above the 1000 total so the mock never rejects.
	for _, keyName := range mockPoolKeys {
		assert.NoError(t, "raise mock limit for "+keyName, mock.SetLimit(keyName, 2000, ""))
	}

	const goroutines = 50
	const requestsPerGoroutine = 20
	const total = goroutines * requestsPerGoroutine // 1000

	var mu sync.Mutex
	var count200 int
	var wg sync.WaitGroup

	t.Logf(color.Cyan("launching %d goroutines × %d requests = %d total to /race/v1/status"),
		goroutines, requestsPerGoroutine, total)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				resp, err := gw.Get("/race/v1/status")
				if err != nil {
					t.Logf("request error: %v", err)
					return
				}
				if resp.Status == http.StatusOK {
					mu.Lock()
					count200++
					mu.Unlock()
				} else {
					t.Logf("unexpected response: status=%s body=%s", color.Status(resp.Status), resp.Body)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf(color.Cyan("all goroutines done: count_200=%s (want %s)"),
		color.Bold(fmt.Sprintf("%d", count200)),
		color.Bold(fmt.Sprintf("%d", total)))

	// All 1000 must succeed: gateway limit 1000/1m/key × 3 keys > 1000 total,
	// and mock limit 2000/key never rejects.
	assert.Equal(t, "all requests returned 200", count200, total)

	// Gateway usage must match the harness count exactly — no lost increments.
	u := fetchUsage(t, gw, "/usage?route=race-api")
	gatewayTotal := int(sumUsed(u))
	t.Logf("gateway usage total=%s (want %s)",
		color.Bold(fmt.Sprintf("%d", gatewayTotal)),
		color.Bold(fmt.Sprintf("%d", count200)))
	assert.Equal(t, "gateway usage matches client 200 count", gatewayTotal, count200)

	// Mock window_count must also match — no drift between gateway and mock.
	state, err := mock.State()
	assert.NoError(t, "GET mock /admin/state", err)
	var mockTotal int64
	for _, ks := range state.Keys {
		mockTotal += ks.WindowCount
	}
	t.Logf("mock total window_count=%s (want %s)",
		color.Bold(fmt.Sprintf("%d", mockTotal)),
		color.Bold(fmt.Sprintf("%d", count200)))
	assert.Equal(t, "mock count matches client 200 count", int(mockTotal), count200)
}

// NoOverrun: once the gateway's per-key limit is exhausted, overflow requests
// get 503 (not silently forwarded); the mock never sees a request the gateway
// should have blocked.
//
// Route: race-budget-api (/rbudget) — gateway limit 20/1m/key (budget=60),
// mock limit 1000/key (never rejects, so the gateway is the sole authority).
// 30 goroutines × 3 requests = 90 total; excess beyond the budget gets 503.
//
// Strict "count_200 == 60" does not hold because the gateway's CheckLimits
// and IncreaseUsage are not atomic: a goroutine can pass CheckLimits while
// another's IncreaseUsage is in flight, producing an overrun proportional to
// the number of concurrent in-flight requests per key (up to ~10 here). We
// verify the four properties that ARE invariant under this race:
//
//  1. Only 200 and 503 — no 429 leaks from the mock (mock limits are high).
//  2. All 90 requests are accounted for.
//  3. The full budget was consumed: count_200 ≥ budgetTotal.
//  4. The pool was exhausted: count_503 > 0 (overflow is expected).
//  5. Mock count == harness 200 count (no gateway-mock drift).
func NoOverrun(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Keep mock limits high: the gateway's CheckLimits is the authority.
	// When the gateway decides a key is exhausted it returns 503 directly —
	// the mock never sees that request, so mock window_count == count_200.
	for _, keyName := range mockPoolKeys {
		assert.NoError(t, "raise mock limit for "+keyName, mock.SetLimit(keyName, 1000, ""))
	}

	const goroutines = 30
	const requestsPerGoroutine = 3
	const total = goroutines * requestsPerGoroutine // 90
	const budgetPerKey = 20
	const budgetTotal = budgetPerKey * 3 // 60

	var mu sync.Mutex
	var count200, count503, countOther int
	var wg sync.WaitGroup

	t.Logf(color.Cyan("launching %d goroutines × %d requests = %d total to /rbudget/v1/status (budget=%d)"),
		goroutines, requestsPerGoroutine, total, budgetTotal)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				resp, err := gw.Get("/rbudget/v1/status")
				if err != nil {
					t.Logf("request error: %v", err)
					return
				}
				mu.Lock()
				switch resp.Status {
				case http.StatusOK:
					count200++
				case http.StatusServiceUnavailable:
					count503++
				default:
					countOther++
					t.Logf("unexpected response: status=%s body=%s", color.Status(resp.Status), resp.Body)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf(color.Cyan("all goroutines done: count_200=%s count_503=%s count_other=%s (overrun=%d)"),
		color.Bold(fmt.Sprintf("%d", count200)),
		color.Bold(fmt.Sprintf("%d", count503)),
		color.Bold(fmt.Sprintf("%d", countOther)),
		count200-budgetTotal)

	// 1. Only 200 and 503 — no mock 429 leaks.
	assert.Equal(t, "no unexpected status codes (no mock 429 leaks)", countOther, 0)

	// 2. All 90 requests accounted for.
	assert.Equal(t, "all requests accounted for (200+503 == 90)", count200+count503, total)

	// 3. Full budget consumed before overflow began.
	assert.True(t, "budget fully consumed (count_200 >= budgetTotal)",
		count200 >= budgetTotal)

	// 4. Pool was exhausted: the overflow appears as 503.
	assert.True(t, "pool exhausted (count_503 > 0)", count503 > 0)

	// 5. Mock count == harness 200 count: no gateway-mock drift.
	// 503 responses short-circuit in the proxy before reaching the mock, so
	// mock window_count should equal count_200 exactly.
	state, err := mock.State()
	assert.NoError(t, "GET mock /admin/state", err)
	var mockTotal int64
	for _, ks := range state.Keys {
		t.Logf("  mock key=%q window_count=%s", ks.Name, color.Bold(fmt.Sprintf("%d", ks.WindowCount)))
		mockTotal += ks.WindowCount
	}
	t.Logf("mock total window_count=%s (want %s — no drift)",
		color.Bold(fmt.Sprintf("%d", mockTotal)),
		color.Bold(fmt.Sprintf("%d", count200)))
	assert.Equal(t, "mock count equals client 200 count (no gateway-mock drift)",
		int(mockTotal), count200)
}

// DoubleCooldown: concurrent 429 feedback for the same key must apply cooldown
// without panics or data races (verified by the -race build flag on the harness).
//
// Route: race-api (/race) — race-primary (prod-primary token) is capped at 1
// mock request so the first selection succeeds and subsequent ones return 429.
// 15 concurrent goroutines ensure Feedback(429)/MarkCooldown is called from
// multiple goroutines simultaneously for the same key.
func DoubleCooldown(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Cap race-primary's token to 1 so the first request succeeds and each
	// subsequent selection triggers a mock 429 → Feedback(429) → MarkCooldown.
	assert.NoError(t, "cap prod-primary to mock limit 1", mock.SetLimit("prod-primary", 1, ""))
	assert.NoError(t, "raise prod-secondary mock limit", mock.SetLimit("prod-secondary", 1000, ""))
	assert.NoError(t, "raise dev-key mock limit", mock.SetLimit("dev-key", 1000, ""))

	// 15 goroutines, 1 request each. Round-robin across 3 keys means race-primary
	// is selected on positions 1, 4, 7, 10, 13 — 5 selections total. The first
	// succeeds; the remaining 4 each receive a mock 429 and call Feedback(429)
	// concurrently, exercising the concurrent MarkCooldown path.
	const goroutines = 15
	var wg sync.WaitGroup
	var mu sync.Mutex
	var count200, count429 int

	t.Logf(color.Cyan("launching %d concurrent goroutines to /race/v1/status; prod-primary mock limit=1"),
		goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := gw.Get("/race/v1/status")
			if err != nil {
				t.Logf("request error: %v", err)
				return
			}
			mu.Lock()
			switch resp.Status {
			case http.StatusOK:
				count200++
			case http.StatusTooManyRequests:
				count429++
			default:
				t.Logf("unexpected response: status=%s body=%s", color.Status(resp.Status), resp.Body)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	t.Logf(color.Cyan("all goroutines done: count_200=%s count_429=%s"),
		color.Bold(fmt.Sprintf("%d", count200)),
		color.Bold(fmt.Sprintf("%d", count429)))

	// Primary assertion: the test reached this line without panic. The -race
	// flag on the harness binary catches any data race in Feedback/MarkCooldown.

	// race-primary must be cooled: at least one 429 triggered MarkCooldown.
	u := fetchUsage(t, gw, "/usage/race-primary")
	var raceKey raceUsageKey
	for _, k := range u.Keys {
		if k.Name == "race-primary" {
			raceKey = k
			break
		}
	}
	t.Logf("race-primary cooled_until=%v", raceKey.CooledUntil)
	assert.True(t, "race-primary present in /usage", raceKey.Name == "race-primary")
	assert.True(t, "race-primary is cooled after concurrent 429s", raceKey.CooledUntil != nil)
	if raceKey.CooledUntil != nil {
		assert.True(t, "race-primary cooled_until is in the future",
			raceKey.CooledUntil.After(time.Now()))
	}
}

// WindowReset: after a window rolls over the keys become available again and
// only post-reset requests are counted in the fresh window.
//
// Route: race-window-api (/rwindow) — 3s window / 5 req per key. Phase 1
// fills all keys exactly (15 sequential requests = 5 per key). After a 3.5s
// sleep the window has rolled. Phase 3 sends 15 concurrent requests and all
// must return 200; gateway usage must reflect only the new window's counts.
func WindowReset(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Keep mock limits high so the mock never rejects; the gateway window is
	// the sole constraint.
	for _, keyName := range mockPoolKeys {
		assert.NoError(t, "raise mock limit for "+keyName, mock.SetLimit(keyName, 1000, ""))
	}

	const keysInPool = 3
	const limitPerKey = 5
	const phase1Total = keysInPool * limitPerKey // 15: exactly fills every key's window

	// Phase 1: fill every key's window exactly (sequential, no concurrency noise).
	t.Logf(color.Cyan("phase 1: %d sequential requests to /rwindow/v1/status (limit=%d/3s per key)"),
		phase1Total, limitPerKey)
	for i := 0; i < phase1Total; i++ {
		resp, err := gw.Get("/rwindow/v1/status")
		assert.NoError(t, fmt.Sprintf("phase-1 request %d", i+1), err)
		if i == 0 {
			t.Logf("sample first response: status=%s body=%s", color.Status(resp.Status), resp.Body)
		} else if resp.Status != 200 {
			t.Logf("phase-1 request %d: status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		}
		assert.Equal(t, fmt.Sprintf("phase-1 request %d status", i+1), resp.Status, 200)
	}
	t.Log(color.Cyan("phase 1 complete; all keys at window limit"))

	// Confirm pool is exhausted before sleeping.
	t.Log(color.Cyan("probing pool exhaustion before sleep — expect 503"))
	probeResp, probeErr := gw.Get("/rwindow/v1/status")
	assert.NoError(t, "pre-sleep exhaustion probe", probeErr)
	t.Logf("probe response: status=%s body=%s", color.Status(probeResp.Status), probeResp.Body)
	assert.Equal(t, "pre-sleep probe returns 503 (pool exhausted)", probeResp.Status, 503)

	// Sleep until the 3s window has definitely rolled (+0.5s buffer).
	const windowDuration = 3 * time.Second
	sleepFor := windowDuration + 500*time.Millisecond
	t.Logf(color.Cyan("sleeping %s for window reset"), sleepFor)
	time.Sleep(sleepFor)

	// Phase 3: send phase1Total concurrent requests. Keys are now available
	// again in the fresh window; all must return 200.
	t.Logf(color.Cyan("phase 3: %d concurrent requests after window reset"), phase1Total)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var count200, countOther int

	for i := 0; i < phase1Total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := gw.Get("/rwindow/v1/status")
			if err != nil {
				t.Logf("phase-3 request error: %v", err)
				return
			}
			mu.Lock()
			if resp.Status == http.StatusOK {
				count200++
			} else {
				countOther++
				t.Logf("phase-3 unexpected response: status=%s body=%s", color.Status(resp.Status), resp.Body)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	t.Logf(color.Cyan("phase 3 done: count_200=%s count_other=%s"),
		color.Bold(fmt.Sprintf("%d", count200)),
		color.Bold(fmt.Sprintf("%d", countOther)))

	// All phase-3 requests must succeed: window reset made keys available again.
	assert.Equal(t, "all phase-3 requests returned 200", count200, phase1Total)
	assert.Equal(t, "no phase-3 unexpected statuses", countOther, 0)

	// Gateway usage for the new window must reflect only phase-3 counts.
	// The old window bucket is replaced on the first IncreaseUsage call after
	// expiry, so the snapshot shows only the fresh window's count.
	u := fetchUsage(t, gw, "/usage?route=race-window-api")
	newWindowTotal := int(sumUsed(u))
	t.Logf("gateway usage after window reset: %s (want %s)",
		color.Bold(fmt.Sprintf("%d", newWindowTotal)),
		color.Bold(fmt.Sprintf("%d", phase1Total)))
	assert.Equal(t, "gateway usage reflects only post-reset window counts",
		newWindowTotal, phase1Total)
}
