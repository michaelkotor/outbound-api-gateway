package keys

import (
	"fmt"
	"os"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
)

// Resolve reads the secret from the environment and computes the fingerprint.
// Returns an error if the env var is unset or empty.
func Resolve(keyConfig config.KeyConfig) (Key, error) {
	key, ok := os.LookupEnv(keyConfig.Env)
	if !ok || key == "" {
		return Key{}, fmt.Errorf("keys: env var %q for key %q is unset or empty", keyConfig.Env, keyConfig.Name)
	}

	limits := make([]Limit, 0, len(keyConfig.Limits))
	for _, limitConfig := range keyConfig.Limits {
		limits = append(limits, Limit{
			Window:      limitConfig.Window,
			MaxRequests: limitConfig.MaxRequests,
		})
	}

	return Key{
		Name:        keyConfig.Name,
		EnvVar:      keyConfig.Env,
		Value:       key,
		Fingerprint: Fingerprint(key),
		Limits:      limits,
	}, nil
}

// Fingerprint returns a safe display token for a raw secret value.
// Format: "***" + last4(value). Panics if value is shorter than 4 chars.
func Fingerprint(value string) string {
	if len(value) < 4 {
		panic("keys: cannot fingerprint a value shorter than 4 characters")
	}
	return "***" + value[len(value)-4:]
}
