package storage

import "time"

// WindowUsage is the current state of one limit window for a key.
type WindowUsage struct {
	Window      time.Duration
	Count       int64
	MaxRequests int64     // mirrors the configured Limit.MaxRequests; 0 = unlimited
	ResetsAt    time.Time // when this window counter resets
}

// KeyUsage is the full usage snapshot for one key on one route.
type KeyUsage struct {
	KeyName     string
	Fingerprint string
	Route       string
	Windows     []WindowUsage
	LastUsed    time.Time
	CooledUntil *time.Time // nil if not cooled
}
