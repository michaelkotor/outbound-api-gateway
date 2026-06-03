package leastused_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/leastused"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/selectortest"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

func threeKeys() []keys.Key {
	return []keys.Key{{Name: "a"}, {Name: "b"}, {Name: "c"}}
}

// usageWith builds a UsageFn that reports a fixed 1h-window count per key name.
func usageWith(counts map[string]int64) func(keyName, route string) *storage.KeyUsage {
	return func(keyName, route string) *storage.KeyUsage {
		return &storage.KeyUsage{
			KeyName: keyName,
			Route:   route,
			Windows: []storage.WindowUsage{{Window: time.Hour, Count: counts[keyName]}},
		}
	}
}

func TestPicksKeyWithLowestWindowCount(t *testing.T) {
	mock := selectortest.New()
	mock.UsageFn = usageWith(map[string]int64{"a": 50, "b": 10, "c": 30})
	sel := leastused.New(mock, threeKeys(), 60*time.Second)

	k, err := sel.Next(context.Background(), "api")
	require.NoError(t, err)
	assert.Equal(t, "b", k.Name)
}

func TestTiesBrokenByStableOrder(t *testing.T) {
	mock := selectortest.New()
	mock.UsageFn = usageWith(map[string]int64{"a": 0, "b": 0, "c": 0})
	sel := leastused.New(mock, threeKeys(), 60*time.Second)
	ctx := context.Background()

	seen := map[string]int{}
	for i := 0; i < 3; i++ {
		k, err := sel.Next(ctx, "api")
		require.NoError(t, err)
		seen[k.Name]++
	}
	assert.Equal(t, map[string]int{"a": 1, "b": 1, "c": 1}, seen,
		"each key should be returned exactly once under a stable tiebreak")
}

func TestFallsBackToAvailableKeyWhenLeastUsedIsCooled(t *testing.T) {
	mock := selectortest.New()
	mock.UsageFn = usageWith(map[string]int64{"a": 50, "b": 10, "c": 30})
	mock.CooledFn = func(name string) bool { return name == "b" }
	sel := leastused.New(mock, threeKeys(), 60*time.Second)

	k, err := sel.Next(context.Background(), "api")
	require.NoError(t, err)
	assert.Equal(t, "c", k.Name)
}

func TestReturnsPoolExhaustedWhenAllCooled(t *testing.T) {
	mock := selectortest.New()
	mock.CooledFn = func(name string) bool { return true }
	sel := leastused.New(mock, threeKeys(), 60*time.Second)

	_, err := sel.Next(context.Background(), "api")
	assert.ErrorIs(t, err, selector.ErrPoolExhausted)
}

func TestReturnsPoolExhaustedWhenAllOverLimit(t *testing.T) {
	mock := selectortest.New()
	mock.CheckLimitsFn = func(name string) (bool, *keys.Limit) {
		return false, &keys.Limit{Window: time.Hour, MaxRequests: 1}
	}
	sel := leastused.New(mock, threeKeys(), 60*time.Second)

	_, err := sel.Next(context.Background(), "api")
	assert.ErrorIs(t, err, selector.ErrPoolExhausted)
}

func TestFeedback429TriggersCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := leastused.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 429, nil)

	until, ok := mock.Cooldown("a")
	assert.True(t, ok, "cooldown should be recorded for key a")
	assert.True(t, until.After(time.Now()), "cooldown until should be in the future")
}

func TestFeedback401TriggersCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := leastused.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 401, nil)

	_, ok := mock.Cooldown("a")
	assert.True(t, ok, "cooldown should be recorded for key a")
}

func TestFeedback200DoesNotCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := leastused.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 200, nil)

	assert.Equal(t, 0, mock.CooldownCount(), "no cooldown expected on a 200")
}

func TestFeedbackErrorDoesNotCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := leastused.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 0, assert.AnError)

	assert.Equal(t, 0, mock.CooldownCount(), "no cooldown expected on transport error")
}

func TestConcurrentNextNoRace(t *testing.T) {
	mock := selectortest.New()
	mock.UsageFn = usageWith(map[string]int64{"a": 0, "b": 0, "c": 0})
	sel := leastused.New(mock, threeKeys(), 60*time.Second)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				k, err := sel.Next(ctx, "api")
				require.NoError(t, err)
				assert.NotEmpty(t, k.Name)
			}
		}()
	}
	wg.Wait()
}
