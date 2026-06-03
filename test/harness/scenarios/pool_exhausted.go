package scenarios

import (
	"fmt"
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// PoolExhausted (phase 2): once every key has been cooled (each trips its mock
// limit and gets a 429), the selector reports ErrPoolExhausted and the gateway
// returns 503. We cap every key at 1 request, then drive traffic until a 503
// appears. This cools the whole pool, so it is registered as the last
// state-mutating layer-2 scenario.
func PoolExhausted(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	t.Logf(color.Cyan("capping all %d pool keys to mock limit=1 to force 429s and cooldowns"), len(poolKeys))
	for _, name := range poolKeys {
		assert.NoError(t, "set "+name+" limit", mock.SetLimit(name, 1, ""))
	}

	t.Log(color.Cyan("draining pool: sending requests until gateway returns 503 (up to 40 attempts)"))
	var saw503 bool
	var body string
	for i := 0; i < 40 && !saw503; i++ {
		resp, err := gw.Get("/api/v1/status")
		assert.NoError(t, "pool-drain request", err)
		t.Logf("  request %d: status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		if resp.Status == 503 {
			saw503 = true
			body = string(resp.Body)
		}
	}

	t.Logf("pool exhausted saw503=%s body=%q", color.Bold(fmt.Sprintf("%v", saw503)), body)
	assert.True(t, "gateway returns 503 once all keys are cooled", saw503)
	assert.Contains(t, "503 body names the exhausted pool", body, "no keys available")
}
