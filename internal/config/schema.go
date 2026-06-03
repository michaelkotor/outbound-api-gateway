// Package config defines the YAML configuration schema for the gateway and
// the Load function that reads and parses it at startup.
package config

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level gateway configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Routes  []Route       `yaml:"routes"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Address     string        `yaml:"addr"`         // default ":9080"; overridable via GATEWAY_ADDR
	ReadTimeout time.Duration `yaml:"read_timeout"` // default 30s
}

// StorageConfig selects and configures the usage store backend.
type StorageConfig struct {
	Adapter  string `yaml:"adapter"`   // "memory" | "redis"
	RedisURL string `yaml:"redis_url"` // only used when adapter = "redis"
}

// Route describes a single upstream proxy route and its key pool.
type Route struct {
	Name            string        `yaml:"name"`
	Prefix          string        `yaml:"prefix"`           // incoming path prefix matched by proxy
	Upstream        string        `yaml:"upstream"`         // base URL of upstream API
	Selector        string        `yaml:"selector"`         // "round_robin" | "least_used" | "random"
	UpstreamTimeout time.Duration `yaml:"upstream_timeout"` // 0 = no timeout
	CooldownTTL     time.Duration `yaml:"cooldown_ttl"`     // 0 = use system default (60s)
	Headers         HeaderConfig  `yaml:"headers"`
	Keys            []KeyConfig   `yaml:"keys"`
}

// HeaderConfig controls header injection and stripping for a route.
type HeaderConfig struct {
	Inject map[string]string `yaml:"inject"` // value may contain "{key}" placeholder
	Strip  []string          `yaml:"strip"`
}

// KeyConfig is a single configured key within a route's pool.
type KeyConfig struct {
	Name   string        `yaml:"name"` // stable identifier; used in usage API and logs
	Env    string        `yaml:"env"`  // env var name holding the secret value
	Limits []LimitConfig `yaml:"limits"`
}

// LimitConfig is one usage-window constraint for a key.
type LimitConfig struct {
	Window      time.Duration `yaml:"window"`       // e.g. 24h, 1h
	MaxRequests int64         `yaml:"max_requests"` // 0 = unlimited
}

const (
	defaultAddr        = ":9080"
	defaultReadTimeout = 30 * time.Second

	// envAddr overrides the server address from the environment. It takes
	// precedence over the value in the config file.
	envAddr = "GATEWAY_ADDR"
)

// Load reads YAML configuration from path, expands ${ENV_VAR} tokens in the
// raw document, and unmarshals it into a Config. It applies server defaults but
// does not validate key environment variables (that is phase 2).
func Load(path string) (*Config, error) {
	rawYAML, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %q: %w", path, err)
	}

	expandedYAML := os.Expand(string(rawYAML), func(envName string) string {
		return os.Getenv(envName)
	})

	var config Config
	if err := yaml.Unmarshal([]byte(expandedYAML), &config); err != nil {
		return nil, fmt.Errorf("config: parsing %q: %w", path, err)
	}

	// Precedence for the listen address: GATEWAY_ADDR env > config file > default.
	if addr := os.Getenv(envAddr); addr != "" {
		config.Server.Address = addr
	}
	if config.Server.Address == "" {
		config.Server.Address = defaultAddr
	}
	if config.Server.ReadTimeout == 0 {
		config.Server.ReadTimeout = defaultReadTimeout
	}

	for index := range config.Routes {
		route := config.Routes[index]
		parsedUpstream, err := url.Parse(route.Upstream)
		if err != nil || parsedUpstream.Scheme == "" || parsedUpstream.Host == "" {
			return nil, fmt.Errorf("config: route %q: upstream %q is not a valid absolute URL", route.Name, route.Upstream)
		}
	}

	return &config, nil
}
