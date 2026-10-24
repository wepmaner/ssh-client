package app

import "sshclient/internal/model"

// GetSettings returns the user settings, defaults while the vault is locked.
func (a *App) GetSettings() model.Settings {
	if !a.storeUnlocked() {
		return model.DefaultSettings()
	}
	return a.store.Settings()
}

// SaveSettings stores the user settings.
func (a *App) SaveSettings(s model.Settings) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	return a.store.SaveSettings(s)
}
