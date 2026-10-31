package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"sshclient/internal/model"
)

// ErrNotBackup is returned for a file that is not a password-protected vault envelope.
var ErrNotBackup = errors.New("store: not a vault backup")

// A backup is a password vault file: the same envelope as vault.json with the password wrap,
// so it opens on any Windows account and any PC. It carries the whole vault data except the
// window geometry, which belongs to the screen it was saved on.

// backupPlain is the JSON payload of a backup: d without the window state.
func backupPlain(d *model.VaultData) ([]byte, error) {
	c := *d
	c.Settings.Window = model.DefaultSettings().Window
	c.MCPJournal = nil // the journal belongs to this machine's history
	return json.Marshal(c)
}

// WriteBackup writes data to path as a new password vault: a fresh DEK wrapped by a fresh
// Argon2id KEK of password. The file is written atomically.
func WriteBackup(path string, data *model.VaultData, password []byte) error {
	if len(password) == 0 {
		return ErrNoPassword
	}
	dek, err := NewDEK()
	if err != nil {
		return err
	}
	defer clear(dek)
	kdf := NewKDFParams()
	kek := DeriveKEK(password, kdf)
	defer clear(kek)
	dekNonce, dekEnc, err := Seal(kek, dek)
	if err != nil {
		return err
	}
	env := Envelope{Version: envelopeVersion, Wrap: WrapPassword, KDF: &kdf, DEKNonce: dekNonce, DEKEnc: dekEnc}
	if env.Nonce, env.Data, err = sealBackup(dek, data); err != nil {
		return err
	}
	return writeEnvelope(path, env)
}

func sealBackup(dek []byte, data *model.VaultData) (nonce, ct []byte, err error) {
	plain, err := backupPlain(data)
	if err != nil {
		return nil, nil, err
	}
	defer clear(plain) // the plaintext JSON carries the private keys in base64
	return Seal(dek, plain)
}

// backup writes the vault data to path. A password vault is written with its own wrap (same
// password and KDF parameters, the data sealed again); a DPAPI vault needs password and gets a
// backup of its own (see WriteBackup).
func (v *Vault) backup(path string, password []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dek == nil {
		return ErrLocked
	}
	if v.env.Wrap != WrapPassword {
		return WriteBackup(path, v.data, password)
	}
	env := v.env
	var err error
	if env.Nonce, env.Data, err = sealBackup(v.dek, v.data); err != nil {
		return err
	}
	return writeEnvelope(path, env)
}

// Backup writes a portable copy of the vault to path; password is used only for a vault
// without one (see Vault.backup). No mutation runs meanwhile.
func (s *Store) Backup(path string, password []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v.backup(path, password)
}

// writeEnvelope writes env to path through a synced temporary file in the same folder and a rename.
func writeEnvelope(path string, env Envelope) error {
	out, err := MarshalEnvelope(env)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	tmp := f.Name()
	_ = f.Close() //nolint:gosec // G104: reopened by writeFileSync
	if err := writeFileSync(tmp, out); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return fmt.Errorf("write backup: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil { //nolint:gosec // G703: path chosen by the user in the system dialog
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

// ReadBackup reads and validates a backup file without decrypting it: anything but a password
// vault envelope is ErrNotBackup.
func ReadBackup(path string) (Envelope, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path chosen by the user in the system dialog
	if err != nil {
		return Envelope{}, fmt.Errorf("read backup: %w", err)
	}
	env, err := UnmarshalEnvelope(raw)
	if err != nil || env.Wrap != WrapPassword {
		return Envelope{}, ErrNotBackup
	}
	return env, nil
}

// OpenBackup reads the backup at path and unlocks it with password. The result lives only in
// memory until InstallBackup; a wrong password is ErrWrongPassword, a damaged file ErrNotBackup.
func OpenBackup(path string, password []byte) (*Vault, error) {
	env, err := ReadBackup(path)
	if err != nil {
		return nil, err
	}
	b := &Vault{env: env}
	if err := b.Unlock(password); err != nil {
		if errors.Is(err, ErrWrongPassword) {
			return nil, err
		}
		return nil, ErrNotBackup
	}
	return b, nil
}

// AsideName is the name the current vault file is kept under by a restore at t.
func AsideName(t time.Time) string {
	return "vault.before-restore-" + t.Format("2006-01-02-150405") + ".json"
}

// renameFile replaces a file atomically; tests swap it to make the final step fail.
var renameFile = os.Rename

// InstallBackup makes the unlocked backup b the vault at vaultPath. vault.json is never moved:
// its bytes are first copied to AsideName(now) in the same folder (a new file, with a numeric
// suffix when that name is taken, never overwriting one), the backup envelope is written to a
// temporary file, and one atomic rename replaces vault.json. A failure at any step leaves
// vaultPath as it was. It returns the aside path ("" when there was no vault file).
func InstallBackup(vaultPath string, b *Vault, now time.Time) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dek == nil || b.env.Wrap != WrapPassword {
		return "", ErrLocked
	}
	out, err := MarshalEnvelope(b.env)
	if err != nil {
		return "", err
	}
	aside := ""
	cur, err := os.ReadFile(vaultPath) //nolint:gosec // G304: path comes from appdir.Dir() or the caller
	switch {
	case err == nil:
		if aside, err = writeAside(filepath.Dir(vaultPath), now, cur); err != nil {
			return "", fmt.Errorf("keep current vault: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("read vault: %w", err)
	}
	tmp := vaultPath + ".restore.tmp"
	if err := writeFileSync(tmp, out); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return "", fmt.Errorf("write vault: %w", err)
	}
	if err := renameFile(tmp, vaultPath); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return "", fmt.Errorf("replace vault: %w", err)
	}
	b.path = vaultPath
	return aside, nil
}

// writeAside writes data to a new file AsideName(now) in dir, or to the first free "-2", "-3"…
// variant of it, and flushes it to disk. An existing file is never opened for writing.
func writeAside(dir string, now time.Time, data []byte) (string, error) {
	base := AsideName(now)
	stem := base[:len(base)-len(".json")]
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = stem + "-" + strconv.Itoa(i) + ".json"
		}
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: sibling of the vault path
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(p) //nolint:gosec // G104: best-effort cleanup after the primary error
			return "", err
		}
		return p, nil
	}
	return "", errors.New("no free name for the current vault")
}

// Retire takes the store out of use: swap runs while no store read or write and no vault-level
// writer (Save, SetPassword, RemovePassword, Lock) can run, and on success the vault key and the
// decrypted data are wiped, so a late write through this store or its vault fails instead of
// overwriting the file swap installed. A failed swap leaves the store as it was. swap must not
// touch this store or its vault.
func (s *Store) Retire(swap func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.mu.Lock()
	defer s.v.mu.Unlock()
	if err := swap(); err != nil {
		return err
	}
	s.v.wipeLocked()
	return nil
}

// wipe forgets the DEK and the decrypted data, whatever protects the vault.
func (v *Vault) wipe() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.wipeLocked()
}

// wipeLocked is wipe for a caller holding v.mu.
func (v *Vault) wipeLocked() {
	clear(v.dek)
	if v.data != nil {
		clearPrivate(v.data.Keys)
	}
	v.dek, v.data = nil, nil
}

// Discard wipes a vault that is not going to be used, such as a backup opened for a restore
// that then failed.
func (v *Vault) Discard() { v.wipe() }
