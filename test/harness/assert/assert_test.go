package assert_test

import (
	"errors"
	"testing"

	harnessassert "github.com/michaelkotor/outbound-api-gateway/test/harness/assert"
)

func TestEqual_PassesWhenEqual(t *testing.T) {
	mockT := &testing.T{}
	harnessassert.Equal(mockT, "value", 42, 42)
	// If Equal called t.Fatalf, mockT would be marked as failed.
	if mockT.Failed() {
		t.Error("Equal should not fail when values are equal")
	}
}

func TestEqual_FailsWhenNotEqual(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Equal(mockT, "value", 42, 99)
	if !mockT.failed {
		t.Error("Equal should fail when values are not equal")
	}
}

func TestRange_PassesWhenInRange(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Range(mockT, "count", 5, 1, 10)
	if mockT.failed {
		t.Error("Range should pass when value is in range")
	}
}

func TestRange_PassesAtBoundaries(t *testing.T) {
	lo := &fakeT{}
	harnessassert.Range(lo, "lo", 1, 1, 10)
	if lo.failed {
		t.Error("Range should pass at lower boundary")
	}

	hi := &fakeT{}
	harnessassert.Range(hi, "hi", 10, 1, 10)
	if hi.failed {
		t.Error("Range should pass at upper boundary")
	}
}

func TestRange_FailsBelowRange(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Range(mockT, "count", 0, 1, 10)
	if !mockT.failed {
		t.Error("Range should fail when value is below range")
	}
}

func TestRange_FailsAboveRange(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Range(mockT, "count", 11, 1, 10)
	if !mockT.failed {
		t.Error("Range should fail when value is above range")
	}
}

func TestContains_PassesWhenContained(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Contains(mockT, "body", "hello world", "world")
	if mockT.failed {
		t.Error("Contains should pass when haystack contains needle")
	}
}

func TestContains_FailsWhenNotContained(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.Contains(mockT, "body", "hello world", "missing")
	if !mockT.failed {
		t.Error("Contains should fail when haystack does not contain needle")
	}
}

func TestTrue_PassesWhenTrue(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.True(mockT, "flag", true)
	if mockT.failed {
		t.Error("True should pass when condition is true")
	}
}

func TestTrue_FailsWhenFalse(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.True(mockT, "flag", false)
	if !mockT.failed {
		t.Error("True should fail when condition is false")
	}
}

func TestNoError_PassesWhenNil(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.NoError(mockT, "op", nil)
	if mockT.failed {
		t.Error("NoError should pass when error is nil")
	}
}

func TestNoError_FailsWhenNonNil(t *testing.T) {
	mockT := &fakeT{}
	harnessassert.NoError(mockT, "op", errors.New("something went wrong"))
	if !mockT.failed {
		t.Error("NoError should fail when error is non-nil")
	}
}

// fakeT is a minimal stand-in for *testing.T that records Fatalf calls.
type fakeT struct {
	failed bool
}

func (f *fakeT) Helper()                   {}
func (f *fakeT) Fatalf(_ string, _ ...any) { f.failed = true }
