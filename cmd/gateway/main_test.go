package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/memory"
)

func TestNewStorage_MemoryAdapter(t *testing.T) {
	cfg := &config.Config{Storage: config.StorageConfig{Adapter: "memory"}}
	store, err := newStorage(cfg)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestNewStorage_EmptyAdapterDefaultsToMemory(t *testing.T) {
	cfg := &config.Config{Storage: config.StorageConfig{Adapter: ""}}
	store, err := newStorage(cfg)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestNewStorage_UnknownAdapterReturnsError(t *testing.T) {
	cfg := &config.Config{Storage: config.StorageConfig{Adapter: "cassandra"}}
	_, err := newStorage(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cassandra")
}

func TestNewStorage_RedisInvalidAddressReturnsError(t *testing.T) {
	cfg := &config.Config{Storage: config.StorageConfig{Adapter: "redis", RedisURL: "127.0.0.1:1"}}
	_, err := newStorage(cfg)
	require.Error(t, err)
}

func TestResolveKeys_ResolvesValidKey(t *testing.T) {
	t.Setenv("TEST_GW_KEY", "secret-value-1234")
	route := config.Route{
		Name: "test-route",
		Keys: []config.KeyConfig{
			{Name: "my-key", Env: "TEST_GW_KEY"},
		},
	}

	resolvedKeys, err := resolveKeys(route)
	require.NoError(t, err)
	require.Len(t, resolvedKeys, 1)
	assert.Equal(t, "my-key", resolvedKeys[0].Name)
	assert.Equal(t, "secret-value-1234", resolvedKeys[0].Value)
}

func TestResolveKeys_MissingEnvVarReturnsError(t *testing.T) {
	route := config.Route{
		Name: "test-route",
		Keys: []config.KeyConfig{
			{Name: "bad-key", Env: "DEFINITELY_NOT_SET_XYZ"},
		},
	}

	_, err := resolveKeys(route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test-route")
}

func TestResolveKeys_EmptyPoolReturnsEmpty(t *testing.T) {
	route := config.Route{Name: "empty-route", Keys: nil}
	resolvedKeys, err := resolveKeys(route)
	require.NoError(t, err)
	assert.Empty(t, resolvedKeys)
}

func TestNewKeySelector_RoundRobin(t *testing.T) {
	store := memory.New()
	sel, err := newKeySelector("round_robin", store, nil, 60*time.Second)
	require.NoError(t, err)
	require.NotNil(t, sel)
}

func TestNewKeySelector_EmptyDefaultsToRoundRobin(t *testing.T) {
	store := memory.New()
	sel, err := newKeySelector("", store, nil, 60*time.Second)
	require.NoError(t, err)
	require.NotNil(t, sel)
}

func TestNewKeySelector_LeastUsed(t *testing.T) {
	store := memory.New()
	sel, err := newKeySelector("least_used", store, nil, 60*time.Second)
	require.NoError(t, err)
	require.NotNil(t, sel)
}

func TestNewKeySelector_UnsupportedReturnsError(t *testing.T) {
	store := memory.New()
	_, err := newKeySelector("random", store, nil, 60*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "random")
}

func TestSwappableHandler_ServesRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	sh := &swappableHandler{}
	sh.set(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sh.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
}

func TestSwappableHandler_SwapIsLive(t *testing.T) {
	first := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	second := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	sh := &swappableHandler{}
	sh.set(first)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	sh.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	sh.set(second)

	rec = httptest.NewRecorder()
	sh.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestBuildHandler_ValidConfig(t *testing.T) {
	t.Setenv("BH_KEY_1234", "secret-value-abcd")

	cfg := &config.Config{
		Server: config.ServerConfig{Address: ":9080", ReadTimeout: 30 * time.Second},
		Routes: []config.Route{
			{
				Name:     "test",
				Prefix:   "/test",
				Upstream: "https://api.example.com",
				Selector: "round_robin",
				Keys: []config.KeyConfig{
					{Name: "key1", Env: "BH_KEY_1234"},
				},
			},
		},
	}

	store := memory.New()
	handler, err := buildHandler(cfg, store)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestBuildHandler_MissingKeyEnvVarReturnsError(t *testing.T) {
	cfg := &config.Config{
		Routes: []config.Route{
			{
				Name:     "test",
				Prefix:   "/test",
				Upstream: "https://api.example.com",
				Keys: []config.KeyConfig{
					{Name: "key1", Env: "DEFINITELY_NOT_SET_ABC"},
				},
			},
		},
	}

	store := memory.New()
	_, err := buildHandler(cfg, store)
	require.Error(t, err)
}

func TestBuildHandler_EmptyPrefixReturnsError(t *testing.T) {
	t.Setenv("BP_KEY_ABCD", "secret-value-1234")

	cfg := &config.Config{
		Routes: []config.Route{
			{
				Name:     "test",
				Prefix:   "",
				Upstream: "https://api.example.com",
				Keys:     []config.KeyConfig{{Name: "key1", Env: "BP_KEY_ABCD"}},
			},
		},
	}

	store := memory.New()
	_, err := buildHandler(cfg, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty prefix")
}

func TestBuildHandler_HealthzEndpoint(t *testing.T) {
	cfg := &config.Config{}
	store := memory.New()

	handler, err := buildHandler(cfg, store)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")
}
