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
	r := a.current()
	if r.vault == nil {
		st := VaultStateDTO{Locked: true, Error: "Не удалось открыть хранилище"}
		if errors.Is(r.err, store.ErrCorrupt) {
			st.Corrupt = true
			st.Error = "Файл хранилища повреждён или создан другим пользователем Windows"
		}
		return st
	}
	return VaultStateDTO{Locked: r.vault.Locked(), HasPassword: r.vault.HasPassword()}
}

// VaultUnlock unlocks a password vault, makes the typed store available and starts the
// autostart forwards.
func (a *App) VaultUnlock(password string) error {
	v := a.vaultRef()
	if v == nil {
		return errVaultMissing
	}
	pw := []byte(password)
	defer clear(pw)
	if err := v.Unlock(pw); err != nil {
		if errors.Is(err, store.ErrWrongPassword) {
			return errWrongPassword
		}
		return err
	}
	a.autostartOnce()
	return nil
}

// VaultLock locks a password vault after the idle timeout: the key and the decrypted data are
// wiped from memory until VaultUnlock. Open sessions and running forwards keep their own clients;
// a forward that has to redial meanwhile fails that attempt and retries.
func (a *App) VaultLock() error {
	r := a.current()
	if r.vault == nil {
		return errVaultMissing
	}
	if !r.vault.HasPassword() {
		return errNoPassword
	}
	return r.store.Lock() // the store lock keeps reads and saves off the data being wiped
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
	return a.vaultRef().SetPassword(pw)
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
	return a.vaultRef().RemovePassword()
}

// verifyPassword checks the password against the vault file with a separate
// probe, so a wrong value never touches the live vault. A vault without a
// password accepts any input.
func (a *App) verifyPassword(password string) error {
	if !a.vaultRef().HasPassword() {
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
	// The probe's key and decrypted copy of the data are wiped once the check is done.
	defer func() { _ = probe.Lock() }() //nolint:gosec // G104: a probe that never unlocked has nothing to wipe
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
	s := a.storeRef()
	return VaultCountsDTO{Keys: len(s.KeyMetas()), Hosts: len(s.Hosts())}
}
