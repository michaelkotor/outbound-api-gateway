package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/proxy"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// mockSelector is a test-only selector.KeySelector.
type mockSelector struct {
	key keys.Key
	err error
}

func (m *mockSelector) Next(_ context.Context, _ string) (keys.Key, error) { return m.key, m.err }
func (m *mockSelector) Feedback(_ keys.Key, _ int, _ error)                {}

// noopStorage satisfies storage.Storage with no-op implementations.
type noopStorage struct{}

func (n *noopStorage) IncreaseUsage(_ context.Context, _, _ string, _ []time.Duration) error {
	return nil
}
func (n *noopStorage) CheckLimits(_ context.Context, _ string, _ []keys.Limit) (bool, *keys.Limit, error) {
	return true, nil, nil
}
func (n *noopStorage) MarkCooldown(_ context.Context, _ string, _ time.Time) error { return nil }
func (n *noopStorage) IsCooledDown(_ context.Context, _ string) (bool, error)      { return false, nil }
func (n *noopStorage) GetUsage(_ context.Context, _, _ string) (*storage.KeyUsage, error) {
	return nil, storage.ErrKeyNotFound
}
func (n *noopStorage) ListUsage(_ context.Context, _ string) ([]storage.KeyUsage, error) {
	return nil, nil
}

func makeRoute(upstream string) config.Route {
	return config.Route{
		Name:     "test-route",
		Prefix:   "/api",
		Upstream: upstream,
		Headers: config.HeaderConfig{
			Inject: map[string]string{"Authorization": "Bearer {key}"},
		},
	}
}

func TestNew_InvalidUpstreamReturnsError(t *testing.T) {
	route := makeRoute("://bad-url")
	_, err := proxy.New(route, &mockSelector{}, &noopStorage{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-route")
}

func TestNew_MissingSchemeReturnsError(t *testing.T) {
	route := makeRoute("example.com/api")
	_, err := proxy.New(route, &mockSelector{}, &noopStorage{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute URL")
}

func TestNew_MissingHostReturnsError(t *testing.T) {
	route := makeRoute("http://")
	_, err := proxy.New(route, &mockSelector{}, &noopStorage{})
	require.Error(t, err)
}

func TestNew_ValidUpstreamSucceeds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestHandler_PoolExhaustedReturns503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{err: selector.ErrPoolExhausted}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "no keys available")
}

func TestHandler_ForwardsToUpstreamAndReturnsResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-response"))
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/data", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandler_KeyInjectedIntoUpstreamHeader(t *testing.T) {
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	route.Headers.Inject = map[string]string{"Authorization": "Bearer {key}"}
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "my-secret-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "Bearer my-secret-1234", receivedAuth)
}

func TestHandler_StripHeaderRemovedFromUpstreamRequest(t *testing.T) {
	var receivedHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Internal-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := config.Route{
		Name:     "test-route",
		Prefix:   "/api",
		Upstream: upstream.URL,
		Headers: config.HeaderConfig{
			Strip: []string{"X-Internal-Token"},
		},
	}
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-Internal-Token", "should-be-stripped")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Empty(t, receivedHeader)
}

func TestHandler_PrefixStrippedBeforeForwarding(t *testing.T) {
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	route.Prefix = "/api"
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, "/v1/users", receivedPath)
}

func TestHandler_XForwardedForSet(t *testing.T) {
	var forwardedFor string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedFor = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Contains(t, forwardedFor, "10.0.0.1")
}

func TestHandler_XForwardedForAppended(t *testing.T) {
	var forwardedFor string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedFor = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Contains(t, forwardedFor, "192.168.1.1")
	assert.Contains(t, forwardedFor, "10.0.0.1")
}

func TestHandler_UpstreamUnreachableReturns502(t *testing.T) {
	route := makeRoute("http://127.0.0.1:1") // nothing listening
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}

	handler, err := proxy.New(route, sel, &noopStorage{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestHandler_UsageIncrementedOn200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}
	store := &trackingStorage{}

	handler, err := proxy.New(route, sel, store)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, 1, store.increaseUsageCalls)
}

func TestHandler_UsageNotIncrementedOn429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}
	store := &trackingStorage{}

	handler, err := proxy.New(route, sel, store)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, 0, store.increaseUsageCalls)
}

func TestHandler_UsageNotIncrementedOn401(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	route := makeRoute(upstream.URL)
	sel := &mockSelector{key: keys.Key{Name: "k1", Value: "secret-value-1234"}}
	store := &trackingStorage{}

	handler, err := proxy.New(route, sel, store)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, 0, store.increaseUsageCalls)
}

// trackingStorage counts IncreaseUsage calls.
type trackingStorage struct {
	noopStorage
	increaseUsageCalls int
}

func (t *trackingStorage) IncreaseUsage(_ context.Context, _, _ string, _ []time.Duration) error {
	t.increaseUsageCalls++
	return nil
}
