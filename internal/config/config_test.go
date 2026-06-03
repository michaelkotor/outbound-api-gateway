package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
)

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

const validConfig = `server:
  addr: ":8080"
  read_timeout: 30s

storage:
  adapter: memory

routes:
  - name: api
    prefix: /api
    upstream: https://api.api.com
    selector: round_robin
    headers:
      inject:
        Authorization: "Bearer {key}"
    keys:
      - name: prod-primary
        env: API_KEY_PROD_1
        limits:
          - window: 24h
            max_requests: 10000
          - window: 1h
            max_requests: 1000
`

func TestLoadValidYAML(t *testing.T) {
	path := writeConfig(t, validConfig)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.Server.Address)
	require.Len(t, cfg.Routes, 1)
	assert.Equal(t, "api", cfg.Routes[0].Name)
	require.NotEmpty(t, cfg.Routes[0].Keys)
	assert.Equal(t, "API_KEY_PROD_1", cfg.Routes[0].Keys[0].Env)
}

func TestEnvExpansionInStringFields(t *testing.T) {
	t.Setenv("MY_UPSTREAM", "https://api.example.com")
	content := `server:
  addr: ":8080"
routes:
  - name: api
    prefix: /api
    upstream: "${MY_UPSTREAM}"
`
	path := writeConfig(t, content)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Routes, 1)
	assert.Equal(t, "https://api.example.com", cfg.Routes[0].Upstream)
}

func TestMissingFileReturnsError(t *testing.T) {
	_, err := config.Load("/nonexistent/path.yaml")
	require.Error(t, err)
}

func TestMalformedYAMLReturnsError(t *testing.T) {
	path := writeConfig(t, ":: invalid: yaml: [")
	_, err := config.Load(path)
	require.Error(t, err)
}

func TestInvalidUpstreamURLReturnsError(t *testing.T) {
	content := `server:
  addr: ":8080"
routes:
  - name: api
    prefix: /api
    upstream: "not a url"
`
	path := writeConfig(t, content)

	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api")
}
