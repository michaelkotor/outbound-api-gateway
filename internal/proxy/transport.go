package proxy

import (
	"net/http"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/metrics"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// trackedTransport wraps an http.RoundTripper to report each upstream response
// to the selector and record successful usage in the storage. The selected key
// is read from the request context (set by the proxy handler).
type trackedTransport struct {
	baseTransport http.RoundTripper
	selector      selector.KeySelector
	usageStorage  storage.Storage
	route         string
	windowsByKey  map[string][]time.Duration
}

// Compile-time check that trackedTransport is an http.RoundTripper.
var _ http.RoundTripper = (*trackedTransport)(nil)

// RoundTrip executes the request, then feeds the result back to the selector
// and increments usage on a non-rejected response.
func (transport *trackedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	selectedKey, _ := request.Context().Value(keyContextKey).(keys.Key)

	response, err := transport.baseTransport.RoundTrip(request)

	statusCode := 0
	if response != nil {
		statusCode = response.StatusCode
	}
	transport.selector.Feedback(selectedKey, statusCode, err)

	// A 429/401 is handled as a cooldown by Feedback and is not counted as
	// successful usage. Everything else (including upstream 5xx that still
	// consumed quota) increments the usage counters.
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusUnauthorized {
		metrics.RecordCooldown(selectedKey.Name, statusCode)
	} else if err == nil {
		_ = transport.usageStorage.IncreaseUsage(request.Context(), selectedKey.Name, transport.route, transport.windowsByKey[selectedKey.Name])
	}
	return response, err
}
