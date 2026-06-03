package roundrobin_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/roundrobin"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/selectortest"
)

func threeKeys() []keys.Key {
	return []keys.Key{{Name: "a"}, {Name: "b"}, {Name: "c"}}
}

// collect calls Next n times and returns the resulting key names. It fails the
// test on any error so callers can assume n successful selections.
func collect(t *testing.T, sel selector.KeySelector, n int) []string {
	t.Helper()
	ctx := context.Background()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k, err := sel.Next(ctx, "api")
		require.NoError(t, err)
		out = append(out, k.Name)
	}
	return out
}

func count(names []string, target string) int {
	n := 0
	for _, name := range names {
		if name == target {
			n++
		}
	}
	return n
}

func TestCyclesThroughAllKeys(t *testing.T) {
	mock := selectortest.New()
	sel := roundrobin.New(mock, threeKeys(), 60*time.Second)

	results := collect(t, sel, 10)
	assert.Equal(t, 4, count(results, "a")) // positions 0,3,6,9
	assert.Equal(t, 3, count(results, "b")) // positions 1,4,7
	assert.Equal(t, 3, count(results, "c")) // positions 2,5,8
}

func TestSkipsCooledKey(t *testing.T) {
	mock := selectortest.New()
	mock.CooledFn = func(name string) bool { return name == "b" }
	sel := roundrobin.New(mock, threeKeys(), 60*time.Second)

	results := collect(t, sel, 6)
	assert.NotContains(t, results, "b")
	assert.Equal(t, 3, count(results, "a"))
	assert.Equal(t, 3, count(results, "c"))
}

func TestSkipsOverLimitKey(t *testing.T) {
	mock := selectortest.New()
	mock.CheckLimitsFn = func(name string) (bool, *keys.Limit) {
		if name == "a" {
			return false, &keys.Limit{Window: time.Hour, MaxRequests: 1}
		}
		return true, nil
	}
	sel := roundrobin.New(mock, threeKeys(), 60*time.Second)

	results := collect(t, sel, 6)
	assert.NotContains(t, results, "a")
}

func TestReturnsPoolExhaustedWhenAllBlocked(t *testing.T) {
	mock := selectortest.New()
	mock.CooledFn = func(name string) bool { return true }
	sel := roundrobin.New(mock, threeKeys(), 60*time.Second)

	_, err := sel.Next(context.Background(), "api")
	assert.ErrorIs(t, err, selector.ErrPoolExhausted)
}

func TestConcurrentNextNoRace(t *testing.T) {
	mock := selectortest.New()
	sel := roundrobin.New(mock, threeKeys(), 60*time.Second)
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

func TestFeedback429TriggersCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := roundrobin.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 429, nil)

	until, ok := mock.Cooldown("a")
	assert.True(t, ok, "cooldown should be recorded for key a")
	assert.True(t, until.After(time.Now()), "cooldown until should be in the future")
}

func TestFeedback401TriggersCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := roundrobin.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 401, nil)

	_, ok := mock.Cooldown("a")
	assert.True(t, ok, "cooldown should be recorded for key a")
}

func TestFeedback200DoesNotCooldown(t *testing.T) {
	mock := selectortest.New()
	ks := threeKeys()
	sel := roundrobin.New(mock, ks, 60*time.Second)

	sel.Feedback(ks[0], 200, nil)

	assert.Equal(t, 0, mock.CooldownCount(), "no cooldown expected on a 200")
}
