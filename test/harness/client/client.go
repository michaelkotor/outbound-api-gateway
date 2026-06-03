// Package client provides typed HTTP clients for the gateway under test and
// the mock upstream API. They are used by the layer-2 scenarios and layer-3
// race cases in the integration harness.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Response is a decoded HTTP response: status code plus the raw body, with a
// lazy JSON decode helper.
type Response struct {
	Status int
	Body   []byte
}

// JSON decodes the response body into v.
func (r *Response) JSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// GatewayClient talks to the gateway under test.
type GatewayClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewGateway returns a GatewayClient with a sane default timeout.
func NewGateway(baseURL string) *GatewayClient {
	return &GatewayClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Do issues a request to path (relative to BaseURL) and reads the full body.
func (c *GatewayClient) Do(method, path string, header http.Header) (*Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return do(c.HTTP, req)
}

// Get is a convenience wrapper for GET with no extra headers.
func (c *GatewayClient) Get(path string) (*Response, error) {
	return c.Do(http.MethodGet, path, nil)
}

// WaitHealthy polls GET /healthz until it returns 200 or the timeout elapses.
func (c *GatewayClient) WaitHealthy(timeout time.Duration) error {
	return waitHealthy("gateway", c.HTTP, c.BaseURL+"/healthz", timeout)
}

// MockClient talks to the mock upstream API's admin surface.
type MockClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewMock returns a MockClient with a sane default timeout.
func NewMock(baseURL string) *MockClient {
	return &MockClient{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// WaitHealthy polls the mock's GET /healthz until 200 or timeout.
func (m *MockClient) WaitHealthy(timeout time.Duration) error {
	return waitHealthy("mockapi", m.HTTP, m.BaseURL+"/healthz", timeout)
}

// Reset zeroes all per-key window counters and clears cooldowns.
func (m *MockClient) Reset() error {
	resp, err := m.post("/admin/reset", nil)
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK {
		return fmt.Errorf("mock reset: status %d: %s", resp.Status, resp.Body)
	}
	return nil
}

// SetLimit overrides a key's limit (and optionally its window). Pass an empty
// window to leave the window unchanged.
func (m *MockClient) SetLimit(keyName string, limit int64, window string) error {
	body := map[string]any{"key_name": keyName, "limit": limit}
	if window != "" {
		body["window"] = window
	}
	resp, err := m.post("/admin/set-limit", body)
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK {
		return fmt.Errorf("mock set-limit %q: status %d: %s", keyName, resp.Status, resp.Body)
	}
	return nil
}

// SetLatency changes the artificial latency applied to every echo response.
func (m *MockClient) SetLatency(latencyMS int64) error {
	resp, err := m.post("/admin/set-latency", map[string]any{"latency_ms": latencyMS})
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK {
		return fmt.Errorf("mock set-latency: status %d: %s", resp.Status, resp.Body)
	}
	return nil
}

// KeyState is one key's row in the mock's /admin/state snapshot.
type KeyState struct {
	Name        string     `json:"name"`
	Fingerprint string     `json:"fingerprint"`
	RequestN    int64      `json:"request_n"`
	WindowCount int64      `json:"window_count"`
	Limit       int64      `json:"limit"`
	CooledUntil *time.Time `json:"cooled_until"`
	ResetsAt    time.Time  `json:"resets_at"`
}

// State is the full mock snapshot returned by GET /admin/state.
type State struct {
	SnapshotAt time.Time  `json:"snapshot_at"`
	Window     string     `json:"window"`
	Keys       []KeyState `json:"keys"`
}

// State fetches the mock's current per-key counters.
func (m *MockClient) State() (*State, error) {
	req, err := http.NewRequest(http.MethodGet, m.BaseURL+"/admin/state", nil)
	if err != nil {
		return nil, err
	}
	resp, err := do(m.HTTP, req)
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("mock state: status %d: %s", resp.Status, resp.Body)
	}
	var s State
	if err := resp.JSON(&s); err != nil {
		return nil, fmt.Errorf("mock state: decode: %w", err)
	}
	return &s, nil
}

func (m *MockClient) post(path string, body any) (*Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, m.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return do(m.HTTP, req)
}

func do(hc *http.Client, req *http.Request) (*Response, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Body: b}, nil
}

// waitHealthy polls url every 500ms until it returns 200 or timeout elapses.
func waitHealthy(name string, hc *http.Client, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := hc.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become healthy within %s", name, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
