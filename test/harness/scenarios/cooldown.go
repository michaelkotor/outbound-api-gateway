package scenarios

import (
	"testing"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// CooldownApplied (phase 2): a 429 from the mock triggers selector.Feedback →
// store.MarkCooldown, after which the cooled key is skipped and /usage reports a
// future cooled_until. We constrain only prod-primary, so it is the key that
// trips its limit and gets cooled; the others stay effectively unlimited.
//
// Note: this cools prod-primary on the gateway for the (non-resettable) cooldown
// TTL, so this scenario is registered near the end of the layer-2 sequence.
func CooldownApplied(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	t.Log(color.Cyan("setting mock limits: prod-primary=1, prod-secondary=1000, dev-key=1000"))
	assert.NoError(t, "set prod-primary limit", mock.SetLimit("prod-primary", 1, ""))
	assert.NoError(t, "set prod-secondary limit", mock.SetLimit("prod-secondary", 1000, ""))
	assert.NoError(t, "set dev-key limit", mock.SetLimit("dev-key", 1000, ""))

	// Round-robin hits prod-primary every third request; 24 requests guarantee it
	// is selected past its limit of 1, producing a 429 and a cooldown.
	t.Log(color.Cyan("sending 24 requests; round-robin will hit prod-primary past its limit of 1, triggering 429 → cooldown"))
	for i := 0; i < 24; i++ {
		resp, err := gw.Get("/api/v1/status")
		assert.NoError(t, "cooldown drive request", err)
		if i == 0 {
			t.Logf("sample first response: status=%s body=%s", color.Status(resp.Status), resp.Body)
		} else if resp.Status != 200 {
			t.Logf("request %d: status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		}
	}

	t.Log(color.Cyan("checking /usage/prod-primary for a future cooled_until timestamp"))
	u := fetchUsage(t, gw, "/usage/prod-primary")
	pp, ok := findUsageKey(u, "prod-primary")
	assert.True(t, "prod-primary present in /usage", ok)
	t.Logf("prod-primary cooled_until=%v", pp.CooledUntil)
	assert.True(t, "prod-primary is cooled", pp.CooledUntil != nil)
	if pp.CooledUntil != nil {
		assert.True(t, "cooled_until is in the future", pp.CooledUntil.After(time.Now()))
	}
}

// CooldownRecovery verifies that a cooled key rejoins the pool once the TTL
// elapses and /usage reports cooled_until == nil. Uses the recovery-api route
// (/rapi) which has cooldown_ttl: 2s so the wait is observable in a test.
//
// Token mapping: recovery-key → TEST_KEY_C → mock name "dev-key".
func CooldownRecovery(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Cap dev-key at 1 request so the second request triggers a mock 429,
	// which the gateway converts into a cooldown on recovery-key.
	t.Log(color.Cyan("setting dev-key mock limit to 1 so the second request triggers 429 → cooldown"))
	assert.NoError(t, "set dev-key mock limit to 1", mock.SetLimit("dev-key", 1, ""))

	// First request: within the mock limit, returns 200.
	t.Log(color.Cyan("first request to /rapi/v1/status — expect 200 (within mock limit)"))
	resp1, err1 := gw.Get("/rapi/v1/status")
	assert.NoError(t, "first request", err1)
	t.Logf("response: status=%s body=%s", color.Status(resp1.Status), resp1.Body)
	assert.Equal(t, "first request status", resp1.Status, 200)

	// Second request: mock returns 429 → gateway cools recovery-key for 2s.
	t.Log(color.Cyan("second request to /rapi/v1/status — mock returns 429; gateway should cool recovery-key for 2s"))
	resp2, _ := gw.Get("/rapi/v1/status") // 429 or 503; we only care about the side-effect
	if resp2 != nil {
		t.Logf("response: status=%s body=%s (triggers cooldown)", color.Status(resp2.Status), resp2.Body)
	}

	// Confirm the gateway applied the cooldown.
	t.Log(color.Cyan("verifying recovery-key has a future cooled_until in /usage"))
	u1 := fetchUsage(t, gw, "/usage/recovery-key")
	rk1, ok1 := findUsageKey(u1, "recovery-key")
	assert.True(t, "recovery-key present in /usage", ok1)
	t.Logf("recovery-key cooled_until=%v", rk1.CooledUntil)
	assert.True(t, "recovery-key is cooled after 429", rk1.CooledUntil != nil)
	if rk1.CooledUntil != nil {
		assert.True(t, "cooled_until is in the future", rk1.CooledUntil.After(time.Now()))
	}

	// Wait for the 2s TTL plus a 1s buffer.
	t.Log(color.Cyan("sleeping 3s for cooldown TTL (2s) + 1s buffer to elapse"))
	time.Sleep(3 * time.Second)

	// Cooldown must have expired.
	t.Log(color.Cyan("checking recovery-key cooled_until is nil after TTL"))
	u2 := fetchUsage(t, gw, "/usage/recovery-key")
	rk2, ok2 := findUsageKey(u2, "recovery-key")
	assert.True(t, "recovery-key still in /usage after sleep", ok2)
	t.Logf("post-sleep recovery-key cooled_until=%v (want nil)", rk2.CooledUntil)
	assert.True(t, "recovery-key cooled_until is nil after TTL", rk2.CooledUntil == nil)

	// Raise mock limit and confirm the key is selected again.
	t.Log(color.Cyan("raising dev-key mock limit to 1000 and sending a final request to confirm recovery"))
	assert.NoError(t, "raise dev-key mock limit", mock.SetLimit("dev-key", 1000, ""))
	resp3, err3 := gw.Get("/rapi/v1/status")
	assert.NoError(t, "request after recovery", err3)
	t.Logf("response: status=%s body=%s", color.Status(resp3.Status), resp3.Body)
	assert.Equal(t, "request after recovery returns 200", resp3.Status, 200)
}
