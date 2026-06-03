package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// errKeyNotFound is returned by admin operations targeting an unknown key.
var errKeyNotFound = errors.New("key not found")

// KeyState is the mutable per-key counter state. All mutations happen under mu.
type KeyState struct {
	mu          sync.Mutex
	Name        string
	Fingerprint string
	RequestN    int64         // monotonic, never reset
	WindowCount int64         // resets each window
	WindowStart time.Time     // start of current window
	Limit       int64         // current limit, mutable via admin (0 = unlimited)
	Window      time.Duration // current window duration, mutable via admin
	CooledUntil *time.Time    // present for spec parity; mock has no cooldown logic
}

// Decision is an immutable snapshot of a key's state after an Allow call.
type Decision struct {
	Allowed     bool
	KeyName     string
	RequestN    int64
	WindowCount int64
	Limit       int64
	Window      time.Duration
	ResetsAt    time.Time
}

// Limiter holds per-key state and enforces windowed rate limits exactly.
type Limiter struct {
	states map[string]*KeyState
	names  []string // sorted for deterministic, deadlock-free iteration
}

// newLimiter seeds limiter state from the registry using the default limit and
// window for every key.
func newLimiter(reg *Registry, defaultLimit int64, window time.Duration) *Limiter {
	l := &Limiter{states: make(map[string]*KeyState)}
	now := time.Now()
	for _, name := range reg.names {
		rec := reg.byName[name]
		l.states[name] = &KeyState{
			Name:        name,
			Fingerprint: rec.Fingerprint,
			WindowStart: now,
			Limit:       defaultLimit,
			Window:      window,
		}
		l.names = append(l.names, name)
	}
	sort.Strings(l.names)
	return l
}

// Allow performs an atomic window-roll, limit-check, and increment for one key.
// The limit check and increment happen together under the key's mutex so the
// mock never overshoots its own limit.
func (l *Limiter) Allow(name string) Decision {
	st := l.states[name]
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	if now.Sub(st.WindowStart) >= st.Window {
		st.WindowCount = 0
		st.WindowStart = now
	}

	resetsAt := st.WindowStart.Add(st.Window)

	// Limit 0 means unlimited.
	if st.Limit > 0 && st.WindowCount >= st.Limit {
		return Decision{
			Allowed:     false,
			KeyName:     st.Name,
			RequestN:    st.RequestN,
			WindowCount: st.WindowCount,
			Limit:       st.Limit,
			Window:      st.Window,
			ResetsAt:    resetsAt,
		}
	}

	st.RequestN++
	st.WindowCount++
	return Decision{
		Allowed:     true,
		KeyName:     st.Name,
		RequestN:    st.RequestN,
		WindowCount: st.WindowCount,
		Limit:       st.Limit,
		Window:      st.Window,
		ResetsAt:    resetsAt,
	}
}

// Reset zeroes every key's window counter and clears cooldown, preserving the
// monotonic RequestN. Keys are processed in alphabetical order. Returns the
// number of keys reset.
func (l *Limiter) Reset() int {
	now := time.Now()
	for _, name := range l.names {
		st := l.states[name]
		st.mu.Lock()
		st.WindowCount = 0
		st.WindowStart = now
		st.CooledUntil = nil
		st.mu.Unlock()
	}
	return len(l.names)
}

// SetLimit overrides a key's limit and, if window is non-nil, its window.
// Returns errKeyNotFound if the key does not exist.
func (l *Limiter) SetLimit(name string, limit int64, window *time.Duration) error {
	st, ok := l.states[name]
	if !ok {
		return errKeyNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Limit = limit
	if window != nil {
		st.Window = *window
	}
	return nil
}

// StateSnapshot is an immutable view of one key for the admin/state endpoint.
type StateSnapshot struct {
	Name        string
	Fingerprint string
	RequestN    int64
	WindowCount int64
	Limit       int64
	Window      time.Duration
	CooledUntil *time.Time
	ResetsAt    time.Time
}

// Snapshot returns the current state of every key in alphabetical order.
func (l *Limiter) Snapshot() []StateSnapshot {
	out := make([]StateSnapshot, 0, len(l.names))
	for _, name := range l.names {
		st := l.states[name]
		st.mu.Lock()
		out = append(out, StateSnapshot{
			Name:        st.Name,
			Fingerprint: st.Fingerprint,
			RequestN:    st.RequestN,
			WindowCount: st.WindowCount,
			Limit:       st.Limit,
			Window:      st.Window,
			CooledUntil: st.CooledUntil,
			ResetsAt:    st.WindowStart.Add(st.Window),
		})
		st.mu.Unlock()
	}
	return out
}
