package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/michaelkotor/outbound-api-gateway/internal/metrics"
)

func TestHandler_ServesPrometheusMetrics(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "go_")
}

func TestRecordRequest_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordRequest("test-route", "GET", 200, 42*time.Millisecond)
	})
}

func TestRecordRequest_DifferentStatuses(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordRequest("route-a", "POST", 201, 10*time.Millisecond)
		metrics.RecordRequest("route-a", "GET", 500, 5*time.Second)
		metrics.RecordRequest("route-b", "DELETE", 404, 1*time.Millisecond)
	})
}

func TestRecordSelection_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordSelection("test-route", "key-a")
		metrics.RecordSelection("test-route", "key-b")
	})
}

func TestRecordCooldown_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordCooldown("key-a", 429)
		metrics.RecordCooldown("key-b", 401)
	})
}

func TestRecordPoolExhausted_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordPoolExhausted("test-route")
		metrics.RecordPoolExhausted("another-route")
	})
}

func TestHandler_MetricsAppearsAfterRecord(t *testing.T) {
	metrics.RecordPoolExhausted("metrics-test-unique-route")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, req)

	assert.Contains(t, rec.Body.String(), "gateway_pool_exhausted_total")
}
