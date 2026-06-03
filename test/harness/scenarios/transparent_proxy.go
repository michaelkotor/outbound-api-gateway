package scenarios

import (
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// TransparentProxy (phase 2): with no client Authorization header the gateway
// selects a pool key, injects it, strips the route prefix, and forwards to the
// upstream. The mock authenticates the injected key and answers 200 — proving
// the request reached the upstream through the gateway with a valid key.
func TransparentProxy(t *testing.T, gw *client.GatewayClient, _ *client.MockClient) {
	t.Log(color.Cyan("GET /api/v1/status → gateway should inject a pool key and proxy to mock"))
	resp, err := gw.Get("/api/v1/status")
	assert.NoError(t, "GET /api/v1/status", err)
	t.Logf("response: status=%s body=%s", color.Status(resp.Status), resp.Body)
	assert.Equal(t, "status", resp.Status, 200)

	var body struct {
		OK      bool   `json:"ok"`
		KeyName string `json:"key_name"`
	}
	assert.NoError(t, "decode mock 200 body", resp.JSON(&body))
	t.Logf("mock authenticated request via key_name=%q ok=%v", body.KeyName, body.OK)
	assert.True(t, "mock response ok", body.OK)
	assert.True(t, "served key is a pool key ("+body.KeyName+")", inPool(body.KeyName))
}
