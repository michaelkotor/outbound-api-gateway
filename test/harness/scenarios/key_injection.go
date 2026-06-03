package scenarios

import (
	"fmt"
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// KeyInjection (phase 2): with no client Authorization header the gateway selects
// a pool key, injects "Authorization: Bearer {key}", and the mock answers 200
// echoing the chosen key_name. We additionally confirm the mock counted exactly
// the echoed key in its (freshly reset) window, proving the injected token mapped
// to the key the gateway claims it used.
func KeyInjection(t *testing.T, gw *client.GatewayClient, mock *client.MockClient) {
	t.Log(color.Cyan("GET /api/v1/models → gateway injects Authorization: Bearer {key}"))
	resp, err := gw.Get("/api/v1/models")
	assert.NoError(t, "GET /api/v1/models", err)
	t.Logf("response: status=%s body=%s", color.Status(resp.Status), resp.Body)
	assert.Equal(t, "status", resp.Status, 200)

	var body struct {
		OK      bool   `json:"ok"`
		KeyName string `json:"key_name"`
	}
	assert.NoError(t, "decode mock body", resp.JSON(&body))
	t.Logf("mock echoed key_name=%q ok=%v", body.KeyName, body.OK)
	assert.True(t, "mock authenticated the injected key", body.OK)
	assert.True(t, "served key is a pool key ("+body.KeyName+")", inPool(body.KeyName))

	t.Log(color.Cyan("verifying mock window counter incremented for the echoed key"))
	state, err := mock.State()
	assert.NoError(t, "GET mock /admin/state", err)
	var found bool
	for _, ks := range state.Keys {
		if ks.Name == body.KeyName {
			found = true
			t.Logf("mock state: key=%q window_count=%s", ks.Name, color.Bold(fmt.Sprintf("%d", ks.WindowCount)))
			assert.True(t, "served key window_count >= 1", ks.WindowCount >= 1)
		}
	}
	assert.True(t, "served key present in mock state", found)
}
