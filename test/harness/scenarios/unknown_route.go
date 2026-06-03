package scenarios

import (
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// UnknownRoute verifies that a path matching no configured route prefix returns
// 404 from the gateway's router rather than being proxied anywhere.
func UnknownRoute(t *testing.T, gw *client.GatewayClient, _ *client.MockClient) {
	t.Log(color.Cyan("GET /nonexistent/path → expect 404 from gateway router (no proxy)"))
	resp, err := gw.Get("/nonexistent/path")
	assert.NoError(t, "GET /nonexistent/path", err)
	t.Logf("response: status=%s body=%s", color.Status(resp.Status), resp.Body)
	assert.Equal(t, "status", resp.Status, 404)
}
