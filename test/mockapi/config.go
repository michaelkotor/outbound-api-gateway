package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the runtime configuration parsed from environment variables.
type Config struct {
	Port         string
	RawKeys      string
	DefaultLimit int64
	Window       time.Duration
	LatencyMS    int64
}

const (
	defaultPort         = "9090"
	defaultLimit        = int64(1000)
	defaultWindowString = "1m"
)

// loadConfig reads and validates configuration from the environment. It returns
// a descriptive error for any invalid value so the caller can exit non-zero.
func loadConfig() (*Config, error) {
	cfg := &Config{
		Port:         getenv("MOCK_PORT", defaultPort),
		RawKeys:      os.Getenv("MOCK_KEYS"),
		DefaultLimit: defaultLimit,
		LatencyMS:    0,
	}

	if cfg.RawKeys == "" {
		return nil, fmt.Errorf("MOCK_KEYS is required (comma-separated name:token pairs)")
	}

	windowStr := getenv("MOCK_WINDOW", defaultWindowString)
	window, err := time.ParseDuration(windowStr)
	if err != nil {
		return nil, fmt.Errorf("MOCK_WINDOW %q is not a valid Go duration: %w", windowStr, err)
	}
	if window <= 0 {
		return nil, fmt.Errorf("MOCK_WINDOW must be positive, got %q", windowStr)
	}
	cfg.Window = window

	if v := os.Getenv("MOCK_DEFAULT_LIMIT"); v != "" {
		limit, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("MOCK_DEFAULT_LIMIT %q is not an integer: %w", v, err)
		}
		if limit <= 0 {
			return nil, fmt.Errorf("MOCK_DEFAULT_LIMIT must be a positive integer, got %q", v)
		}
		cfg.DefaultLimit = limit
	}

	if v := os.Getenv("MOCK_LATENCY_MS"); v != "" {
		latency, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("MOCK_LATENCY_MS %q is not an integer: %w", v, err)
		}
		if latency < 0 {
			return nil, fmt.Errorf("MOCK_LATENCY_MS must be non-negative, got %q", v)
		}
		cfg.LatencyMS = latency
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
