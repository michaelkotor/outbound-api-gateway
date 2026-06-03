// Package redis provides a Redis-backed implementation of storage.Storage.
package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// Key namespaces. Counters are fixed-window: the TTL is set only when a window
// is first opened so the reset time does not slide on every increment.
const (
	indexKey    = "gw:idx"        // SET of "<keyName>\x00<route>" members
	counterFmt  = "gw:c:%s:%s:%d" // keyName, route, windowNanos
	windowFmt   = "gw:w:%s:%s"    // SET of window-nanos strings for keyName/route
	routesFmt   = "gw:kr:%s"      // SET of routes a keyName has usage on
	lastUseFmt  = "gw:lu:%s:%s"   // last-used unix-nanos for keyName/route
	cooldownFmt = "gw:cd:%s"      // cooldown marker for keyName (TTL-bounded)

	memberSeparator = "\x00"
)

// Storage is a Redis-backed usage store. It is safe for concurrent use.
type Storage struct {
	client *goredis.Client
}

// Compile-time check that Storage satisfies the storage.Storage contract.
var _ storage.Storage = (*Storage)(nil)

// New constructs a Redis-backed store from a connection URL or a bare
// "host:port" address. It verifies connectivity with a PING.
func New(redisURL string) (*Storage, error) {
	var clientOptions *goredis.Options
	if parsedOptions, parseErr := goredis.ParseURL(redisURL); parseErr == nil {
		clientOptions = parsedOptions
	} else {
		clientOptions = &goredis.Options{Addr: redisURL}
	}

	client := goredis.NewClient(clientOptions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: connecting to %q: %w", redisURL, err)
	}
	return &Storage{client: client}, nil
}

func counterRedisKey(keyName, route string, windowNanos int64) string {
	return fmt.Sprintf(counterFmt, keyName, route, windowNanos)
}

func windowSetRedisKey(keyName, route string) string { return fmt.Sprintf(windowFmt, keyName, route) }
func routesRedisKey(keyName string) string           { return fmt.Sprintf(routesFmt, keyName) }
func lastUseRedisKey(keyName, route string) string   { return fmt.Sprintf(lastUseFmt, keyName, route) }
func cooldownRedisKey(keyName string) string         { return fmt.Sprintf(cooldownFmt, keyName) }
func indexMember(keyName, route string) string       { return keyName + memberSeparator + route }

func (redisStorage *Storage) IncreaseUsage(ctx context.Context, keyName, route string, windows []time.Duration) error {
	now := time.Now()
	if err := redisStorage.client.SAdd(ctx, indexKey, indexMember(keyName, route)).Err(); err != nil {
		return err
	}
	if err := redisStorage.client.SAdd(ctx, routesRedisKey(keyName), route).Err(); err != nil {
		return err
	}
	if err := redisStorage.client.Set(ctx, lastUseRedisKey(keyName, route), strconv.FormatInt(now.UnixNano(), 10), 0).Err(); err != nil {
		return err
	}

	for _, window := range windows {
		windowNanos := int64(window)
		if err := redisStorage.client.SAdd(ctx, windowSetRedisKey(keyName, route), strconv.FormatInt(windowNanos, 10)).Err(); err != nil {
			return err
		}
		counterKey := counterRedisKey(keyName, route, windowNanos)
		count, err := redisStorage.client.Incr(ctx, counterKey).Result()
		if err != nil {
			return err
		}
		// Open the window on the first increment: set the TTL so the counter
		// resets exactly one window later regardless of later increments.
		if count == 1 {
			if err := redisStorage.client.PExpire(ctx, counterKey, window).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (redisStorage *Storage) CheckLimits(ctx context.Context, keyName string, limits []keys.Limit) (bool, *keys.Limit, error) {
	routes, err := redisStorage.client.SMembers(ctx, routesRedisKey(keyName)).Result()
	if err != nil {
		return false, nil, err
	}

	for index := range limits {
		limit := limits[index]
		if limit.MaxRequests == 0 { // 0 = unlimited
			continue
		}
		var totalCount int64
		windowNanos := int64(limit.Window)
		for _, route := range routes {
			rawCount, err := redisStorage.client.Get(ctx, counterRedisKey(keyName, route, windowNanos)).Result()
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if err != nil {
				return false, nil, err
			}
			count, parseErr := strconv.ParseInt(rawCount, 10, 64)
			if parseErr != nil {
				return false, nil, parseErr
			}
			totalCount += count
		}
		if totalCount >= limit.MaxRequests {
			exceeded := limit
			return false, &exceeded, nil
		}
	}
	return true, nil, nil
}

func (redisStorage *Storage) MarkCooldown(ctx context.Context, keyName string, until time.Time) error {
	cooldownDuration := time.Until(until)
	if cooldownDuration <= 0 {
		return nil
	}
	// Keep the later of any existing cooldown and the new one.
	if existingTTL, err := redisStorage.client.PTTL(ctx, cooldownRedisKey(keyName)).Result(); err == nil && existingTTL > 0 && existingTTL >= cooldownDuration {
		return nil
	}
	return redisStorage.client.Set(ctx, cooldownRedisKey(keyName), "1", cooldownDuration).Err()
}

func (redisStorage *Storage) IsCooledDown(ctx context.Context, keyName string) (bool, error) {
	existingCount, err := redisStorage.client.Exists(ctx, cooldownRedisKey(keyName)).Result()
	if err != nil {
		return false, err
	}
	return existingCount > 0, nil
}

func (redisStorage *Storage) GetUsage(ctx context.Context, keyName, route string) (*storage.KeyUsage, error) {
	keyUsage, found, err := redisStorage.snapshot(ctx, keyName, route)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, storage.ErrKeyNotFound
	}
	return keyUsage, nil
}

func (redisStorage *Storage) ListUsage(ctx context.Context, route string) ([]storage.KeyUsage, error) {
	members, err := redisStorage.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	var snapshots []storage.KeyUsage
	for _, member := range members {
		memberParts := strings.SplitN(member, memberSeparator, 2)
		if len(memberParts) != 2 {
			continue
		}
		keyName, currentRoute := memberParts[0], memberParts[1]
		if route != "" && currentRoute != route {
			continue
		}
		keyUsage, found, err := redisStorage.snapshot(ctx, keyName, currentRoute)
		if err != nil {
			return nil, err
		}
		if found {
			snapshots = append(snapshots, *keyUsage)
		}
	}
	return snapshots, nil
}

// snapshot reads one key/route usage view. found is false when no usage has
// ever been recorded for the pair.
func (redisStorage *Storage) snapshot(ctx context.Context, keyName, route string) (*storage.KeyUsage, bool, error) {
	windowMembers, err := redisStorage.client.SMembers(ctx, windowSetRedisKey(keyName, route)).Result()
	if err != nil {
		return nil, false, err
	}
	if len(windowMembers) == 0 {
		return nil, false, nil
	}

	sortedWindowNanos := make([]int64, 0, len(windowMembers))
	for _, member := range windowMembers {
		windowNanos, parseErr := strconv.ParseInt(member, 10, 64)
		if parseErr != nil {
			return nil, false, parseErr
		}
		sortedWindowNanos = append(sortedWindowNanos, windowNanos)
	}
	sort.Slice(sortedWindowNanos, func(i, j int) bool { return sortedWindowNanos[i] < sortedWindowNanos[j] })

	now := time.Now()
	windowUsages := make([]storage.WindowUsage, 0, len(sortedWindowNanos))
	for _, windowNanos := range sortedWindowNanos {
		counterKey := counterRedisKey(keyName, route, windowNanos)
		count := int64(0)
		resetsAt := now.Add(time.Duration(windowNanos))
		rawCount, err := redisStorage.client.Get(ctx, counterKey).Result()
		if err == nil {
			if count, err = strconv.ParseInt(rawCount, 10, 64); err != nil {
				return nil, false, err
			}
			if remainingTTL, ttlErr := redisStorage.client.PTTL(ctx, counterKey).Result(); ttlErr == nil && remainingTTL > 0 {
				resetsAt = now.Add(remainingTTL)
			}
		} else if !errors.Is(err, goredis.Nil) {
			return nil, false, err
		}
		windowUsages = append(windowUsages, storage.WindowUsage{
			Window:   time.Duration(windowNanos),
			Count:    count,
			ResetsAt: resetsAt,
		})
	}

	keyUsage := &storage.KeyUsage{KeyName: keyName, Route: route, Windows: windowUsages}
	if lastUsedRaw, err := redisStorage.client.Get(ctx, lastUseRedisKey(keyName, route)).Result(); err == nil {
		if lastUsedNanos, parseErr := strconv.ParseInt(lastUsedRaw, 10, 64); parseErr == nil {
			keyUsage.LastUsed = time.Unix(0, lastUsedNanos)
		}
	}
	if remainingTTL, err := redisStorage.client.PTTL(ctx, cooldownRedisKey(keyName)).Result(); err == nil && remainingTTL > 0 {
		cooledUntil := now.Add(remainingTTL)
		keyUsage.CooledUntil = &cooledUntil
	}
	return keyUsage, true, nil
}
