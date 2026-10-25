package importers

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"sshclient/internal/model"
)

func parse(t *testing.T, cfg string) ([]model.Host, []string) {
	t.Helper()
	hosts, warns, err := ParseSSHConfig(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return hosts, warns
}

func TestParseSSHConfigBasic(t *testing.T) {
	hosts, warns := parse(t, `
# work servers
Host prod
    HostName 10.0.4.11
    User deploy
    Port 2222
`)
	want := []model.Host{{Name: "prod", Address: "10.0.4.11", Port: 2222, User: "deploy"}}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts %+v", hosts)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings %v", warns)
	}
}

func TestParseSSHConfigDefaultsAndSyntax(t *testing.T) {
	hosts, _ := parse(t, "host a b\n\tuser=alice\nHost \"c\"\r\n  HostName = c.example.test # comment\r\n  User bob\n")
	want := []model.Host{
		{Name: "a", Address: "a", Port: 22, User: "alice"},
		{Name: "b", Address: "b", Port: 22, User: "alice"},
		{Name: "c", Address: "c.example.test", Port: 22, User: "bob"},
	}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts %+v", hosts)
	}
}

func TestParseSSHConfigSkipsWildcards(t *testing.T) {
	hosts, _ := parse(t, `
Host *.example.test web?
    User nobody
Host db !db-old
    HostName db.example.test
Host *
    User admin
    Port 2200
`)
	// Wildcard patterns are not hosts, but their values apply to the matching aliases,
	// first value wins as in OpenSSH.
	want := []model.Host{{Name: "db", Address: "db.example.test", Port: 2200, User: "admin"}}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts %+v", hosts)
	}
}

func TestParseSSHConfigWarnsIdentityFile(t *testing.T) {
	hosts, warns := parse(t, `
Host prod
    HostName prod.example.test
    User deploy
    IdentityFile ~/.ssh/id_prod
Match host other
    User skipped
Include conf.d/*
`)
	if len(hosts) != 1 || hosts[0].KeyID != "" {
		t.Fatalf("hosts %+v", hosts)
	}
	if !slices.Contains(warns, "ключ для prod нужно импортировать отдельно: ~/.ssh/id_prod") {
		t.Fatalf("warnings %v", warns)
	}
	if !slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, "Match") }) ||
		!slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, "Include") }) {
		t.Fatalf("Match and Include are not reported: %v", warns)
	}
}

func TestParseSSHConfigBadPort(t *testing.T) {
	hosts, warns := parse(t, "Host x\n  User u\n  Port many\n")
	if len(hosts) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "x") {
		t.Fatalf("hosts %+v warnings %v", hosts, warns)
	}
}

// fakeRegistry maps a session name (as stored in the registry) to its values.
type fakeRegistry struct {
	sessions map[string]map[string]any
	err      error
}

func (f fakeRegistry) Sessions() ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, 0, len(f.sessions))
	for name := range f.sessions {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

func (f fakeRegistry) String(session, name string) (string, bool) {
	v, ok := f.sessions[session][name].(string)
	return v, ok
}

func (f fakeRegistry) Int(session, name string) (int, bool) {
	v, ok := f.sessions[session][name].(int)
	return v, ok
}

func TestPuTTYSessionNameDecoded(t *testing.T) {
	reg := fakeRegistry{sessions: map[string]map[string]any{
		"my%20server": {"HostName": "srv.example.test", "PortNumber": 2222, "UserName": "deploy", "Protocol": "ssh"},
		"other":       {"HostName": "root@10.0.0.7", "PortNumber": 22, "Protocol": "ssh"},
	}}
	hosts, warns, err := ReadPuTTYSessionsFrom(reg)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Host{
		{Name: "my server", Address: "srv.example.test", Port: 2222, User: "deploy"},
		{Name: "other", Address: "10.0.0.7", Port: 22, User: "root"},
	}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts %+v", hosts)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings %v", warns)
	}
}

func TestReadPuTTYSessionsSkipsNonSSH(t *testing.T) {
	reg := fakeRegistry{sessions: map[string]map[string]any{
		"Default%20Settings": {"HostName": "", "Protocol": "ssh"},
		"router":             {"HostName": "10.0.0.1", "PortNumber": 23, "Protocol": "telnet"},
		"empty":              {"Protocol": "ssh"},
		"ok":                 {"HostName": "ok.example.test", "Protocol": "ssh"},
	}}
	hosts, warns, err := ReadPuTTYSessionsFrom(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "ok" || hosts[0].Port != 22 {
		t.Fatalf("hosts %+v", hosts)
	}
	if !slices.Contains(warns, "router: сессия telnet пропущена, импортируются только SSH") ||
		!slices.Contains(warns, "empty: не указан адрес") || len(warns) != 2 {
		t.Fatalf("warnings %v", warns)
	}
}

func TestReadPuTTYSessionsError(t *testing.T) {
	boom := errors.New("boom")
	if _, _, err := ReadPuTTYSessionsFrom(fakeRegistry{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
}

// bom is the UTF-8 byte order mark Notepad puts at the start of a file.
var bom = string(rune(0xFEFF))

func TestParseSSHConfigBOMAndUnknownGlobals(t *testing.T) {
	hosts, warns := parse(t, bom+"Host prod\n    HostName prod.example.test\n    User deploy\n")
	if len(hosts) != 1 || hosts[0].Address != "prod.example.test" || len(warns) != 0 {
		t.Fatalf("BOM config: hosts %+v warnings %v", hosts, warns)
	}
	_, warns = parse(t, "Hots prod\nServerAliveInterval 30\nHost prod\n  User u\n  Hots x\n")
	if !slices.Equal(warns, []string{"строка 1: неизвестный параметр Hots"}) {
		t.Fatalf("warnings %v", warns)
	}
}

func TestParseSSHConfigGlobalIdentityFileWarnsOnce(t *testing.T) {
	_, warns := parse(t, `
IdentityFile ~/.ssh/id_all
Host a b c
    User u
Host *.example.test
    IdentityFile ~/.ssh/id_ex
`)
	want := []string{
		"ключ для всех хостов нужно импортировать отдельно: ~/.ssh/id_all",
		"ключ для Host *.example.test нужно импортировать отдельно: ~/.ssh/id_ex",
	}
	if !slices.Equal(warns, want) {
		t.Fatalf("warnings %v", warns)
	}
}
