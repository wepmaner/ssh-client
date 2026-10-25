package appdir

// ResetFallback forgets a failed migration between tests.
func ResetFallback() { fallback.Store(nil) }
