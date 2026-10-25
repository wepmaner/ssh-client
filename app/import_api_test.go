package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// withHome points os.UserHomeDir at a temp folder holding cfg as .ssh/config.
func withHome(t *testing.T, cfg string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if cfg == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportSSHConfigSkipsDuplicates(t *testing.T) {
	a, keyID := newHostsApp(t)
	if _, err := a.HostSave(HostDTO{Address: "db.example.test", Port: 22, User: "deploy", KeyID: keyID}); err != nil {
		t.Fatal(err)
	}
	withHome(t, `
Host db
    HostName db.example.test
    User deploy
Host db-again
    HostName DB.example.test
    User deploy
Host web
    HostName 10.0.4.11
    User deploy
    Port 2222
    IdentityFile ~/.ssh/id_web
Host web2
    HostName 10.0.4.11
    User deploy
    Port 2222
Host nouser
    HostName 10.0.4.12
`)
	res, err := a.ImportSSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Skipped != 4 {
		t.Fatalf("result %+v", res)
	}
	for _, want := range []string{
		"ключ для web нужно импортировать отдельно: ~/.ssh/id_web",
		"db: такой хост уже есть",
		"db-again: такой хост уже есть",
		"web2: такой хост уже есть",
		"nouser: Укажите пользователя",
	} {
		if !slices.Contains(res.Warnings, want) {
			t.Fatalf("missing warning %q in %v", want, res.Warnings)
		}
	}
	var web HostDTO
	for _, h := range a.HostsList() {
		if h.Name == "web" {
			web = h
		}
	}
	if web.Address != "10.0.4.11" || web.Port != 2222 || web.User != "deploy" || web.KeyID != "" || web.KeyName != "" {
		t.Fatalf("imported %+v", web)
	}
	if len(a.HostsList()) != 2 {
		t.Fatalf("hosts %+v", a.HostsList())
	}

	// A second run adds nothing.
	res, err = a.ImportSSHConfig()
	if err != nil || res.Added != 0 || res.Skipped != 5 {
		t.Fatalf("second import %+v %v", res, err)
	}
}

func TestImportSSHConfigMissingFile(t *testing.T) {
	a := newKeysApp(t)
	withHome(t, "")
	if _, err := a.ImportSSHConfig(); err == nil || err.Error() != "Файл ~/.ssh/config не найден" {
		t.Fatalf("err %v", err)
	}
}

type fakePuTTY map[string]map[string]any

func (f fakePuTTY) Sessions() ([]string, error) {
	out := make([]string, 0, len(f))
	for k := range f {
		out = append(out, k)
	}
	slices.Sort(out)
	return out, nil
}

func (f fakePuTTY) String(s, n string) (string, bool) { v, ok := f[s][n].(string); return v, ok }
func (f fakePuTTY) Int(s, n string) (int, bool)       { v, ok := f[s][n].(int); return v, ok }

func TestImportPuTTYUsesFakeRegistry(t *testing.T) {
	a := newKeysApp(t)
	a.putty = fakePuTTY{
		"my%20server": {"HostName": "deploy@srv.example.test", "PortNumber": 2222, "Protocol": "ssh"},
		"same":        {"HostName": "srv.example.test", "UserName": "deploy", "PortNumber": 2222, "Protocol": "ssh"},
		"serial":      {"Protocol": "serial"},
	}
	res, err := a.ImportPuTTY()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Skipped != 1 || len(res.Warnings) != 2 {
		t.Fatalf("result %+v", res)
	}
	hs := a.HostsList()
	if len(hs) != 1 || hs[0].Name != "my server" || hs[0].User != "deploy" || hs[0].Port != 2222 || hs[0].KeyID != "" {
		t.Fatalf("hosts %+v", hs)
	}
}

func TestImportWhileLocked(t *testing.T) {
	a := &App{}
	if _, err := a.ImportPuTTY(); err == nil {
		t.Fatal("import without a vault")
	}
}

func TestHostsExportJSONHasNoKeys(t *testing.T) {
	a, keyID := newHostsApp(t)
	g, err := a.GroupSave("prod")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.HostSave(HostDTO{Name: "db", Address: "db.example.test", Port: 22, User: "deploy", KeyID: keyID, GroupID: g.ID}); err != nil {
		t.Fatal(err)
	}
	data, err := a.hostsExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), keyID) || strings.Contains(string(data), "key") {
		t.Fatalf("export mentions keys: %s", data)
	}
	var got struct {
		Hosts []map[string]any `json:"hosts"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "db", "address": "db.example.test", "port": float64(22), "user": "deploy", "group": "prod"}
	if len(got.Hosts) != 1 || len(got.Hosts[0]) != len(want) {
		t.Fatalf("export %s", data)
	}
	for k, v := range want {
		if got.Hosts[0][k] != v {
			t.Fatalf("export %s", data)
		}
	}
	if err := a.HostsExport(); err != errNoWindow {
		t.Fatalf("windowless export: %v", err)
	}
}

func TestVersion(t *testing.T) {
	a := New(application.WebviewWindowOptions{}, "1.2.3")
	if a.Version() != "1.2.3" {
		t.Fatal(a.Version())
	}
}
