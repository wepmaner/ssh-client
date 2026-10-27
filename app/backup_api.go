package app

import (
	"errors"
	"log"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/appdir"
	"sshclient/internal/store"
)

// User-facing messages of the backup API, shown verbatim by the UI.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errNotBackup = errors.New("Это не резервная копия sshclient")
	errReadCopy  = errors.New("Не удалось прочитать файл копии")
)

const backupExt = ".vault"

var backupFilters = []application.FileFilter{{DisplayName: "Резервная копия (*.vault)", Pattern: "*.vault"}}

// VaultBackup saves a password-encrypted copy of the vault to a file chosen in the system
// dialog and returns its path ("" when the dialog was cancelled). A vault with a password is
// copied under that password and password must be empty; a vault on DPAPI needs password, which
// protects only the copy.
func (a *App) VaultBackup(password string) (string, error) {
	if !a.storeUnlocked() {
		return "", errVaultLocked
	}
	if !a.vaultRef().HasPassword() && utf8.RuneCountInString(password) < minPasswordLen {
		return "", errPasswordShort
	}
	if a.wapp == nil {
		return "", errNoWindow
	}
	path, err := dialogPath(a.wapp.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "Резервная копия хранилища",
		Filename: "sshclient-backup-" + time.Now().Format("2006-01-02") + backupExt,
		Filters:  backupFilters,
		Window:   a.mainWin,
	}).PromptForSingleSelection())
	if errors.Is(err, ErrCancelled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += backupExt
	}
	return path, a.writeBackup(path, password)
}

// writeBackup writes the backup file at path (VaultBackup after its dialog).
func (a *App) writeBackup(path, password string) error {
	s := a.storeRef()
	if s == nil || !a.storeUnlocked() {
		return errVaultLocked
	}
	var pw []byte
	if !a.vaultRef().HasPassword() {
		pw = []byte(password)
		defer clear(pw)
	}
	if err := s.Backup(path, pw); err != nil {
		log.Printf("vault backup: %v", err)
		return errors.New("Не удалось сохранить резервную копию") //nolint:staticcheck // ST1005: Russian UI text
	}
	return nil
}

// VaultRestorePick asks for a backup file in the system dialog and returns its path ("" when
// cancelled). A file that is not a backup is refused before the password is asked.
func (a *App) VaultRestorePick() (string, error) {
	if a.wapp == nil {
		return "", errNoWindow
	}
	path, err := dialogPath(a.wapp.Dialog.OpenFile().
		AttachToWindow(a.mainWin).
		SetTitle("Восстановить из резервной копии").
		AddFilter(backupFilters[0].DisplayName, backupFilters[0].Pattern).
		AddFilter("Все файлы (*.*)", "*.*").
		PromptForSingleSelection())
	if errors.Is(err, ErrCancelled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := store.ReadBackup(path); err != nil {
		return "", backupError(err)
	}
	return path, nil
}

// backupError maps a store error of a backup file to the UI text.
func backupError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotBackup):
		return errNotBackup
	case errors.Is(err, store.ErrWrongPassword):
		return fieldError("password", "Неверный пароль копии")
	default:
		log.Printf("read backup: %v", err)
		return errReadCopy
	}
}

// VaultRestore replaces the vault with the backup at path. The password is checked first and a
// wrong one changes nothing. Then every session, forward and host connection is closed, the
// current vault.json is kept as vault.before-restore-<time>.json, the backup becomes vault.json
// and opens unlocked; its password stays the vault password. It works on an open vault and on a
// corrupt one, not on a locked one.
func (a *App) VaultRestore(path, password string) error {
	gen := a.refs.Load()
	r := a.current()
	if r.vault == nil && !errors.Is(r.err, store.ErrCorrupt) {
		return errVaultMissing
	}
	if r.vault != nil && r.vault.Locked() {
		return errVaultLocked
	}
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	pw := []byte(password)
	b, err := store.OpenBackup(path, pw)
	clear(pw)
	if err != nil {
		return backupError(err)
	}

	a.refsMu.Lock()
	defer a.refsMu.Unlock()
	if a.refs.Load() != gen { // replaced meanwhile (another restore or a corrupt reset)
		b.Discard()
		return errors.New("Хранилище изменилось, повторите восстановление") //nolint:staticcheck // ST1005: Russian UI text
	}
	a.closeAllSessions()
	a.stopAllForwards()
	a.closeAllHostConns()
	defer a.reopenHostConns()

	vaultPath := filepath.Join(dir, vaultFile)
	install := func() error {
		_, err := store.InstallBackup(vaultPath, b, time.Now())
		return err
	}
	if r.store != nil {
		err = r.store.Retire(install)
	} else {
		err = install()
	}
	if err != nil {
		b.Discard()
		log.Printf("vault restore: %v", err)
		return errors.New("Не удалось восстановить хранилище, текущее не изменено") //nolint:staticcheck // ST1005: Russian UI text
	}
	a.setVault(b, store.NewStore(b), nil)
	return nil
}

// reopenHostConns lets new host connections start again after closeAllHostConns (a restore) and
// forgets the last errors of the old hosts. Hosts whose server key changed stay blocked.
func (a *App) reopenHostConns() {
	a.hcMu.Lock()
	defer a.hcMu.Unlock()
	a.hcDown = false
	a.hcLastErr = nil
}
