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
	errNoPassword    = errors.New("Хранилище не защищено паролем")
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

// VaultUnlock unlocks a password vault, makes the typed store available and starts the
// autostart forwards.
func (a *App) VaultUnlock(password string) error {
	if a.vault == nil {
		return errVaultMissing
	}
	pw := []byte(password)
	defer clear(pw)
	if err := a.vault.Unlock(pw); err != nil {
		if errors.Is(err, store.ErrWrongPassword) {
			return errWrongPassword
		}
		return err
	}
	if a.store == nil {
		a.store = store.NewStore(a.vault)
	}
	a.autostartOnce()
	return nil
}

// VaultLock locks a password vault after the idle timeout: the key and the decrypted data are
// wiped from memory until VaultUnlock. Open sessions and running forwards keep their own clients;
// a forward that has to redial meanwhile fails that attempt and retries.
func (a *App) VaultLock() error {
	if a.vault == nil {
		return errVaultMissing
	}
	if !a.vault.HasPassword() {
		return errNoPassword
	}
	if a.store == nil {
		return a.vault.Lock()
	}
	return a.store.Lock()
}

// VaultSetPassword protects the vault with a password.
func (a *App) VaultSetPassword(password string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if utf8.RuneCountInString(password) < minPasswordLen {
		return errPasswordShort
	}
	pw := []byte(password)
	defer clear(pw)
	return a.vault.SetPassword(pw)
}

// VaultRemovePassword verifies the password against the file and returns the
// vault to DPAPI protection.
func (a *App) VaultRemovePassword(password string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if err := a.verifyPassword(password); err != nil {
		return err
	}
	return a.vault.RemovePassword()
}

// verifyPassword checks the password against the vault file with a separate
// probe, so a wrong value never touches the live vault. A vault without a
// password accepts any input.
func (a *App) verifyPassword(password string) error {
	if !a.vault.HasPassword() {
		return nil
	}
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	probe, err := store.OpenVault(filepath.Join(dir, vaultFile))
	if err != nil {
		return err
	}
	pw := []byte(password)
	defer clear(pw)
	if err := probe.Unlock(pw); err != nil {
		if errors.Is(err, store.ErrWrongPassword) {
			return errWrongPassword
		}
		return err
	}
	return nil
}

// VaultCounts returns how many keys and hosts are stored.
func (a *App) VaultCounts() VaultCountsDTO {
	if !a.storeUnlocked() {
		return VaultCountsDTO{}
	}
	return VaultCountsDTO{Keys: len(a.store.Keys()), Hosts: len(a.store.Hosts())}
}
