package app

import (
	"errors"
	"slices"

	"sshclient/internal/model"
)

// The choices the Settings screen offers; SaveSettings refuses anything else.
var (
	terminalFonts = []string{"Cascadia Mono", "JetBrains Mono", "Consolas"}
	lockMinutes   = []int{0, 5, 15, 30}
)

const (
	minTerminalSize = 10
	maxTerminalSize = 20
)

var errBadSettings = errors.New("invalid settings")

// GetSettings returns the user settings, defaults while the vault is locked.
func (a *App) GetSettings() model.Settings {
	if !a.storeUnlocked() {
		return model.DefaultSettings()
	}
	return a.store.Settings()
}

// SaveSettings stores the user settings. The font comes from the fixed list, the size is
// 10–20, the idle lock is 0 (off), 5, 15 or 30 minutes; the window geometry is kept as stored.
func (a *App) SaveSettings(s model.Settings) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if !slices.Contains(terminalFonts, s.TerminalFont) || s.TerminalSize < minTerminalSize ||
		s.TerminalSize > maxTerminalSize || !slices.Contains(lockMinutes, s.LockAfterMin) {
		return errBadSettings
	}
	s.Window = a.store.Settings().Window // the geometry is Go's: the window saves it on close
	return a.store.SaveSettings(s)
}

// Version returns the release version set at build time, "dev" in local builds.
func (a *App) Version() string { return a.version }
