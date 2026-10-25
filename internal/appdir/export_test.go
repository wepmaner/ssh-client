package appdir

import (
	"testing"
	"time"
)

// ResetFallback forgets a failed migration between tests.
func ResetFallback() { fallback.Store(nil) }

// SetLockWait shortens the wait for another start's lock in one test.
func SetLockWait(t *testing.T, d time.Duration) {
	old := lockWait
	lockWait = d
	t.Cleanup(func() { lockWait = old })
}
