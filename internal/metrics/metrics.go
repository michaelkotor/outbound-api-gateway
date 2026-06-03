// Package metrics defines the Prometheus collectors exposed by the gateway and
// the helpers used to record gateway activity. All collectors are registered on
// the default registry, served by Handler at /metrics.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_requests_total",
		Help: "Total proxied requests, labeled by route, method, and response status.",
	}, []string{"route", "method", "status"})

	upstreamLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_upstream_latency_seconds",
		Help:    "End-to-end proxy latency in seconds, labeled by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})

	keySelectionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_key_selections_total",
		Help: "Number of times a key was selected for a route.",
	}, []string{"route", "key"})

	keyCooldownsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_key_cooldowns_total",
		Help: "Number of times a key was put on cooldown, labeled by key and triggering status.",
	}, []string{"key", "status"})

	poolExhaustedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_pool_exhausted_total",
		Help: "Number of requests rejected because every key in the route pool was unavailable.",
	}, []string{"route"})
)

// Handler returns the HTTP handler that serves the Prometheus exposition format.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordRequest records one proxied request and its latency.
func RecordRequest(route, method string, statusCode int, latency time.Duration) {
	requestsTotal.WithLabelValues(route, method, strconv.Itoa(statusCode)).Inc()
	upstreamLatencySeconds.WithLabelValues(route).Observe(latency.Seconds())
}

// RecordSelection records that keyName was selected to serve a request on route.
func RecordSelection(route, keyName string) {
	keySelectionsTotal.WithLabelValues(route, keyName).Inc()
}

// RecordCooldown records that keyName was cooled down after a given status code.
func RecordCooldown(keyName string, statusCode int) {
	keyCooldownsTotal.WithLabelValues(keyName, strconv.Itoa(statusCode)).Inc()
}

// RecordPoolExhausted records a request rejected because route's pool was empty.
func RecordPoolExhausted(route string) {
	poolExhaustedTotal.WithLabelValues(route).Inc()
}
