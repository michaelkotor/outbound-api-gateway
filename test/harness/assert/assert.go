// Package assert holds the assertion helpers used by harness scenarios. Each
// helper takes a *testing.T and fails the scenario (t.Fatalf) with a clear
// "expected vs actual" message on mismatch, so the first broken assertion in a
// scenario aborts that scenario rather than cascading.
package assert

import (
	"strings"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
)

// THelper is the subset of testing.TB that the assert helpers require.
// *testing.T, *testing.B, and *testing.F all satisfy it, as does any test
// fake that implements Helper and Fatalf.
type THelper interface {
	Helper()
	Fatalf(format string, args ...any)
}

func fail(t THelper, format string, args ...any) {
	t.Helper()
	t.Fatalf(color.Red("FAIL")+" "+format, args...)
}

// Equal fails the scenario unless got == want.
func Equal[V comparable](t THelper, what string, got, want V) {
	t.Helper()
	if got != want {
		fail(t, "%s: expected %v, got %v", what, want, got)
	}
}

// Range fails the scenario unless lo <= got <= hi (inclusive).
func Range(t THelper, what string, got, lo, hi int) {
	t.Helper()
	if got < lo || got > hi {
		fail(t, "%s: expected within [%d, %d], got %d", what, lo, hi, got)
	}
}

// Contains fails the scenario unless haystack contains needle.
func Contains(t THelper, what, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		fail(t, "%s: expected to contain %q, got %q", what, needle, haystack)
	}
}

// True fails the scenario unless cond is true.
func True(t THelper, what string, cond bool) {
	t.Helper()
	if !cond {
		fail(t, "%s: expected true, got false", what)
	}
}

// NoError fails the scenario if err is non-nil.
func NoError(t THelper, what string, err error) {
	t.Helper()
	if err != nil {
		fail(t, "%s: unexpected error: %v", what, err)
	}
}
