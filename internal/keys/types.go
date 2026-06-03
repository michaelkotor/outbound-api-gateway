// Package keys defines the resolved key types and the startup key-resolution
// contract.
package keys

import "time"

// Key is a resolved API key. Value is populated at startup from the env var
// and must never be serialized, logged, or stored.
type Key struct {
	Name        string  // stable human name from YAML; used in logs and usage API
	EnvVar      string  // env variable name, e.g. "API_KEY_PROD_1"
	Value       string  // resolved secret — never expose outside the proxy transport
	Fingerprint string  // safe display token: last 4 chars of Value prefixed with "***"
	Limits      []Limit // configured usage-window constraints, enforced at selection time
}

// Limit defines a single usage window constraint for a key.
type Limit struct {
	Window      time.Duration // e.g. 24h, 1h
	MaxRequests int64         // 0 = unlimited
}
