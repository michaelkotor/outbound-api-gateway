package redis_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/redis"
)

func newRedisStore(t *testing.T) (*redis.Storage, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	s, err := redis.New(mr.Addr())
	require.NoError(t, err)
	return s, mr
}

func TestNew_InvalidAddressReturnsError(t *testing.T) {
	_, err := redis.New("127.0.0.1:1") // nothing listening
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis")
}

func TestNew_ConnectsWithRedisURL(t *testing.T) {
	mr := miniredis.RunT(t)
	s, err := redis.New("redis://" + mr.Addr())
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestIncreaseUsage_CountsAccumulate(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.Len(t, usage.Windows, 1)
	assert.Equal(t, int64(3), usage.Windows[0].Count)
}

func TestIncreaseUsage_MultipleWindows(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	windows := []time.Duration{time.Hour, 24 * time.Hour}
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", windows))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", windows))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.Len(t, usage.Windows, 2)

	counts := make(map[time.Duration]int64)
	for _, w := range usage.Windows {
		counts[w.Window] = w.Count
	}
	assert.Equal(t, int64(2), counts[time.Hour])
	assert.Equal(t, int64(2), counts[24*time.Hour])
}

func TestIncreaseUsage_SeparateRoutes(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route2", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route2", []time.Duration{time.Hour}))

	u1, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	u2, err := s.GetUsage(ctx, "key-a", "route2")
	require.NoError(t, err)

	assert.Equal(t, int64(1), u1.Windows[0].Count)
	assert.Equal(t, int64(2), u2.Windows[0].Count)
}

func TestIncreaseUsage_ConcurrentSafe(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	var wg sync.WaitGroup
	goroutines := 10
	calls := 20
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < calls; j++ {
				_ = s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour})
			}
		}()
	}
	wg.Wait()

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	assert.Equal(t, int64(goroutines*calls), usage.Windows[0].Count)
}

func TestGetUsage_UnknownKeyReturnsErrKeyNotFound(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	_, err := s.GetUsage(ctx, "nonexistent", "route1")
	assert.ErrorIs(t, err, storage.ErrKeyNotFound)
}

func TestCheckLimits_UnderLimit(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 10}})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, exceeded)
}

func TestCheckLimits_AtLimit(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	for i := 0; i < 5; i++ {
		require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	}

	ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 5}})
	require.NoError(t, err)
	assert.False(t, ok)
	require.NotNil(t, exceeded)
	assert.Equal(t, time.Hour, exceeded.Window)
}

func TestCheckLimits_ZeroMeansUnlimited(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	for i := 0; i < 100; i++ {
		require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	}

	ok, _, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 0}})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCheckLimits_NoUsageIsUnderLimit(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	ok, exceeded, err := s.CheckLimits(ctx, "key-never-used", []keys.Limit{{Window: time.Hour, MaxRequests: 5}})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, exceeded)
}

func TestCheckLimits_AggregatesAcrossRoutes(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	for i := 0; i < 3; i++ {
		require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
		require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route2", []time.Duration{time.Hour}))
	}

	ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 5}})
	require.NoError(t, err)
	assert.False(t, ok)
	require.NotNil(t, exceeded)
}

func TestMarkCooldown_IsCooledDown(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(30*time.Second)))

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, cooled)
}

func TestMarkCooldown_PastTimeIsNoop(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(-time.Second)))

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.False(t, cooled)
}

func TestIsCooledDown_UnknownKeyReturnsFalse(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	cooled, err := s.IsCooledDown(ctx, "key-never-cooled")
	require.NoError(t, err)
	assert.False(t, cooled)
}

func TestIsCooledDown_ExpiredCooldownReturnsFalse(t *testing.T) {
	ctx := context.Background()
	s, mr := newRedisStore(t)

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(100*time.Millisecond)))
	mr.FastForward(200 * time.Millisecond)

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.False(t, cooled)
}

func TestMarkCooldown_LaterTimeWins(t *testing.T) {
	ctx := context.Background()
	s, mr := newRedisStore(t)

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(10*time.Second)))
	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(60*time.Second)))

	mr.FastForward(11 * time.Second)

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, cooled)
}

func TestListUsage_ReturnsAllKeys(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-b", "route1", []time.Duration{time.Hour}))

	all, err := s.ListUsage(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestListUsage_FiltersByRoute(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route2", []time.Duration{time.Hour}))

	results, err := s.ListUsage(ctx, "route2")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "route2", results[0].Route)
}

func TestListUsage_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	all, err := s.ListUsage(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestGetUsage_IncludesCooledUntil(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	until := time.Now().Add(30 * time.Second)
	require.NoError(t, s.MarkCooldown(ctx, "key-a", until))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.NotNil(t, usage.CooledUntil)
	assert.True(t, usage.CooledUntil.After(time.Now()))
}
