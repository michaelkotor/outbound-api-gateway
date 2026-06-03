package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/api"
	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// stubStorage is a minimal storage.Storage for handler tests.
type stubStorage struct {
	listResult []storage.KeyUsage
	listErr    error
}

func (s *stubStorage) IncreaseUsage(_ context.Context, _, _ string, _ []time.Duration) error {
	return nil
}
func (s *stubStorage) CheckLimits(_ context.Context, _ string, _ []keys.Limit) (bool, *keys.Limit, error) {
	return true, nil, nil
}
func (s *stubStorage) MarkCooldown(_ context.Context, _ string, _ time.Time) error { return nil }
func (s *stubStorage) IsCooledDown(_ context.Context, _ string) (bool, error)      { return false, nil }
func (s *stubStorage) GetUsage(_ context.Context, _, _ string) (*storage.KeyUsage, error) {
	return nil, storage.ErrKeyNotFound
}
func (s *stubStorage) ListUsage(_ context.Context, _ string) ([]storage.KeyUsage, error) {
	return s.listResult, s.listErr
}

func TestUsage_ReturnsAllKeys(t *testing.T) {
	now := time.Now()
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{
				KeyName:  "key-a",
				Route:    "api",
				Windows:  []storage.WindowUsage{{Window: time.Hour, Count: 5, ResetsAt: now.Add(time.Hour)}},
				LastUsed: now,
			},
			{
				KeyName:  "key-b",
				Route:    "api",
				Windows:  []storage.WindowUsage{{Window: time.Hour, Count: 3, ResetsAt: now.Add(time.Hour)}},
				LastUsed: now,
			},
		},
	}
	handler := api.NewHandler(store, map[string]string{"key-a": "***1234", "key-b": "***5678"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Keys, 2)
}

func TestUsage_RouteFilterPassedToStorage(t *testing.T) {
	store := &stubStorage{listResult: []storage.KeyUsage{}}
	handler := api.NewHandler(store, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage?route=myroute", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUsage_StorageErrorReturns500(t *testing.T) {
	store := &stubStorage{listErr: storage.ErrKeyNotFound}
	handler := api.NewHandler(store, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUsage_FingerprintFallsBackToMap(t *testing.T) {
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{KeyName: "key-a", Route: "api"},
		},
	}
	handler := api.NewHandler(store, map[string]string{"key-a": "***abcd"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, "***abcd", resp.Keys[0].Fingerprint)
}

func TestUsage_FingerprintFromStorage(t *testing.T) {
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{KeyName: "key-a", Route: "api", Fingerprint: "***from-storage"},
		},
	}
	handler := api.NewHandler(store, map[string]string{"key-a": "***map"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Keys, 1)
	assert.Equal(t, "***from-storage", resp.Keys[0].Fingerprint)
}

func TestUsage_ConfiguredLimitApplied(t *testing.T) {
	now := time.Now()
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{
				KeyName: "key-a",
				Route:   "api",
				Windows: []storage.WindowUsage{{Window: time.Hour, Count: 7, ResetsAt: now.Add(time.Hour)}},
			},
		},
	}
	configuredLimits := map[string]map[time.Duration]int64{
		"key-a": {time.Hour: 100},
	}
	handler := api.NewHandler(store, nil, configuredLimits)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Keys, 1)
	require.Len(t, resp.Keys[0].Windows, 1)
	assert.Equal(t, int64(100), resp.Keys[0].Windows[0].Limit)
}

func TestUsage_CooledUntilPropagated(t *testing.T) {
	until := time.Now().Add(30 * time.Second)
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{KeyName: "key-a", Route: "api", CooledUntil: &until},
		},
	}
	handler := api.NewHandler(store, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	handler.Usage(rec, req)

	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Keys, 1)
	require.NotNil(t, resp.Keys[0].CooledUntil)
}

func TestUsageByKey_ReturnsMatchingKey(t *testing.T) {
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{KeyName: "key-a", Route: "api"},
			{KeyName: "key-b", Route: "api"},
			{KeyName: "key-a", Route: "api2"},
		},
	}
	handler := api.NewHandler(store, nil, nil)

	router := chi.NewRouter()
	router.Get("/usage/{key_name}", handler.UsageByKey)

	req := httptest.NewRequest(http.MethodGet, "/usage/key-a", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Keys, 2)
	for _, k := range resp.Keys {
		assert.Equal(t, "key-a", k.Name)
	}
}

func TestUsageByKey_ReturnsEmptyWhenKeyNotFound(t *testing.T) {
	store := &stubStorage{
		listResult: []storage.KeyUsage{
			{KeyName: "key-b", Route: "api"},
		},
	}
	handler := api.NewHandler(store, nil, nil)

	router := chi.NewRouter()
	router.Get("/usage/{key_name}", handler.UsageByKey)

	req := httptest.NewRequest(http.MethodGet, "/usage/key-a", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp api.UsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Keys)
}

func TestUsageByKey_StorageErrorReturns500(t *testing.T) {
	store := &stubStorage{listErr: storage.ErrKeyNotFound}
	handler := api.NewHandler(store, nil, nil)

	router := chi.NewRouter()
	router.Get("/usage/{key_name}", handler.UsageByKey)

	req := httptest.NewRequest(http.MethodGet, "/usage/key-a", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestNewHandler_NilMapsAreHandledGracefully(t *testing.T) {
	store := &stubStorage{listResult: []storage.KeyUsage{{KeyName: "key-a", Route: "api"}}}
	handler := api.NewHandler(store, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	rec := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		handler.Usage(rec, req)
	})
}
