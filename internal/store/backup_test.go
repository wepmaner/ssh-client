package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sshclient/internal/model"
)

// filledStore is a DPAPI store with a key, a host, a group, a forward and a moved window.
func filledStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := newVaultPath(t)
	s := NewStore(mustOpen(t, path))
	if _, err := s.SaveKey(model.Key{Name: "k", Type: "ed25519", Private: []byte("PRIVATE"), Format: "openssh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveGroup(model.Group{Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(model.Host{Address: "example.test", Port: 22, User: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveForward(model.Forward{Kind: "L", Listen: "127.0.0.1:1", Target: "x:2"}); err != nil {
		t.Fatal(err)
	}
	set := s.Settings()
	set.Window = model.WindowState{X: 100, Y: 50, W: 900, H: 700}
	set.TerminalSize = 16
	if err := s.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func backupPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "copy.vault")
}

func TestBackupOfDPAPIVaultIsAPortablePasswordVault(t *testing.T) {
	s, vaultPath := filledStore(t)
	out := backupPath(t)
	if err := s.Backup(out, nil); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("backup without a password: %v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused backup wrote a file")
	}
	if err := s.Backup(out, []byte("backup pass")); err != nil {
		t.Fatal(err)
	}

	env, err := ReadBackup(out)
	if err != nil {
		t.Fatal(err)
	}
	if env.Wrap != WrapPassword || env.KDF == nil {
		t.Fatalf("wrap = %q", env.Wrap)
	}
	main := s.v.env
	if bytes.Equal(env.DEKEnc, main.DEKEnc) || bytes.Equal(env.Nonce, main.Nonce) {
		t.Fatal("backup reuses the main vault's wrapped key or nonce")
	}
	// A fresh DEK: the main DEK does not open the backup data.
	if _, err := Open(s.v.dek, env.Nonce, env.Data); err == nil {
		t.Fatal("backup is sealed with the main DEK")
	}
	if s.v.HasPassword() {
		t.Fatal("the main vault got a password")
	}

	// Opened as an ordinary vault file, with the password and no DPAPI.
	v, err := OpenVault(out)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Locked() {
		t.Fatal("backup must open locked")
	}
	if err := v.Unlock([]byte("wrong")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := v.Unlock([]byte("backup pass")); err != nil {
		t.Fatal(err)
	}
	d := v.Data()
	if len(d.Keys) != 1 || string(d.Keys[0].Private) != "PRIVATE" || len(d.Hosts) != 1 || len(d.Groups) != 1 || len(d.Forwards) != 1 {
		t.Fatalf("backup data: %+v", d)
	}
	if d.Settings.TerminalSize != 16 {
		t.Fatalf("settings not copied: %+v", d.Settings)
	}
	if d.Settings.Window != model.DefaultSettings().Window {
		t.Fatalf("window state copied: %+v", d.Settings.Window)
	}
	if s.Settings().Window.X != 100 {
		t.Fatal("backup changed the live window state")
	}
	if _, err := os.Stat(vaultPath); err != nil {
		t.Fatal(err)
	}
}

func TestBackupOfPasswordVaultKeepsItsPassword(t *testing.T) {
	s, _ := filledStore(t)
	if err := s.v.SetPassword([]byte("vault pass")); err != nil {
		t.Fatal(err)
	}
	out := backupPath(t)
	if err := s.Backup(out, nil); err != nil {
		t.Fatal(err)
	}
	env, err := ReadBackup(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(env.KDF.Salt, s.v.env.KDF.Salt) || !bytes.Equal(env.DEKEnc, s.v.env.DEKEnc) {
		t.Fatal("backup of a password vault must keep its wrap")
	}
	if _, err := OpenBackup(out, []byte("other")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("other password: %v", err)
	}
	b, err := OpenBackup(out, []byte("vault pass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Data().Hosts) != 1 || b.Data().Settings.Window != model.DefaultSettings().Window {
		t.Fatalf("backup data: %+v", b.Data())
	}
}

func TestReadBackupRefusesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.vault")
	if err := os.WriteFile(garbage, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBackup(garbage); !errors.Is(err, ErrNotBackup) {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := OpenBackup(garbage, []byte("pw")); !errors.Is(err, ErrNotBackup) {
		t.Fatalf("open garbage: %v", err)
	}
	// A DPAPI vault.json is a vault but not a portable backup.
	dpapi := filepath.Join(dir, "vault.json")
	mustOpen(t, dpapi)
	if _, err := ReadBackup(dpapi); !errors.Is(err, ErrNotBackup) {
		t.Fatalf("dpapi vault: %v", err)
	}
	// Tampered data under the right password is not a backup either.
	s, _ := filledStore(t)
	out := backupPath(t)
	if err := s.Backup(out, []byte("backup pass")); err != nil {
		t.Fatal(err)
	}
	env, err := ReadBackup(out)
	if err != nil {
		t.Fatal(err)
	}
	env.Data[0] ^= 1
	raw, err := MarshalEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackup(out, []byte("backup pass")); !errors.Is(err, ErrNotBackup) {
		t.Fatalf("tampered: %v", err)
	}
}

func TestInstallBackupMovesTheVaultAside(t *testing.T) {
	src, _ := filledStore(t)
	out := backupPath(t)
	if err := src.Backup(out, []byte("backup pass")); err != nil {
		t.Fatal(err)
	}

	cur := NewStore(mustOpen(t, newVaultPath(t)))
	vaultPath := cur.v.path
	if _, err := cur.SaveHost(model.Host{Address: "current.test"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(vaultPath) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}

	if _, err := OpenBackup(out, []byte("wrong")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	b, err := OpenBackup(out, []byte("backup pass"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 14, 5, 9, 0, time.Local)
	var aside string
	if err := cur.Retire(func() (err error) {
		aside, err = InstallBackup(vaultPath, b, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(aside) != "vault.before-restore-2026-10-27-140509.json" {
		t.Fatalf("aside = %q", aside)
	}
	if got, err := os.ReadFile(aside); err != nil || !bytes.Equal(got, before) { //nolint:gosec // G304: test temp path
		t.Fatalf("aside copy differs: %v", err)
	}
	if cur.Hosts() != nil {
		t.Fatal("the retired store still serves data")
	}
	if _, err := cur.SaveHost(model.Host{Address: "late.test"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("late write through the retired store: %v", err)
	}

	// The new vault.json is the backup: a password vault with the backup's data, unlocked in memory.
	if b.Locked() || !b.HasPassword() || len(b.Data().Hosts) != 1 || b.Data().Hosts[0].Address != "example.test" {
		t.Fatalf("installed vault: locked=%v data=%+v", b.Locked(), b.Data())
	}
	ns := NewStore(b)
	if _, err := ns.SaveHost(model.Host{Address: "after.test"}); err != nil {
		t.Fatal(err)
	}
	re, err := OpenVault(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := re.Unlock([]byte("backup pass")); err != nil {
		t.Fatal(err)
	}
	if len(re.Data().Hosts) != 2 {
		t.Fatalf("reopened hosts: %+v", re.Data().Hosts)
	}

	// A second restore in the same second keeps both older files.
	b2, err := OpenBackup(out, []byte("backup pass"))
	if err != nil {
		t.Fatal(err)
	}
	aside2, err := InstallBackup(vaultPath, b2, now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(aside2) != "vault.before-restore-2026-10-27-140509-2.json" {
		t.Fatalf("second aside = %q", aside2)
	}
}

func TestInstallBackupWithoutVaultFile(t *testing.T) {
	src, _ := filledStore(t)
	out := backupPath(t)
	if err := src.Backup(out, []byte("backup pass")); err != nil {
		t.Fatal(err)
	}
	b, err := OpenBackup(out, []byte("backup pass"))
	if err != nil {
		t.Fatal(err)
	}
	vaultPath := newVaultPath(t)
	aside, err := InstallBackup(vaultPath, b, time.Now())
	if err != nil || aside != "" {
		t.Fatalf("aside %q, %v", aside, err)
	}
	entries, err := os.ReadDir(filepath.Dir(vaultPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "vault.json" {
		t.Fatalf("files: %v", entries)
	}
}

func TestFailedRetireKeepsTheStore(t *testing.T) {
	s, _ := filledStore(t)
	if err := s.Retire(func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected the swap error")
	}
	if len(s.Hosts()) != 1 {
		t.Fatal("a failed retire wiped the store")
	}
	if _, err := s.SaveHost(model.Host{Address: "b"}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedInstallLeavesTheVaultFile(t *testing.T) {
	src, _ := filledStore(t)
	out := backupPath(t)
	if err := src.Backup(out, []byte("backup pass")); err != nil {
		t.Fatal(err)
	}
	b, err := OpenBackup(out, []byte("backup pass"))
	if err != nil {
		t.Fatal(err)
	}
	cur := mustOpen(t, newVaultPath(t))
	before, err := os.ReadFile(cur.path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	// A directory in the way of the temporary file makes the first write fail.
	if err := os.Mkdir(cur.path+".restore.tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBackup(cur.path, b, time.Now()); err == nil {
		t.Fatal("expected an error")
	}
	if got, err := os.ReadFile(cur.path); err != nil || !bytes.Equal(got, before) { //nolint:gosec // G304: test temp path
		t.Fatalf("vault.json changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(cur.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "vault.json" && e.Name() != "vault.json.restore.tmp" {
			t.Fatalf("unexpected file %s", e.Name())
		}
	}
}
