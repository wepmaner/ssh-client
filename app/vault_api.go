package app

import (
	"errors"
	"path/filepath"
	"unicode/utf8"

	"sshclient/internal/appdir"
	"sshclient/internal/store"
)

const minPasswordLen = 8

// User-facing messages shown verbatim by the UI.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errVaultLocked   = errors.New("Хранилище заблокировано")
	errVaultMissing  = errors.New("Хранилище недоступно")
	errWrongPassword = errors.New("Неверный пароль")
	errPasswordShort = errors.New("Пароль короче 8 символов")
)

// VaultStateDTO tells the UI which screen to show.
type VaultStateDTO struct {
	Locked      bool   `json:"locked"`
	HasPassword bool   `json:"has_password"`
	Corrupt     bool   `json:"corrupt"`
	Error       string `json:"error"`
}

// VaultCountsDTO is the amount of records an encryption will cover.
type VaultCountsDTO struct {
	Keys  int `json:"keys"`
	Hosts int `json:"hosts"`
}

// VaultState reports the vault state for the startup screen choice.
func (a *App) VaultState() VaultStateDTO {
	if a.vault == nil {
		st := VaultStateDTO{Locked: true, Error: "Не удалось открыть хранилище"}
		if errors.Is(a.vaultErr, store.ErrCorrupt) {
			st.Corrupt = true
			st.Error = "Файл хранилища повреждён или создан другим пользователем Windows"
		}
		return st
	}
	return VaultStateDTO{Locked: a.vault.Locked(), HasPassword: a.vault.HasPassword()}
}

// VaultUnlock unlocks a password vault and makes the typed store available.
func (a *App) VaultUnlock(password string) error {
	if a.vault == nil {
		return errVaultMissing
	}
	if err := a.vault.Unlock([]byte(password)); err != nil {
		if errors.Is(err, store.ErrWrongPassword) {
			return errWrongPassword
		}
		return err
	}
	if a.store == nil {
		a.store = store.NewStore(a.vault)
	}
	return nil
}

// VaultSetPassword protects the vault with a password.
func (a *App) VaultSetPassword(password string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if utf8.RuneCountInString(password) < minPasswordLen {
		return errPasswordShort
	}
	return a.vault.SetPassword([]byte(password))
}

// VaultRemovePassword verifies the password against the file and returns the
// vault to DPAPI protection.
func (a *App) VaultRemovePassword(password string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	probe, err := store.OpenVault(filepath.Join(dir, vaultFile))
	if err != nil {
		return err
	}
	if err := probe.Unlock([]byte(password)); err != nil {
		if errors.Is(err, store.ErrWrongPassword) {
			return errWrongPassword
		}
		return err
	}
	return a.vault.RemovePassword()
}

// VaultCounts returns how many keys and hosts are stored.
func (a *App) VaultCounts() VaultCountsDTO {
	if !a.storeUnlocked() {
		return VaultCountsDTO{}
	}
	return VaultCountsDTO{Keys: len(a.store.Keys()), Hosts: len(a.store.Hosts())}
}
