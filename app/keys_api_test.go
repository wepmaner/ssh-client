package app

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

func newKeysApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())
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

	if _, err := a.KeyGenerate("  ", "ed25519", "", ""); err == nil || err.Error() != "Укажите название ключа" {
		t.Fatalf("empty name: %v", err)
	}
	k, err := a.KeyGenerate("work", "ed25519", "me@box", "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ed25519" || k.Comment != "me@box" || k.ID == "" || !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") {
		t.Fatalf("dto: %+v", k)
	}
	if _, err := a.KeyGenerate("WORK", "ed25519", "", ""); err == nil || err.Error() != "Ключ с таким названием уже есть" {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := a.KeyGenerate("x", "dsa", "", ""); err == nil {
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
	p, err := a.KeyImportText(enc, "ppk")
	if err != nil {
		t.Fatalf("encrypted ppk must import without a passphrase: %v", err)
	}
	if p.Name != "ppk" || p.Type != "rsa" || !p.Encrypted || p.Format != "ppk" {
		t.Fatalf("ppk: %+v", p)
	}
	stored, err := a.findKey(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Private) != strings.TrimSpace(enc) {
		t.Fatal("encrypted ppk must be stored in its original bytes")
	}
}

func TestKeyImportLegacyPEMAsksPassphrase(t *testing.T) {
	a := newKeysApp(t)
	enc := fixture(t, "legacy_encrypted.pem") // passphrase testkey
	if _, err := a.KeyImportText(enc, "old"); err == nil || err.Error() != "passphrase_required:text" {
		t.Fatalf("no passphrase: %v", err)
	}
	if _, err := a.KeyImportTextWithPassphrase(enc, "old", "nope"); err == nil || err.Error() != "Неверная парольная фраза" {
		t.Fatalf("wrong passphrase: %v", err)
	}
	k, err := a.KeyImportTextWithPassphrase(enc, "old", "testkey")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Encrypted || k.Format != "pem" {
		t.Fatalf("dto: %+v", k)
	}
	stored, err := a.findKey(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Private) != strings.TrimSpace(enc) {
		t.Fatal("legacy PEM must stay encrypted in its original bytes")
	}

	path := filepath.Join(t.TempDir(), "legacy.pem")
	if err := os.WriteFile(path, []byte(enc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.KeyImportFileWithPassphrase(path, ""); err == nil || err.Error() != "passphrase_required:"+path {
		t.Fatalf("file without passphrase: %v", err)
	}
	if _, err := a.KeyImportFileWithPassphrase(path, "testkey"); err != nil {
		t.Fatal(err)
	}
}

func TestKeyImportFileEncryptedPPK(t *testing.T) {
	a := newKeysApp(t)
	path := filepath.Join("..", "internal", "keys", "testdata", "v3.ppk")
	k, err := a.KeyImportFileWithPassphrase(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "v3" || !k.Encrypted || k.Format != "ppk" {
		t.Fatalf("dto: %+v", k)
	}
}

func TestKeyGenerateWithPassphrase(t *testing.T) {
	a := newKeysApp(t)
	d, err := a.KeyGenerate("locked", "ed25519", "c", "phrase-123")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Encrypted || d.Format != "openssh" {
		t.Fatalf("dto: %+v", d)
	}
	k, err := a.findKey(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParseRawPrivateKey(k.Private); err == nil {
		t.Fatal("stored key is not encrypted")
	}
	if _, err := ssh.ParseRawPrivateKeyWithPassphrase(k.Private, []byte("phrase-123")); err != nil {
		t.Fatalf("stored key does not open with its passphrase: %v", err)
	}
	blob, err := json.Marshal(a.store.Keys())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "phrase-123") {
		t.Fatal("passphrase leaked into the store")
	}

	plain, err := a.KeyGenerate("open", "ed25519", "c", "")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Encrypted {
		t.Fatal("empty passphrase must mean an unencrypted key")
	}
}

func TestExportPayloadEncrypted(t *testing.T) {
	a := newKeysApp(t)
	d, err := a.KeyImportText(fixture(t, "v3.ppk"), "put")
	if err != nil {
		t.Fatal(err)
	}
	k, err := a.findKey(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"openssh", "ppk"} {
		if _, _, err := exportPayload(k, f); err == nil {
			t.Fatalf("%s export of an encrypted key accepted", f)
		}
	}
	data, name, err := exportPayload(k, "original")
	if err != nil || name != "put.ppk" || string(data) != string(k.Private) {
		t.Fatalf("original: %q %v", name, err)
	}
	if _, name, err := exportPayload(k, "public"); err != nil || name != "put.pub" {
		t.Fatalf("public: %q %v", name, err)
	}
}

func TestExportPayload(t *testing.T) {
	a := newKeysApp(t)
	d, err := a.KeyGenerate("srv", "ed25519", "c", "")
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

func TestDialogPath(t *testing.T) {
	if p, err := dialogPath(`C:\k\id_ed25519`, nil); err != nil || p != `C:\k\id_ed25519` {
		t.Fatalf("chosen: %q %v", p, err)
	}
	if _, err := dialogPath("", nil); !errors.Is(err, ErrCancelled) {
		t.Fatalf("empty path: %v", err)
	}
	if _, err := dialogPath("", errors.New(dialogCancelledText)); !errors.Is(err, ErrCancelled) {
		t.Fatalf("dismissed: %v", err)
	}
	boom := errors.New("boom")
	if _, err := dialogPath("", boom); !errors.Is(err, boom) {
		t.Fatalf("failure: %v", err)
	}
}

func TestWindowlessCallsFailCleanly(t *testing.T) {
	a := newKeysApp(t)
	k, err := a.KeyGenerate("k", "ed25519", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.KeyCopyPublic(k.ID); !errors.Is(err, errNoWindow) {
		t.Fatalf("copy: %v", err)
	}
	if _, err := a.KeyImportFile(); !errors.Is(err, errNoWindow) {
		t.Fatalf("import: %v", err)
	}
	if err := a.KeyExport(k.ID, "public", ""); !errors.Is(err, errNoWindow) {
		t.Fatalf("export: %v", err)
	}
}

// The Wails dialog error lives in an internal package; compare its text with
// the module source so a Wails upgrade that changes it fails here.
func TestDialogCancelledTextMatchesWails(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v3").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "internal", "go-common-file-dialog", "cfd", "errors.go")) //nolint:gosec // G304: file in the Wails module
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `ErrorCancelled = errors.New("`+dialogCancelledText+`")`) {
		t.Fatalf("cfd.ErrorCancelled text changed:\n%s", src)
	}
}
