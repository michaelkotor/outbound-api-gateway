// Package scenarios holds the layer-2 integration scenarios. Each scenario is
// an exported function with the signature
//
//	func(t *testing.T, gw *client.GatewayClient, mock *client.MockClient)
//
// registered and run sequentially by the harness. Scenarios marked phase 2 call
// t.Skip until the corresponding gateway feature is implemented.
package scenarios

import (
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// Healthz verifies the gateway health endpoint.
func Healthz(t *testing.T, gw *client.GatewayClient, _ *client.MockClient) {
	t.Log(color.Cyan("GET /healthz → expect 200 with {status:ok}"))
	resp, err := gw.Get("/healthz")
	assert.NoError(t, "GET /healthz", err)
	t.Logf("response: status=%s body=%s", color.Status(resp.Status), resp.Body)
	assert.Equal(t, "status", resp.Status, 200)

	var body struct {
		Status string `json:"status"`
	}
	assert.NoError(t, "decode /healthz body", resp.JSON(&body))
	assert.Equal(t, "body.status", body.Status, "ok")
}
