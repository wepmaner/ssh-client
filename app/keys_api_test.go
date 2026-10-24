package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshclient/internal/model"
)

func newKeysApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a := &App{}
	a.openVault()
	if !a.storeUnlocked() {
		t.Fatal("vault not open")
	}
	return a
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "internal", "keys", "testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKeyLifecycle(t *testing.T) {
	a := newKeysApp(t)

	if _, err := a.KeyGenerate("  ", "ed25519", ""); err == nil || err.Error() != "Укажите название ключа" {
		t.Fatalf("empty name: %v", err)
	}
	k, err := a.KeyGenerate("work", "ed25519", "me@box")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ed25519" || k.Comment != "me@box" || k.ID == "" || !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") {
		t.Fatalf("dto: %+v", k)
	}
	if _, err := a.KeyGenerate("WORK", "ed25519", ""); err == nil || err.Error() != "Ключ с таким названием уже есть" {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := a.KeyGenerate("x", "dsa", ""); err == nil {
		t.Fatal("unknown kind accepted")
	}

	if list := a.KeysList(); len(list) != 1 || list[0].ID != k.ID || len(list[0].HostIDs) != 0 {
		t.Fatalf("list: %+v", list)
	}
	if err := a.KeyRename(k.ID, "home"); err != nil {
		t.Fatal(err)
	}
	if got := a.KeysList()[0].Name; got != "home" {
		t.Fatalf("renamed to %q", got)
	}

	if _, err := a.store.SaveHost(model.Host{Name: "srv", Address: "h", Port: 22, User: "u", KeyID: k.ID}); err != nil {
		t.Fatal(err)
	}
	if got := a.KeysList()[0].HostIDs; len(got) != 1 {
		t.Fatalf("host ids: %v", got)
	}
	if err := a.KeyDelete(k.ID); err == nil || err.Error() != "Ключ используется хостами: srv" {
		t.Fatalf("delete in use: %v", err)
	}
	hosts := a.store.Hosts()
	if err := a.store.DeleteHost(hosts[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.KeyDelete(k.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.KeysList()) != 0 {
		t.Fatal("key not deleted")
	}
}

func TestKeyImportText(t *testing.T) {
	a := newKeysApp(t)

	k, err := a.KeyImportText(fixture(t, "ed25519_openssh"), "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "fixture@test" || k.Source != "openssh" {
		t.Fatalf("default name from comment: %+v", k)
	}
	if _, err := a.KeyImportText(fixture(t, "ed25519_openssh"), "fixture@test"); err == nil {
		t.Fatal("duplicate import accepted")
	}
	if _, err := a.KeyImportText("garbage", "g"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := a.KeyImportText(fixture(t, "ecdsa_openssh"), "e"); err == nil || !strings.Contains(err.Error(), "Ed25519") {
		t.Fatalf("ecdsa: %v", err)
	}

	enc := fixture(t, "v3.ppk")
	_, err = a.KeyImportText(enc, "ppk")
	if err == nil || err.Error() != "passphrase_required:text" {
		t.Fatalf("no passphrase: %v", err)
	}
	if _, err := a.KeyImportTextWithPassphrase(enc, "ppk", "nope"); err == nil || err.Error() != "Неверная парольная фраза" {
		t.Fatalf("wrong passphrase: %v", err)
	}
	p, err := a.KeyImportTextWithPassphrase(enc, "ppk", "testkey")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "ppk" || p.Type != "rsa" {
		t.Fatalf("ppk: %+v", p)
	}
}

func TestKeyImportFileWithPassphrase(t *testing.T) {
	a := newKeysApp(t)
	path := filepath.Join("..", "internal", "keys", "testdata", "v3.ppk")
	if _, err := a.KeyImportFileWithPassphrase(path, ""); err == nil || err.Error() != "passphrase_required:"+path {
		t.Fatalf("no passphrase: %v", err)
	}
	k, err := a.KeyImportFileWithPassphrase(path, "testkey")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "v3" {
		t.Fatalf("name from file: %q", k.Name)
	}
}

func TestExportPayload(t *testing.T) {
	a := newKeysApp(t)
	d, err := a.KeyGenerate("srv", "ed25519", "c")
	if err != nil {
		t.Fatal(err)
	}
	k, err := a.findKey(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for format, want := range map[string]string{"public": "srv.pub", "openssh": "srv", "ppk": "srv.ppk"} {
		data, name, err := exportPayload(k, format)
		if err != nil || name != want || len(data) == 0 {
			t.Fatalf("%s: %q %v", format, name, err)
		}
	}
	if _, _, err := exportPayload(k, "x"); err == nil {
		t.Fatal("bad format accepted")
	}
}

func TestVerifyPasswordWithoutVaultPassword(t *testing.T) {
	a := newKeysApp(t)
	if err := a.verifyPassword(""); err != nil {
		t.Fatal(err)
	}
	if err := a.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := a.verifyPassword(""); err == nil || err.Error() != "Неверный пароль" {
		t.Fatalf("empty with password: %v", err)
	}
	if err := a.verifyPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
}
