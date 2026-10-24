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
	e3Raw, _ := os.ReadFile(path) //nolint:gosec // G304: test temp path
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
	cur, _ := os.ReadFile(path) //nolint:gosec // G304: test temp path
	if bytes.Equal(cur, first) {
		t.Fatal("vault file not updated")
	}
}
