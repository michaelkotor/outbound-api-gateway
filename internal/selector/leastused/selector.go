// Package leastused implements a least-used selector.KeySelector.
package leastused

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// LeastUsedSelector picks the eligible key with the lowest aggregate window usage.
// Ties are broken in a stable, rotating order so equal keys are distributed
// evenly. It is safe for concurrent use.
type LeastUsedSelector struct {
	storage     storage.Storage
	keys        []keys.Key
	cooldownTTL time.Duration

	mutex          sync.Mutex
	rotatingOffset int // rotating offset used to break ties fairly
}

// Compile-time check that LeastUsedSelector satisfies the selector.KeySelector contract.
var _ selector.KeySelector = (*LeastUsedSelector)(nil)

// New constructs a least-used selector backed by storage.
func New(usageStorage storage.Storage, keys []keys.Key, cooldownTTL time.Duration) selector.KeySelector {
	return &LeastUsedSelector{storage: usageStorage, keys: keys, cooldownTTL: cooldownTTL}
}

func (leastUsedSelector *LeastUsedSelector) Next(context context.Context, routeName string) (keys.Key, error) {
	leastUsedSelector.mutex.Lock()
	defer leastUsedSelector.mutex.Unlock()

	numberOfKeys := len(leastUsedSelector.keys)
	var (
		bestKey   keys.Key
		bestCount int64
		bestOff   int
		haveBest  bool
	)

	for offset := 0; offset < numberOfKeys; offset++ {
		index := (leastUsedSelector.rotatingOffset + offset) % numberOfKeys
		currentKey := leastUsedSelector.keys[index]

		cooled, err := leastUsedSelector.storage.IsCooledDown(context, currentKey.Name)
		if err != nil {
			return keys.Key{}, err
		}
		if cooled {
			continue
		}
		withinLimits, _, err := leastUsedSelector.storage.CheckLimits(context, currentKey.Name, currentKey.Limits)
		if err != nil {
			return keys.Key{}, err
		}
		if !withinLimits {
			continue
		}

		counter, err := leastUsedSelector.usageCount(context, currentKey.Name, routeName)
		if err != nil {
			return keys.Key{}, err
		}
		// First eligible key wins ties because off increases monotonically.
		if !haveBest || counter < bestCount {
			bestKey = currentKey
			bestCount = counter
			bestOff = offset
			haveBest = true
		}
	}

	if !haveBest {
		return keys.Key{}, selector.ErrPoolExhausted
	}

	// Advance the tiebreak rotation past the chosen key.
	leastUsedSelector.rotatingOffset = (leastUsedSelector.rotatingOffset + bestOff + 1) % numberOfKeys
	return bestKey, nil
}

// usageCount returns the aggregate count across all of a key's windows on a
// route. A key with no recorded usage counts as zero.
func (leastUsedSelector *LeastUsedSelector) usageCount(ctx context.Context, keyName, route string) (int64, error) {
	usage, err := leastUsedSelector.storage.GetUsage(ctx, keyName, route)
	if errors.Is(err, storage.ErrKeyNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, window := range usage.Windows {
		total += window.Count
	}
	return total, nil
}

func (leastUsedSelector *LeastUsedSelector) Feedback(key keys.Key, statusCode int, err error) {
	if statusCode == http.StatusTooManyRequests || statusCode == http.StatusUnauthorized {
		_ = leastUsedSelector.storage.MarkCooldown(context.Background(), key.Name, time.Now().Add(leastUsedSelector.cooldownTTL))
	}
}
