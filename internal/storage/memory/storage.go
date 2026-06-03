// Package memory provides an in-process implementation of storage.Storage.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// windowBucket is a single fixed-window counter. resetsAt is set when the
// window is first opened and the counter resets once the wall clock passes it.
type windowBucket struct {
	count    int64
	resetsAt time.Time
}

// routeUsage holds the per-window counters for one key on one route.
type routeUsage struct {
	windows  map[time.Duration]*windowBucket
	lastUsed time.Time
}

// Storage is an in-memory usage store. It is safe for concurrent use.
type Storage struct {
	mutex         sync.Mutex
	routeUsageMap map[string]map[string]*routeUsage // keyName -> route -> usage
	cooldowns     map[string]time.Time              // keyName -> cooled-until
}

// Compile-time check that Storage satisfies the storage.Storage contract.
var _ storage.Storage = (*Storage)(nil)

// New constructs an in-memory store.
func New() *Storage {
	return &Storage{
		routeUsageMap: make(map[string]map[string]*routeUsage),
		cooldowns:     make(map[string]time.Time),
	}
}

func (memoryStorage *Storage) IncreaseUsage(ctx context.Context, keyName, currentRoute string, windows []time.Duration) error {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()

	now := time.Now()
	routesForKey := memoryStorage.routeUsageMap[keyName]
	if routesForKey == nil {
		routesForKey = make(map[string]*routeUsage)
		memoryStorage.routeUsageMap[keyName] = routesForKey
	}
	routeUsageEntry := routesForKey[currentRoute]
	if routeUsageEntry == nil {
		routeUsageEntry = &routeUsage{windows: make(map[time.Duration]*windowBucket)}
		routesForKey[currentRoute] = routeUsageEntry
	}
	routeUsageEntry.lastUsed = now

	for _, window := range windows {
		bucket := routeUsageEntry.windows[window]
		if bucket == nil || !now.Before(bucket.resetsAt) {
			bucket = &windowBucket{resetsAt: now.Add(window)}
			routeUsageEntry.windows[window] = bucket
		}
		bucket.count++
	}
	return nil
}

func (memoryStorage *Storage) CheckLimits(ctx context.Context, keyName string, limits []keys.Limit) (bool, *keys.Limit, error) {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()

	now := time.Now()
	for index := range limits {
		limit := limits[index]
		if limit.MaxRequests == 0 { // 0 = unlimited
			continue
		}
		var count int64
		for _, routeUsageEntry := range memoryStorage.routeUsageMap[keyName] {
			if bucket := routeUsageEntry.windows[limit.Window]; bucket != nil && now.Before(bucket.resetsAt) {
				count += bucket.count
			}
		}
		if count >= limit.MaxRequests {
			exceeded := limit
			return false, &exceeded, nil
		}
	}
	return true, nil, nil
}

func (memoryStorage *Storage) MarkCooldown(ctx context.Context, keyName string, until time.Time) error {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()
	if existing, ok := memoryStorage.cooldowns[keyName]; !ok || until.After(existing) {
		memoryStorage.cooldowns[keyName] = until
	}
	return nil
}

func (memoryStorage *Storage) IsCooledDown(ctx context.Context, keyName string) (bool, error) {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()
	until, ok := memoryStorage.cooldowns[keyName]
	if !ok {
		return false, nil
	}
	if time.Now().Before(until) {
		return true, nil
	}
	delete(memoryStorage.cooldowns, keyName)
	return false, nil
}

func (memoryStorage *Storage) GetUsage(ctx context.Context, keyName, route string) (*storage.KeyUsage, error) {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()
	routesForKey := memoryStorage.routeUsageMap[keyName]
	if routesForKey == nil {
		return nil, storage.ErrKeyNotFound
	}
	routeUsageEntry := routesForKey[route]
	if routeUsageEntry == nil {
		return nil, storage.ErrKeyNotFound
	}
	return memoryStorage.snapshot(keyName, route, routeUsageEntry), nil
}

func (memoryStorage *Storage) ListUsage(ctx context.Context, route string) ([]storage.KeyUsage, error) {
	memoryStorage.mutex.Lock()
	defer memoryStorage.mutex.Unlock()
	var snapshots []storage.KeyUsage
	for keyName, routesForKey := range memoryStorage.routeUsageMap {
		for currentRoute, routeUsageEntry := range routesForKey {
			if route != "" && currentRoute != route {
				continue
			}
			snapshots = append(snapshots, *memoryStorage.snapshot(keyName, currentRoute, routeUsageEntry))
		}
	}
	return snapshots, nil
}

// snapshot builds a KeyUsage view of one route's usage. The caller must hold
// memoryStorage.mutex. Windows are sorted by duration for deterministic output.
func (memoryStorage *Storage) snapshot(keyName, route string, routeUsageEntry *routeUsage) *storage.KeyUsage {
	sortedWindows := make([]time.Duration, 0, len(routeUsageEntry.windows))
	for window := range routeUsageEntry.windows {
		sortedWindows = append(sortedWindows, window)
	}
	sort.Slice(sortedWindows, func(i, j int) bool { return sortedWindows[i] < sortedWindows[j] })

	windowUsages := make([]storage.WindowUsage, 0, len(sortedWindows))
	for _, window := range sortedWindows {
		bucket := routeUsageEntry.windows[window]
		windowUsages = append(windowUsages, storage.WindowUsage{
			Window:   window,
			Count:    bucket.count,
			ResetsAt: bucket.resetsAt,
		})
	}

	keyUsage := &storage.KeyUsage{
		KeyName:  keyName,
		Route:    route,
		Windows:  windowUsages,
		LastUsed: routeUsageEntry.lastUsed,
	}
	if until, ok := memoryStorage.cooldowns[keyName]; ok && time.Now().Before(until) {
		cooledUntil := until
		keyUsage.CooledUntil = &cooledUntil
	}
	return keyUsage
}
