// Package selectortest provides a configurable storage.Storage mock used by the
// roundrobin and leastused selector test suites. It is test-support code: it is
// never imported by production packages, so it does not ship in the binary.
package selectortest

import (
	"context"
	"sync"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// MockStore is a storage.Storage whose decision logic is injected via function
// fields so each selector case can drive cooldown / limit / usage behavior.
// MarkCooldown invocations are recorded in CooldownCalls for feedback assertions.
type MockStore struct {
	// CheckLimitsFn decides limit state per key. Returns ok and, when ok is
	// false, the breached limit. Nil means "always ok".
	CheckLimitsFn func(keyName string) (ok bool, exceeded *keys.Limit)
	// CooledFn reports whether a key is currently cooled. Nil means "never".
	CooledFn func(keyName string) bool
	// UsageFn returns the usage snapshot for a key/route, used by leastused to
	// compare window counts. Nil means an empty snapshot.
	UsageFn func(keyName, route string) *storage.KeyUsage

	mu            sync.Mutex
	CooldownCalls map[string]time.Time
}

// Compile-time check that MockStore satisfies the storage.Storage contract.
var _ storage.Storage = (*MockStore)(nil)

// New returns a MockStore with default permissive behavior: no limits breached,
// nothing cooled, empty usage.
func New() *MockStore {
	return &MockStore{CooldownCalls: make(map[string]time.Time)}
}

func (m *MockStore) IncreaseUsage(ctx context.Context, keyName, route string, windows []time.Duration) error {
	return nil
}

func (m *MockStore) CheckLimits(ctx context.Context, keyName string, limits []keys.Limit) (bool, *keys.Limit, error) {
	if m.CheckLimitsFn == nil {
		return true, nil, nil
	}
	ok, exceeded := m.CheckLimitsFn(keyName)
	return ok, exceeded, nil
}

func (m *MockStore) MarkCooldown(ctx context.Context, keyName string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CooldownCalls == nil {
		m.CooldownCalls = make(map[string]time.Time)
	}
	m.CooldownCalls[keyName] = until
	return nil
}

func (m *MockStore) IsCooledDown(ctx context.Context, keyName string) (bool, error) {
	if m.CooledFn == nil {
		return false, nil
	}
	return m.CooledFn(keyName), nil
}

func (m *MockStore) GetUsage(ctx context.Context, keyName, route string) (*storage.KeyUsage, error) {
	if m.UsageFn == nil {
		return &storage.KeyUsage{KeyName: keyName, Route: route}, nil
	}
	return m.UsageFn(keyName, route), nil
}

func (m *MockStore) ListUsage(ctx context.Context, route string) ([]storage.KeyUsage, error) {
	return nil, nil
}

// Cooldown returns the recorded cooldown time for keyName and whether it was set.
func (m *MockStore) Cooldown(keyName string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.CooldownCalls[keyName]
	return until, ok
}

// CooldownCount returns how many distinct keys have a recorded cooldown.
func (m *MockStore) CooldownCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.CooldownCalls)
}
