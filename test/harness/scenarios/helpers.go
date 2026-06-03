package scenarios

import (
	"testing"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
)

// poolKeys are the key names configured for the api route in
// test/config.test.yaml, in declaration (round-robin) order.
var poolKeys = []string{"prod-primary", "prod-secondary", "dev-key"}

// usageWindow mirrors one entry of api.WindowPayload.
type usageWindow struct {
	Window string `json:"window"`
	Used   int64  `json:"used"`
	Limit  int64  `json:"limit"`
}

// usageKey mirrors api.KeyPayload (only the fields scenarios assert on).
type usageKey struct {
	Name        string        `json:"name"`
	Fingerprint string        `json:"fingerprint"`
	Route       string        `json:"route"`
	Windows     []usageWindow `json:"windows"`
	CooledUntil *time.Time    `json:"cooled_until"`
}

// usageResponse mirrors api.UsageResponse.
type usageResponse struct {
	Keys []usageKey `json:"keys"`
}

// fetchUsage GETs a gateway /usage path and decodes it, failing the scenario on
// any transport, status, or decode error.
func fetchUsage(t *testing.T, gw *client.GatewayClient, path string) usageResponse {
	t.Helper()
	resp, err := gw.Get(path)
	assert.NoError(t, "GET "+path, err)
	assert.Equal(t, "GET "+path+" status", resp.Status, 200)
	var u usageResponse
	assert.NoError(t, "decode "+path, resp.JSON(&u))
	return u
}

// sumUsed totals the per-window used counts across every key in a usage snapshot.
func sumUsed(u usageResponse) int64 {
	var total int64
	for _, k := range u.Keys {
		for _, w := range k.Windows {
			total += w.Used
		}
	}
	return total
}

// findUsageKey returns the usage row for name, if present.
func findUsageKey(u usageResponse, name string) (usageKey, bool) {
	for _, k := range u.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return usageKey{}, false
}

// inPool reports whether name is one of the configured pool keys.
func inPool(name string) bool {
	for _, k := range poolKeys {
		if k == name {
			return true
		}
	}
	return false
}
