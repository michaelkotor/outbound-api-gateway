package scenarios

import (
	"testing"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// UpstreamTimeout verifies that a slow upstream is cut off promptly rather
// than allowed to stall the gateway indefinitely. The recovery-api route has
// upstream_timeout: 500ms; the mock is configured to sleep 2000ms per
// request. The gateway must return 502 in well under 2s.
func UpstreamTimeout(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	// Restore latency to 0 when the scenario exits, even on failure, so the
	// next scenario does not inherit the slow mock.
	t.Cleanup(func() {
		t.Log(color.Cyan("cleanup: resetting mock latency to 0ms"))
		_ = mock.SetLatency(0)
	})

	t.Log(color.Cyan("setting mock latency to 2000ms; recovery-api route has upstream_timeout: 500ms"))
	assert.NoError(t, "set mock latency to 2000ms", mock.SetLatency(2000))

	t.Log(color.Cyan("sending GET /rapi/v1/status — expect 502 well under 2s"))
	start := time.Now()
	resp, err := gw.Get("/rapi/v1/status")
	elapsed := time.Since(start)

	t.Logf("response: status=%s body=%s elapsed=%s (mock latency=2000ms, gateway timeout=500ms)",
		color.Status(resp.Status), resp.Body, elapsed.Round(time.Millisecond))
	assert.NoError(t, "request must not error at transport level", err)
	assert.Equal(t, "slow upstream returns 502", resp.Status, 502)
	assert.Contains(t, "502 body says upstream failed", string(resp.Body), "upstream request failed")
	// Response must arrive well before the 2s mock latency: use 1500ms as a
	// generous upper bound that still catches a hang.
	assert.True(t, "response arrived before mock latency elapsed",
		elapsed < 1500*time.Millisecond)
}
