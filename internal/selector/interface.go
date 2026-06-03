// Package selector defines the key-selection contract used by the proxy to
// pick the next available key for a route.
package selector

import (
	"context"
	"errors"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
)

// KeySelector picks the next available key for a route and receives
// feedback after each upstream response. Implementations must be safe
// for concurrent use.
type KeySelector interface {
	// Next returns the next eligible key for routeName.
	// It checks cooldown and limits via the injected Storage before returning.
	// Returns ErrPoolExhausted if every key in the pool is cooled or over-limit.
	Next(ctx context.Context, routeName string) (keys.Key, error)

	// Feedback is called by the proxy transport after every upstream response.
	// Implementations use statusCode and err to decide whether to call
	// storage.MarkCooldown (e.g. on 429 or 401) or update internal state.
	Feedback(key keys.Key, statusCode int, err error)
}

// Sentinel errors.
var (
	ErrPoolExhausted = errors.New("selector: all keys exhausted or over limit")
)
