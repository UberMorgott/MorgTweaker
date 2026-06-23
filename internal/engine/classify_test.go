package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"

	"morgtweaker/internal/core"
)

// TestClassifyAccessDeniedErrno: a raw ERROR_ACCESS_DENIED (errno 5) maps to a
// clean StatusBlocked with a friendly message, while the raw cause stays
// reachable via Unwrap for logging.
func TestClassifyAccessDeniedErrno(t *testing.T) {
	raw := syscall.Errno(5)
	st, mapped, ok := classifyApplyErr(raw)
	if !ok {
		t.Fatal("access-denied errno should be classified (ok=true)")
	}
	if st != core.StatusBlocked {
		t.Errorf("status = %v want StatusBlocked", st)
	}
	if mapped == nil || mapped.Error() == raw.Error() {
		t.Errorf("mapped error %q should be a friendly message, not the raw OS string", mapped)
	}
	if !errors.Is(mapped, raw) {
		t.Error("mapped error must Unwrap to the raw cause for logging")
	}
}

// TestClassifyWrappedAccessDenied: a wrapped access-denied error is still detected
// (errors.As reaches the inner syscall.Errno).
func TestClassifyWrappedAccessDenied(t *testing.T) {
	raw := fmt.Errorf("RegSetValueEx: %w", syscall.Errno(5))
	st, _, ok := classifyApplyErr(raw)
	if !ok || st != core.StatusBlocked {
		t.Errorf("wrapped access-denied -> (%v, ok=%v) want (Blocked, true)", st, ok)
	}
}

// TestClassifyPermissionSentinel: portable fs.ErrPermission also classifies.
func TestClassifyPermissionSentinel(t *testing.T) {
	if _, _, ok := classifyApplyErr(fs.ErrPermission); !ok {
		t.Error("fs.ErrPermission should classify as access-denied")
	}
}

// TestClassifyOtherErrorPassesThrough: a non-permission error is NOT classified,
// so the engine keeps its honest-status path and the original error.
func TestClassifyOtherErrorPassesThrough(t *testing.T) {
	other := errors.New("disk full")
	st, mapped, ok := classifyApplyErr(other)
	if ok {
		t.Error("a non-permission error must not be classified as Blocked")
	}
	if mapped != other {
		t.Errorf("unclassified error should pass through unchanged, got %v", mapped)
	}
	_ = st
}

// TestClassifyNil: nil error is not classified.
func TestClassifyNil(t *testing.T) {
	if _, _, ok := classifyApplyErr(nil); ok {
		t.Error("nil error must not be classified")
	}
}
