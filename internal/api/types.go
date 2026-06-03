// Package api defines the usage HTTP API payloads and handlers.
package api

import "time"

// UsageResponse is the top-level payload for all /usage endpoints.
type UsageResponse struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Keys        []KeyPayload `json:"keys"`
}

// KeyPayload is the per-key block returned in usage responses.
// Raw key values are never present; only Fingerprint is returned.
type KeyPayload struct {
	Name        string          `json:"name"`
	Fingerprint string          `json:"fingerprint"`
	Route       string          `json:"route"`
	Windows     []WindowPayload `json:"windows"`
	LastUsed    time.Time       `json:"last_used"`
	CooledUntil *time.Time      `json:"cooled_until"` // null if not cooled
}

// WindowPayload is one usage window in a key payload.
type WindowPayload struct {
	Window   string    `json:"window"` // human-readable, e.g. "24h"
	Used     int64     `json:"used"`
	Limit    int64     `json:"limit"` // 0 = unlimited
	ResetsAt time.Time `json:"resets_at"`
}
