package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"sshclient/internal/model"
)

func newVaultPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "vault.json")
}

func TestOpenCreatesNewDPAPIVault(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	if v.Locked() {
		t.Fatal("new vault must be unlocked")
	}
	if v.HasPassword() {
		t.Fatal("new vault must not have a password")
	}
	if !reflect.DeepEqual(v.Data().Settings, model.DefaultSettings()) {
		t.Fatalf("settings = %+v", v.Data().Settings)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("vault file missing: %v", err)
	}
}

func TestSetPasswordLocksOnReopen(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	v.Data().Hosts = append(v.Data().Hosts, model.Host{ID: "h1", Name: "one", Address: "a"})
	if err := v.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if !v.HasPassword() {
		t.Fatal("HasPassword must be true")
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	v2, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	if !v2.Locked() || v2.Data() != nil {
		t.Fatal("password vault must open locked")
	}
	if err := v2.Unlock([]byte("bad")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("bad password err = %v", err)
	}
	if !v2.Locked() {
		t.Fatal("still locked after bad password")
	}
	if err := v2.Unlock([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if v2.Locked() || len(v2.Data().Hosts) != 1 || v2.Data().Hosts[0].ID != "h1" {
		t.Fatalf("data = %+v", v2.Data())
	}
}

func TestSetPasswordKeepsDEK(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	e1, err := UnmarshalEnvelope(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	e2, err := UnmarshalEnvelope(after)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Wrap != WrapPassword || e1.Wrap != WrapDPAPI {
		t.Fatalf("wraps %q -> %q", e1.Wrap, e2.Wrap)
	}
	if !bytes.Equal(e1.Nonce, e2.Nonce) || !bytes.Equal(e1.Data, e2.Data) {
		t.Fatal("data was re-encrypted by SetPassword")
	}
	if err := v.RemovePassword(); err != nil {
		t.Fatal(err)
	}
	v3, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	if v3.Locked() || v3.HasPassword() {
		t.Fatal("expected unlocked dpapi vault after RemovePassword")
	}
	e3Raw, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	e3, err := UnmarshalEnvelope(e3Raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(e1.Data, e3.Data) {
		t.Fatal("data changed across wrap round trip")
	}
}

func TestCorruptFileReturnsErrCorrupt(t *testing.T) {
	path := newVaultPath(t)
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenVault(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveIsAtomicAndKeepsBackup(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	v.Data().Settings.TerminalSize = 20
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".bak") //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bak, first) {
		t.Fatal("backup does not hold the previous content")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file left behind: %v", err)
	}
	cur, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(cur, first) {
		t.Fatal("vault file not updated")
	}
}

func TestFailedSaveDoesNotLeakIntoLaterWrites(t *testing.T) {
	path := newVaultPath(t)
	s := NewStore(mustOpen(t, path))
	// A directory in place of the tmp file makes every write fail.
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(model.Host{ID: "lost", Address: "a"}); err == nil {
		t.Fatal("expected save failure")
	}
	if len(s.Hosts()) != 0 {
		t.Fatal("failed save was not rolled back")
	}
	if err := s.v.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	v2 := mustOpen(t, path)
	if err := v2.Unlock([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if len(v2.Data().Hosts) != 0 {
		t.Fatalf("rolled-back data reached disk: %+v", v2.Data().Hosts)
	}
	if _, err := s.SaveHost(model.Host{Address: "b"}); err != nil {
		t.Fatal(err)
	}
	v3 := mustOpen(t, path)
	if err := v3.Unlock([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	if len(v3.Data().Hosts) != 1 || v3.Data().Hosts[0].Address != "b" {
		t.Fatal("disk does not match memory")
	}
}

func mustOpen(t *testing.T, path string) *Vault {
	t.Helper()
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRecoverCorrupt(t *testing.T) {
	path := newVaultPath(t)
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := RecoverCorrupt(path)
	if err != nil {
		t.Fatal(err)
	}
	if v.Locked() {
		t.Fatal("fresh vault must be unlocked")
	}
	got, err := os.ReadFile(path + ".corrupt") //nolint:gosec // G304: test temp path
	if err != nil || string(got) != "garbage" {
		t.Fatalf("corrupt copy = %q, %v", got, err)
	}
	// A second recovery replaces the older .corrupt file.
	if err := os.WriteFile(path, []byte("garbage2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverCorrupt(path); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path + ".corrupt") //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "garbage2" {
		t.Fatalf("corrupt copy = %q", got)
	}
}

func TestTamperedDataIsCorrupt(t *testing.T) {
	path := newVaultPath(t)
	mustOpen(t, path)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	e, err := UnmarshalEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.Data[0] ^= 0xff
	out, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenVault(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
}

func TestCorrectPasswordCorruptDataIsNotWrongPassword(t *testing.T) {
	path := newVaultPath(t)
	v := mustOpen(t, path)
	if err := v.SetPassword([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	e, err := UnmarshalEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.Data[0] ^= 0xff
	out, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := mustOpen(t, path)
	err = v2.Unlock([]byte("pw"))
	if !errors.Is(err, ErrCorrupt) || errors.Is(err, ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
	if !v2.Locked() {
		t.Fatal("must stay locked")
	}
}

func TestRecoverCorruptWithoutVaultKeepsOlderCorrupt(t *testing.T) {
	path := newVaultPath(t)
	if err := os.WriteFile(path+".corrupt", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := RecoverCorrupt(path)
	if err != nil {
		t.Fatal(err)
	}
	if v.Locked() {
		t.Fatal("fresh vault must be unlocked")
	}
	got, err := os.ReadFile(path + ".corrupt") //nolint:gosec // G304: test temp path
	if err != nil || string(got) != "old" {
		t.Fatalf("older corrupt copy = %q, %v", got, err)
	}
}
