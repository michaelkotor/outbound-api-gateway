package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/memory"
)

func TestNew(t *testing.T) {
	s := memory.New()
	require.NotNil(t, s)
}

func TestIncreaseUsage_SingleWindow(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.Len(t, usage.Windows, 1)
	assert.Equal(t, int64(2), usage.Windows[0].Count)
	assert.Equal(t, time.Hour, usage.Windows[0].Window)
}

func TestIncreaseUsage_MultipleWindows(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

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

func TestIncreaseUsage_SeparateRoutesAreIndependent(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

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

func TestIncreaseUsage_SeparateKeysAreIndependent(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-b", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-b", "route1", []time.Duration{time.Hour}))

	ua, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	ub, err := s.GetUsage(ctx, "key-b", "route1")
	require.NoError(t, err)

	assert.Equal(t, int64(1), ua.Windows[0].Count)
	assert.Equal(t, int64(2), ub.Windows[0].Count)
}

func TestIncreaseUsage_WindowResetAfterExpiry(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	shortWindow := 50 * time.Millisecond
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{shortWindow}))
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{shortWindow}))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), usage.Windows[0].Count)
}

func TestIncreaseUsage_Concurrent(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	var wg sync.WaitGroup
	goroutines := 20
	calls := 50
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
	s := memory.New()

	_, err := s.GetUsage(ctx, "nonexistent", "route1")
	assert.ErrorIs(t, err, storage.ErrKeyNotFound)
}

func TestGetUsage_UnknownRouteReturnsErrKeyNotFound(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	_, err := s.GetUsage(ctx, "key-a", "nonexistent-route")
	assert.ErrorIs(t, err, storage.ErrKeyNotFound)
}

func TestGetUsage_LastUsedIsUpdated(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	before := time.Now()
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	assert.True(t, usage.LastUsed.After(before) || usage.LastUsed.Equal(before))
}

func TestCheckLimits_UnderLimit(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 10}})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, exceeded)
}

func TestCheckLimits_AtLimitReturnsExceeded(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

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
	s := memory.New()

	for i := 0; i < 100; i++ {
		require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	}

	ok, _, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 0}})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCheckLimits_NoUsageIsUnderLimit(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	ok, exceeded, err := s.CheckLimits(ctx, "key-never-used", []keys.Limit{{Window: time.Hour, MaxRequests: 5}})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, exceeded)
}

func TestCheckLimits_AggregatesAcrossRoutes(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	// 3 on route1, 3 on route2 = 6 total
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
	s := memory.New()

	until := time.Now().Add(30 * time.Second)
	require.NoError(t, s.MarkCooldown(ctx, "key-a", until))

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, cooled)
}

func TestIsCooledDown_UnknownKeyReturnsFalse(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	cooled, err := s.IsCooledDown(ctx, "key-never-cooled")
	require.NoError(t, err)
	assert.False(t, cooled)
}

func TestIsCooledDown_ExpiredCooldownReturnsFalse(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(50*time.Millisecond)))
	time.Sleep(100 * time.Millisecond)

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.False(t, cooled)
}

func TestMarkCooldown_LaterTimeWins(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(5*time.Second)))
	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(60*time.Second)))

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, cooled)
}

func TestMarkCooldown_EarlierTimeDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(60*time.Second)))
	require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(5*time.Second)))

	cooled, err := s.IsCooledDown(ctx, "key-a")
	require.NoError(t, err)
	assert.True(t, cooled)
}

func TestListUsage_ReturnsAllKeys(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-b", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-c", "route1", []time.Duration{time.Hour}))

	all, err := s.ListUsage(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	names := make(map[string]bool)
	for _, u := range all {
		names[u.KeyName] = true
	}
	assert.True(t, names["key-a"])
	assert.True(t, names["key-b"])
	assert.True(t, names["key-c"])
}

func TestListUsage_FiltersByRoute(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route2", []time.Duration{time.Hour}))
	require.NoError(t, s.IncreaseUsage(ctx, "key-b", "route2", []time.Duration{time.Hour}))

	results, err := s.ListUsage(ctx, "route2")
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, "route2", r.Route)
	}
}

func TestListUsage_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	all, err := s.ListUsage(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestGetUsage_SnapshotIncludesCooledUntil(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))
	until := time.Now().Add(30 * time.Second)
	require.NoError(t, s.MarkCooldown(ctx, "key-a", until))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.NotNil(t, usage.CooledUntil)
	assert.WithinDuration(t, until, *usage.CooledUntil, time.Second)
}

func TestGetUsage_NoCooldownHasNilCooledUntil(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", []time.Duration{time.Hour}))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	assert.Nil(t, usage.CooledUntil)
}

func TestGetUsage_WindowsSortedByDuration(t *testing.T) {
	ctx := context.Background()
	s := memory.New()

	windows := []time.Duration{24 * time.Hour, time.Hour, 7 * 24 * time.Hour}
	require.NoError(t, s.IncreaseUsage(ctx, "key-a", "route1", windows))

	usage, err := s.GetUsage(ctx, "key-a", "route1")
	require.NoError(t, err)
	require.Len(t, usage.Windows, 3)
	assert.Equal(t, time.Hour, usage.Windows[0].Window)
	assert.Equal(t, 24*time.Hour, usage.Windows[1].Window)
	assert.Equal(t, 7*24*time.Hour, usage.Windows[2].Window)
}
