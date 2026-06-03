package scenarios

import (
	"fmt"
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// RoundRobinDistribution (phase 2): with all keys eligible, 30 sequential
// requests spread evenly across the three pool keys (exactly 10 each). The mock
// is reset before the scenario, so its window counters reflect only these
// requests. 30 is a multiple of the pool size, so the distribution is exact
// regardless of the selector's starting index.
func RoundRobinDistribution(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	const total = 30
	t.Logf(color.Cyan("sending %d sequential requests to /api/v1/status across %d pool keys"), total, len(poolKeys))
	for i := 0; i < total; i++ {
		resp, err := gw.Get("/api/v1/status")
		assert.NoError(t, "round-robin request", err)
		if i == 0 {
			t.Logf("sample first response: status=%s body=%s", color.Status(resp.Status), resp.Body)
		} else if resp.Status != 200 {
			t.Logf("unexpected: request %d status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		}
		assert.Equal(t, "round-robin status", resp.Status, 200)
	}
	t.Logf(color.Cyan("all %d requests returned 200; checking per-key distribution in mock state"), total)

	state, err := mock.State()
	assert.NoError(t, "GET mock /admin/state", err)
	assert.Equal(t, "mock key count", len(state.Keys), len(poolKeys))

	want := total / len(poolKeys)
	for _, ks := range state.Keys {
		t.Logf("  key=%q window_count=%s (want %d)", ks.Name, color.Bold(fmt.Sprintf("%d", ks.WindowCount)), want)
		assert.Equal(t, "window_count for "+ks.Name, int(ks.WindowCount), want)
	}
}
