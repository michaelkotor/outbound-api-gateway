package scenarios

import (
	"fmt"
	"strings"
	"testing"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// UsageAPI (phase 2): the /usage endpoints report accurate per-window counts,
// expose fingerprints only (never raw tokens), and support route and per-key
// filtering. The gateway store has no reset, so counts are asserted as a delta
// across a known number of requests rather than as absolute values.
func UsageAPI(t *testing.T, gw *client.GatewayClient, _ *client.MockClient) {
	t.Log(color.Cyan("reading baseline usage sum from GET /usage"))
	before := sumUsed(fetchUsage(t, gw, "/usage"))
	t.Logf("baseline total used across all keys: %s", color.Bold(fmt.Sprintf("%d", before)))

	const n = 9
	t.Logf(color.Cyan("sending %d requests to /api/v1/status to drive usage counters"), n)
	for i := 0; i < n; i++ {
		resp, err := gw.Get("/api/v1/status")
		assert.NoError(t, "usage drive request", err)
		if i == 0 {
			t.Logf("sample first response: status=%s body=%s", color.Status(resp.Status), resp.Body)
		} else if resp.Status != 200 {
			t.Logf("unexpected: request %d status=%s body=%s", i+1, color.Status(resp.Status), resp.Body)
		}
		assert.Equal(t, "usage drive status", resp.Status, 200)
	}

	after := sumUsed(fetchUsage(t, gw, "/usage"))
	t.Logf("post-drive total used: %s (delta=%s, want %d)",
		color.Bold(fmt.Sprintf("%d", after)),
		color.Bold(fmt.Sprintf("%d", after-before)),
		n)
	assert.Equal(t, "usage counted exactly n new requests", int(after-before), n)

	// Fingerprints only — the raw secrets (sk-...) must never appear.
	t.Log(color.Cyan("checking /usage body exposes fingerprints only, no raw sk- tokens"))
	raw, err := gw.Get("/usage")
	assert.NoError(t, "GET /usage raw", err)
	t.Logf("response: status=%s body=%s", color.Status(raw.Status), raw.Body)
	assert.Equal(t, "GET /usage status", raw.Status, 200)
	assert.Contains(t, "fingerprint present in /usage", string(raw.Body), "***")
	assert.True(t, "no raw token in /usage body", !strings.Contains(string(raw.Body), "sk-"))

	// Route filtering.
	t.Log(color.Cyan("checking route filter: GET /usage?route=api"))
	filtered := fetchUsage(t, gw, "/usage?route=api")
	t.Logf("route filter returned %s key(s)", color.Bold(fmt.Sprintf("%d", len(filtered.Keys))))
	assert.True(t, "route filter returns keys", len(filtered.Keys) >= 1)
	for _, k := range filtered.Keys {
		assert.Equal(t, "filtered key route", k.Route, "api")
	}
	empty := fetchUsage(t, gw, "/usage?route=does-not-exist")
	t.Logf("unknown route filter returned %s key(s) (want 0)", color.Bold(fmt.Sprintf("%d", len(empty.Keys))))
	assert.Equal(t, "unknown route returns no keys", len(empty.Keys), 0)

	// Per-key endpoint.
	t.Log(color.Cyan("checking per-key endpoint: GET /usage/prod-primary"))
	one := fetchUsage(t, gw, "/usage/prod-primary")
	t.Logf("per-key endpoint returned %s row(s)", color.Bold(fmt.Sprintf("%d", len(one.Keys))))
	assert.True(t, "per-key endpoint returns at least one row", len(one.Keys) >= 1)
	for _, k := range one.Keys {
		assert.Equal(t, "per-key endpoint name", k.Name, "prod-primary")
	}
}
