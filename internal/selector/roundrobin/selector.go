// Package roundrobin implements a round-robin selector.KeySelector.
package roundrobin

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// RoundRobinSelector picks keys in round-robin order, skipping keys that are
// cooled down or over their configured limits. It is safe for concurrent use.
type RoundRobinSelector struct {
	storage     storage.Storage
	keys        []keys.Key
	cooldownTTL time.Duration

	mutex     sync.Mutex
	nextIndex int
}

// Compile-time check that RoundRobinSelector satisfies the selector.KeySelector contract.
var _ selector.KeySelector = (*RoundRobinSelector)(nil)

// New constructs a round-robin selector backed by storage.
func New(usageStorage storage.Storage, keys []keys.Key, cooldownTTL time.Duration) selector.KeySelector {
	return &RoundRobinSelector{storage: usageStorage, keys: keys, cooldownTTL: cooldownTTL}
}

func (roundRobinSelector *RoundRobinSelector) Next(ctx context.Context, routeName string) (keys.Key, error) {
	roundRobinSelector.mutex.Lock()
	defer roundRobinSelector.mutex.Unlock()

	numberOfKeys := len(roundRobinSelector.keys)
	for attempt := 0; attempt < numberOfKeys; attempt++ {
		currentKey := roundRobinSelector.keys[roundRobinSelector.nextIndex]
		roundRobinSelector.nextIndex = (roundRobinSelector.nextIndex + 1) % numberOfKeys

		cooled, err := roundRobinSelector.storage.IsCooledDown(ctx, currentKey.Name)
		if err != nil {
			return keys.Key{}, err
		}
		if cooled {
			continue
		}
		withinLimits, _, err := roundRobinSelector.storage.CheckLimits(ctx, currentKey.Name, currentKey.Limits)
		if err != nil {
			return keys.Key{}, err
		}
		if !withinLimits {
			continue
		}
		return currentKey, nil
	}
	return keys.Key{}, selector.ErrPoolExhausted
}

func (roundRobinSelector *RoundRobinSelector) Feedback(key keys.Key, statusCode int, err error) {
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusUnauthorized {
		_ = roundRobinSelector.storage.MarkCooldown(context.Background(), key.Name, time.Now().Add(roundRobinSelector.cooldownTTL))
	}
}
