//go:build !windows

package app

// dropForward is the main window's drop target; only Windows needs one (dropforward_windows.go).
type dropForward struct{}

func (a *App) setupDrops() {}
