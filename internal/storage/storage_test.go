package storage_test

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
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/memory"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/redis"
)

// adapters is the list of store implementations every case runs against.
var adapters = []string{"memory", "redis"}

// newTestStore builds a fresh store for the given adapter. For the redis
// adapter it also returns the backing miniredis handle so cooldown-expiry
// cases can advance time via FastForward; for memory it is nil.
func newTestStore(t *testing.T, adapter string) (storage.Storage, *miniredis.Miniredis) {
	t.Helper()
	switch adapter {
	case "memory":
		return memory.New(), nil
	case "redis":
		mr := miniredis.RunT(t)
		s, err := redis.New(mr.Addr())
		require.NoError(t, err)
		return s, mr
	default:
		t.Fatalf("unknown adapter %q", adapter)
		return nil, nil
	}
}

func TestIncrUsage(t *testing.T) {
	ctx := context.Background()

	t.Run("single_key_single_window", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				before := time.Now()
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))

				usage, err := s.GetUsage(ctx, "key-a", "api")
				require.NoError(t, err)
				require.Len(t, usage.Windows, 1)
				assert.Equal(t, int64(3), usage.Windows[0].Count)
				assert.Equal(t, time.Hour, usage.Windows[0].Window)

				expected := before.Add(time.Hour)
				assert.WithinDuration(t, expected, usage.Windows[0].ResetsAt, 2*time.Second)
			})
		}
	})

	t.Run("multiple_windows_incremented_together", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				windows := []time.Duration{time.Hour, 24 * time.Hour}
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", windows))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", windows))

				usage, err := s.GetUsage(ctx, "key-a", "api")
				require.NoError(t, err)
				require.Len(t, usage.Windows, 2)

				counts := windowCounts(usage)
				assert.Equal(t, int64(2), counts[time.Hour])
				assert.Equal(t, int64(2), counts[24*time.Hour])
			})
		}
	})

	t.Run("separate_keys_independent", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-b", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-b", "api", []time.Duration{time.Hour}))

				usageA, err := s.GetUsage(ctx, "key-a", "api")
				require.NoError(t, err)
				usageB, err := s.GetUsage(ctx, "key-b", "api")
				require.NoError(t, err)

				assert.Equal(t, int64(1), usageA.Windows[0].Count)
				assert.Equal(t, int64(2), usageB.Windows[0].Count)
			})
		}
	})

	t.Run("separate_routes_independent", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api2", []time.Duration{time.Hour}))

				usageAPI, err := s.GetUsage(ctx, "key-a", "api")
				require.NoError(t, err)
				usageAPI2, err := s.GetUsage(ctx, "key-a", "api2")
				require.NoError(t, err)

				assert.Equal(t, int64(1), usageAPI.Windows[0].Count)
				assert.Equal(t, int64(1), usageAPI2.Windows[0].Count)
			})
		}
	})

	t.Run("concurrent_increments_no_loss", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				var wg sync.WaitGroup
				for i := 0; i < 50; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for j := 0; j < 20; j++ {
							_ = s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour})
						}
					}()
				}
				wg.Wait()

				usage, err := s.GetUsage(ctx, "key-a", "api")
				require.NoError(t, err)
				assert.Equal(t, int64(1000), usage.Windows[0].Count)
			})
		}
	})

	t.Run("get_usage_unknown_key", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				_, err := s.GetUsage(ctx, "nonexistent", "api")
				assert.ErrorIs(t, err, storage.ErrKeyNotFound)
			})
		}
	})
}

func TestCheckLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("under_limit_returns_ok", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))

				ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 10}})
				require.NoError(t, err)
				assert.True(t, ok)
				assert.Nil(t, exceeded)
			})
		}
	})

	t.Run("at_limit_returns_exceeded", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				for i := 0; i < 10; i++ {
					require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				}

				ok, exceeded, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 10}})
				require.NoError(t, err)
				assert.False(t, ok)
				require.NotNil(t, exceeded)
				assert.Equal(t, time.Hour, exceeded.Window)
				assert.Equal(t, int64(10), exceeded.MaxRequests)
			})
		}
	})

	t.Run("zero_limit_means_unlimited", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				for i := 0; i < 1000; i++ {
					require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				}

				ok, _, err := s.CheckLimits(ctx, "key-a", []keys.Limit{{Window: time.Hour, MaxRequests: 0}})
				require.NoError(t, err)
				assert.True(t, ok)
			})
		}
	})

	t.Run("first_exceeded_window_returned", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				for i := 0; i < 5; i++ {
					require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour, 24 * time.Hour}))
				}

				limits := []keys.Limit{
					{Window: time.Hour, MaxRequests: 5},
					{Window: 24 * time.Hour, MaxRequests: 100},
				}
				ok, exceeded, err := s.CheckLimits(ctx, "key-a", limits)
				require.NoError(t, err)
				assert.False(t, ok)
				require.NotNil(t, exceeded)
				assert.Equal(t, time.Hour, exceeded.Window)
			})
		}
	})

	t.Run("key_with_no_usage_is_under_limit", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				ok, exceeded, err := s.CheckLimits(ctx, "key-never-used", []keys.Limit{{Window: time.Hour, MaxRequests: 10}})
				require.NoError(t, err)
				assert.True(t, ok)
				assert.Nil(t, exceeded)
			})
		}
	})
}

func TestCooldown(t *testing.T) {
	ctx := context.Background()

	t.Run("mark_then_check", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				until := time.Now().Add(30 * time.Second)
				require.NoError(t, s.MarkCooldown(ctx, "key-a", until))

				cooled, err := s.IsCooledDown(ctx, "key-a")
				require.NoError(t, err)
				assert.True(t, cooled)
			})
		}
	})

	t.Run("expired_cooldown_returns_false", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, mr := newTestStore(t, adapter)
				until := time.Now().Add(100 * time.Millisecond)
				require.NoError(t, s.MarkCooldown(ctx, "key-a", until))

				// Advance past the cooldown: real sleep for memory, simulated
				// clock for redis via miniredis FastForward.
				if mr != nil {
					mr.FastForward(200 * time.Millisecond)
				} else {
					time.Sleep(200 * time.Millisecond)
				}

				cooled, err := s.IsCooledDown(ctx, "key-a")
				require.NoError(t, err)
				assert.False(t, cooled)
			})
		}
	})

	t.Run("uncooled_key_returns_false", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				cooled, err := s.IsCooledDown(ctx, "key-never-cooled")
				require.NoError(t, err)
				assert.False(t, cooled)
			})
		}
	})

	t.Run("overwrite_cooldown_with_later_time", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, mr := newTestStore(t, adapter)
				require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(10*time.Second)))
				require.NoError(t, s.MarkCooldown(ctx, "key-a", time.Now().Add(60*time.Second)))

				// 10s later (simulated): still cooled because of the 60s mark.
				if mr != nil {
					mr.FastForward(11 * time.Second)
				}
				cooled, err := s.IsCooledDown(ctx, "key-a")
				require.NoError(t, err)
				assert.True(t, cooled)
			})
		}
	})
}

func TestListUsage(t *testing.T) {
	ctx := context.Background()

	t.Run("returns_all_keys", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-b", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-c", "api", []time.Duration{time.Hour}))

				all, err := s.ListUsage(ctx, "")
				require.NoError(t, err)
				assert.Len(t, all, 3)

				names := make([]string, 0, len(all))
				for _, u := range all {
					names = append(names, u.KeyName)
				}
				assert.Contains(t, names, "key-a")
				assert.Contains(t, names, "key-b")
				assert.Contains(t, names, "key-c")
			})
		}
	})

	t.Run("filters_by_route", func(t *testing.T) {
		for _, adapter := range adapters {
			t.Run(adapter, func(t *testing.T) {
				s, _ := newTestStore(t, adapter)
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api", []time.Duration{time.Hour}))
				require.NoError(t, s.IncreaseUsage(ctx, "key-a", "api2", []time.Duration{time.Hour}))

				results, err := s.ListUsage(ctx, "api2")
				require.NoError(t, err)
				require.Len(t, results, 1)
				assert.Equal(t, "api2", results[0].Route)
			})
		}
	})
}

// windowCounts indexes a usage snapshot's windows by duration for assertions
// that do not depend on window ordering.
func windowCounts(u *storage.KeyUsage) map[time.Duration]int64 {
	out := make(map[time.Duration]int64, len(u.Windows))
	for _, w := range u.Windows {
		out[w.Window] = w.Count
	}
	return out
}
