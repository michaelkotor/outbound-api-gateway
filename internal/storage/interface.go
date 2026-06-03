// Package storage defines the usage persistence contract and shared usage types.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
)

// Storage is the persistence contract. Implementations must be safe for
// concurrent use. All methods accept a context for cancellation and deadline
// propagation.
type Storage interface {
	// IncreaseUsage atomically increments all window counters for the key.
	// windows is the list of durations configured for this key; the store
	// is responsible for bucketing counts into the correct time windows.
	IncreaseUsage(context context.Context, keyName string, route string, windows []time.Duration) error

	// CheckLimits returns ok=true if the key is within all configured limits.
	// If any limit is exceeded, ok=false and exceeded points to the first
	// breached Limit. Order of evaluation is implementation-defined.
	CheckLimits(context context.Context, keyName string, limits []keys.Limit) (ok bool, exceeded *keys.Limit, err error)

	// MarkCooldown sets a time-bounded cooldown on the key.
	// Any call to IsCooledDown before `until` must return true.
	MarkCooldown(context context.Context, keyName string, until time.Time) error

	// IsCooledDown returns true if the key has an active cooldown.
	IsCooledDown(context context.Context, keyName string) (bool, error)

	// GetUsage returns the current usage snapshot for a single key on a route.
	// Returns ErrKeyNotFound if the key has no recorded usage yet.
	GetUsage(context context.Context, keyName, route string) (*KeyUsage, error)

	// ListUsage returns usage snapshots for all keys.
	// If route is non-empty, results are filtered to that route only.
	ListUsage(context context.Context, route string) ([]KeyUsage, error)
}

// Sentinel errors.
var (
	ErrKeyNotFound = errors.New("storage: key not found")
)
