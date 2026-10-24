package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"sshclient/internal/model"
)

// Vault is the encrypted on-disk container of all user data. The data
// encryption key (DEK) stays in memory while the vault is unlocked.
type Vault struct {
	mu   sync.Mutex
	path string
	env  Envelope
	dek  []byte // nil while locked
	data *model.VaultData
}

// OpenVault opens the vault at path. A missing file creates a new DPAPI-wrapped
// vault; a DPAPI vault is decrypted at once, a password vault stays locked.
func OpenVault(path string) (*Vault, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path comes from appdir.Dir() or the caller
	if errors.Is(err, os.ErrNotExist) {
		return createVault(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read vault: %w", err)
	}
	env, err := UnmarshalEnvelope(raw)
	if err != nil {
		return nil, err
	}
	v := &Vault{path: path, env: env}
	if env.Wrap == WrapDPAPI {
		dek, err := dpapiUnprotect(env.DEKEnc)
		if err != nil {
			return nil, fmt.Errorf("%w: dpapi unprotect: %v", ErrCorrupt, err)
		}
		if err := v.decrypt(dek); err != nil {
			clear(dek)
			return nil, err
		}
	}
	return v, nil
}

// RecoverCorrupt moves a damaged vault aside as <path>.corrupt (replacing an
// older one; nothing is moved when path is absent) and opens a fresh DPAPI vault at path.
func RecoverCorrupt(path string) (*Vault, error) {
	if _, err := os.Stat(path); err == nil { //nolint:gosec // G703: path comes from appdir.Dir() or the caller
		if err := os.Rename(path, path+".corrupt"); err != nil { //nolint:gosec // G703: path comes from appdir.Dir() or the caller
			return nil, fmt.Errorf("move corrupt vault: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat vault: %w", err)
	}
	return OpenVault(path)
}

func createVault(path string) (*Vault, error) {
	dek, err := NewDEK()
	if err != nil {
		return nil, err
	}
	v := &Vault{
		path: path,
		env:  Envelope{Version: envelopeVersion},
		dek:  dek,
		data: &model.VaultData{Settings: model.DefaultSettings()},
	}
	if err := v.wrapDPAPI(); err != nil {
		return nil, err
	}
	if err := v.Save(); err != nil {
		return nil, err
	}
	return v, nil
}

// decrypt opens the data section with dek and, on success, unlocks the vault.
func (v *Vault) decrypt(dek []byte) error {
	plain, err := Open(dek, v.env.Nonce, v.env.Data)
	if err != nil {
		return fmt.Errorf("%w: decrypt data: %v", ErrCorrupt, err)
	}
	var d model.VaultData
	if err := json.Unmarshal(plain, &d); err != nil {
		return fmt.Errorf("%w: decode data: %v", ErrCorrupt, err)
	}
	v.dek, v.data = dek, &d
	return nil
}

// Locked reports whether the vault still needs a password.
func (v *Vault) Locked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dek == nil
}

// HasPassword reports whether the DEK is wrapped with a password.
func (v *Vault) HasPassword() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.env.Wrap == WrapPassword
}

// Data returns the live vault data, or nil while locked.
func (v *Vault) Data() *model.VaultData {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.data
}

// Unlock derives the key from password and decrypts the vault.
func (v *Vault) Unlock(password []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dek != nil {
		return nil
	}
	if v.env.Wrap != WrapPassword || v.env.KDF == nil {
		return fmt.Errorf("%w: vault is not password protected", ErrCorrupt)
	}
	kek := DeriveKEK(password, *v.env.KDF)
	defer clear(kek)
	dek, err := Open(kek, v.env.DEKNonce, v.env.DEKEnc)
	if err != nil {
		return ErrWrongPassword
	}
	if err := v.decrypt(dek); err != nil {
		clear(dek)
		return err
	}
	return nil
}

// Save encrypts the data and writes it atomically, keeping the previous file
// as <path>.bak.
func (v *Vault) Save() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dek == nil {
		return errors.New("vault is locked")
	}
	plain, err := json.Marshal(v.data)
	if err != nil {
		return err
	}
	nonce, ct, err := Seal(v.dek, plain)
	if err != nil {
		return err
	}
	env := v.env
	env.Nonce, env.Data = nonce, ct
	if err := v.persist(env); err != nil {
		return err
	}
	v.env = env
	return nil
}

// writeFileSync writes data to name and flushes it to disk.
func writeFileSync(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: sibling of the vault path
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		return err
	}
	return f.Close()
}

// persist writes env to disk via a synced temporary file and a rename.
func (v *Vault) persist(env Envelope) error {
	out, err := MarshalEnvelope(env)
	if err != nil {
		return err
	}
	if prev, err := os.ReadFile(v.path); err == nil { //nolint:gosec // G304: path comes from appdir.Dir() or the caller
		if err := writeFileSync(v.path+".bak", prev); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	}
	tmp := v.path + ".tmp"
	if err := writeFileSync(tmp, out); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return fmt.Errorf("write vault: %w", err)
	}
	if err := os.Rename(tmp, v.path); err != nil { //nolint:gosec // G703: sibling of the vault path
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return fmt.Errorf("replace vault: %w", err)
	}
	return nil
}

func (v *Vault) wrapDPAPI() error {
	return wrapDPAPI(&v.env, v.dek)
}

func wrapDPAPI(env *Envelope, dek []byte) error {
	enc, err := dpapiProtect(dek)
	if err != nil {
		return fmt.Errorf("dpapi protect: %w", err)
	}
	env.Wrap, env.KDF, env.DEKNonce, env.DEKEnc = WrapDPAPI, nil, nil, enc
	return nil
}

// SetPassword re-wraps the same DEK with a password; the data is not
// re-encrypted.
func (v *Vault) SetPassword(password []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dek == nil {
		return errors.New("vault is locked")
	}
	kdf := NewKDFParams()
	kek := DeriveKEK(password, kdf)
	defer clear(kek)
	nonce, enc, err := Seal(kek, v.dek)
	if err != nil {
		return err
	}
	env := v.env
	env.Wrap, env.KDF, env.DEKNonce, env.DEKEnc = WrapPassword, &kdf, nonce, enc
	if err := v.persist(env); err != nil {
		return err
	}
	v.env = env
	return nil
}

// RemovePassword re-wraps the same DEK with DPAPI again.
func (v *Vault) RemovePassword() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dek == nil {
		return errors.New("vault is locked")
	}
	env := v.env
	if err := wrapDPAPI(&env, v.dek); err != nil {
		return err
	}
	if err := v.persist(env); err != nil {
		return err
	}
	v.env = env
	return nil
}
