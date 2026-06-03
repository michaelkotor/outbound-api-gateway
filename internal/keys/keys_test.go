package keys_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
)

func TestResolveReadsEnvVar(t *testing.T) {
	t.Setenv("TEST_KEY_VAR", "sk-abc123")

	key, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "TEST_KEY_VAR"})
	require.NoError(t, err)
	assert.Equal(t, "test", key.Name)
	assert.Equal(t, "sk-abc123", key.Value)
	assert.Equal(t, "TEST_KEY_VAR", key.EnvVar)
	// last-4 rule: last four chars of "sk-abc123" are "c123".
	assert.Equal(t, "***c123", key.Fingerprint)
}

func TestResolveMissingEnvVarReturnsError(t *testing.T) {
	_, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "DEFINITELY_NOT_SET"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEFINITELY_NOT_SET")
}

func TestResolveEmptyEnvVarReturnsError(t *testing.T) {
	t.Setenv("EMPTY_KEY", "")
	_, err := keys.Resolve(config.KeyConfig{Name: "test", Env: "EMPTY_KEY"})
	require.Error(t, err)
}

func TestFingerprintLastFour(t *testing.T) {
	fp := keys.Fingerprint("sk-abcdefghij1234")
	assert.Equal(t, "***1234", fp)
}

func TestFingerprintShortValuePanics(t *testing.T) {
	assert.Panics(t, func() {
		keys.Fingerprint("abc")
	})
}
