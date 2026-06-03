package scenarios

import (
	"fmt"
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// LimitExhaustion verifies gateway-side limit enforcement: once limited-key
// reaches its configured cap (max_requests: 5/min on the limit-api route),
// the selector skips it and routes all remaining requests to spare-key. Every
// request must return 200 — the pool is not exhausted, just rotated.
//
// Uses the dedicated limit-api route (/lapi) so the api route keys remain
// unaffected for the later cooldown and pool-exhaustion scenarios.
//
// Token mapping (mock authenticates by token, not gateway key name):
//   - limited-key → TEST_KEY_A → mock name "prod-primary"
//   - spare-key   → TEST_KEY_B → mock name "prod-secondary"
func LimitExhaustion(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Raise mock-side limits high so no mock 429s interfere with the
	// gateway-side limit check under test.
	t.Log(color.Cyan("raising mock-side limits to 10000 so mock 429s cannot interfere"))
	assert.NoError(t, "raise prod-primary mock limit", mock.SetLimit("prod-primary", 10000, ""))
	assert.NoError(t, "raise prod-secondary mock limit", mock.SetLimit("prod-secondary", 10000, ""))

	// With 2 keys in round-robin and limited-key capped at 5, the first 10
	// requests split evenly (5 each). At request 11 the gateway's CheckLimits
	// skips limited-key and routes everything to spare-key. Drive 20 requests
	// so spare-key handles the 10 overflow requests.
	const total = 20
	t.Logf(color.Cyan("sending %d requests to /lapi/v1/status; limited-key (prod-primary) has gateway cap max_requests=5"), total)
	for i := 0; i < total; i++ {
		resp, err := gw.Get("/lapi/v1/status")
		assert.NoError(t, "limit drive request", err)
		if i == 0 {
			t.Logf("sample first response: status=%s body=%s", color.Status(resp.Status), resp.Body)
		} else if resp.Status != 200 {
			t.Logf("unexpected: request %d status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		}
		assert.Equal(t, "limit drive status", resp.Status, 200)
	}
	t.Logf(color.Cyan("all %d requests returned 200; checking gateway usage counter for limited-key"), total)

	// Gateway usage counter for limited-key must be at (or above) its limit.
	u := fetchUsage(t, gw, "/usage/limited-key")
	lk, ok := findUsageKey(u, "limited-key")
	assert.True(t, "limited-key present in /usage", ok)
	var limitedUsed int64
	for _, w := range lk.Windows {
		limitedUsed += w.Used
	}
	t.Logf("limited-key gateway usage: %s across %d window(s) (cap is 5)",
		color.Bold(fmt.Sprintf("%d", limitedUsed)), len(lk.Windows))
	assert.True(t, "limited-key usage reached its gateway limit", limitedUsed >= 5)

	// spare-key must have served more requests than limited-key: the gateway
	// routed overflow there after limited-key was exhausted.
	t.Log(color.Cyan("checking mock state: spare-key (prod-secondary) must have served more requests"))
	state, err := mock.State()
	assert.NoError(t, "GET mock /admin/state", err)
	var primaryCount, secondaryCount int64
	for _, ks := range state.Keys {
		switch ks.Name {
		case "prod-primary":
			primaryCount = ks.WindowCount
		case "prod-secondary":
			secondaryCount = ks.WindowCount
		}
	}
	t.Logf("mock window counts: prod-primary=%s  prod-secondary=%s",
		color.Bold(fmt.Sprintf("%d", primaryCount)),
		color.Bold(fmt.Sprintf("%d", secondaryCount)))
	assert.True(t, "spare-key served more requests than limited-key after exhaustion",
		secondaryCount > primaryCount)
}
