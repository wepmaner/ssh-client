package app

import (
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

// GetSettings returns the user settings, defaults while the vault is locked.
func (a *App) GetSettings() model.Settings {
	if !a.storeUnlocked() {
		return model.DefaultSettings()
	}
	set := a.storeRef().Settings()
	set.MCPToken = "" // the token is shown only by the MCP settings screen
	return set
}

// SaveSettings stores the user settings. The font comes from the fixed list, the size is
// 10–20, the idle lock is 0 (off), 5, 15 or 30 minutes; the window geometry is kept as stored.
// A refusal is a field error (field:font|size|lock: text).
func (a *App) SaveSettings(s model.Settings) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	switch {
	case !slices.Contains(terminalFonts, s.TerminalFont):
		return fieldError("font", "Выберите шрифт из списка")
	case s.TerminalSize < minTerminalSize || s.TerminalSize > maxTerminalSize:
		return fieldError("size", "Размер от 10 до 20")
	case !slices.Contains(lockMinutes, s.LockAfterMin):
		return fieldError("lock", "Блокировка через 5, 15 или 30 минут или никогда")
	}
	cur := a.storeRef().Settings()
	s.Window = cur.Window // the geometry is Go's: the window saves it on close
	s.MCPEnabled, s.MCPPort, s.MCPToken = cur.MCPEnabled, cur.MCPPort, cur.MCPToken // changed only by the MCP API
	return a.storeRef().SaveSettings(s)
}

// Version returns the release version set at build time, "dev" in local builds.
func (a *App) Version() string { return a.version }
