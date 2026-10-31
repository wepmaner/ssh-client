package app

import (
	"strings"
	"testing"
	"time"

	"sshclient/internal/sshclient/sshtest"
)

func newHostsApp(t *testing.T) (*App, string) {
	t.Helper()
	a := newKeysApp(t)
	k, err := a.KeyGenerate("work", "ed25519", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return a, k.ID
}

func wantField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), "field:"+field+": ") {
		t.Fatalf("want field %q error, got %v", field, err)
	}
}

func TestHostSaveValidation(t *testing.T) {
	a, keyID := newHostsApp(t)
	ok := HostDTO{Address: "srv.example.test", Port: 22, User: "deploy", KeyID: keyID}

	bad := ok
	bad.Port = 70000
	_, err := a.HostSave(bad)
	wantField(t, err, "port")
	if err.Error() != "field:port: Порт от 1 до 65535" {
		t.Fatalf("port message: %v", err)
	}
	bad = ok
	bad.KeyID = "missing"
	_, err = a.HostSave(bad)
	wantField(t, err, "key")
	bad = ok
	bad.Address = "  "
	_, err = a.HostSave(bad)
	wantField(t, err, "address")
	bad = ok
	bad.User = ""
	_, err = a.HostSave(bad)
	wantField(t, err, "user")
	bad = ok
	bad.GroupID = "missing"
	if _, err = a.HostSave(bad); err == nil {
		t.Fatal("unknown group accepted")
	}
	bad = ok
	bad.ID = "missing"
	if _, err = a.HostSave(bad); err == nil {
		t.Fatal("update of an unknown host accepted")
	}

	in := ok
	in.Address = " srv.example.test "
	in.Name = "  "
	in.Port = 0
	h, err := a.HostSave(in)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID == "" || h.Name != "srv.example.test" || h.Address != "srv.example.test" || h.Port != 22 || h.KeyName != "work" {
		t.Fatalf("saved %+v", h)
	}

	h.Name = "prod"
	h.OS = "ubuntu"
	upd, err := a.HostSave(h)
	if err != nil {
		t.Fatal(err)
	}
	if list := a.HostsList(); len(list) != 1 || list[0].ID != h.ID || upd.Name != "prod" || list[0].OS != "ubuntu" {
		t.Fatalf("update: %+v", list)
	}

	// The key is optional: without one the server asks for the password in the console.
	keyless := ok
	keyless.KeyID = ""
	keyless.Address = "nokey.example.test"
	if h, err := a.HostSave(keyless); err != nil || h.KeyID != "" || h.KeyName != "" {
		t.Fatalf("keyless host %+v, %v", h, err)
	}
}

func TestGroups(t *testing.T) {
	a, keyID := newHostsApp(t)
	_, err := a.GroupSave(" ")
	wantField(t, err, "name")
	g, err := a.GroupSave(" Production ")
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Production" || g.ID == "" {
		t.Fatalf("group %+v", g)
	}
	_, err = a.GroupSave("production")
	wantField(t, err, "name")

	h, err := a.HostSave(HostDTO{Address: "10.0.0.5", User: "root", KeyID: keyID, GroupID: g.ID})
	if err != nil {
		t.Fatal(err)
	}
	if h.GroupName != "Production" {
		t.Fatalf("group name %+v", h)
	}
	if gl := a.GroupsList(); len(gl) != 1 || gl[0].HostCount != 1 || gl[0].ConnectedCount != 0 {
		t.Fatalf("groups %+v", gl)
	}
	if err := a.GroupRename(g.ID, "Prod"); err != nil {
		t.Fatal(err)
	}
	if a.HostsList()[0].GroupName != "Prod" {
		t.Fatal("rename not reflected")
	}
	if err := a.GroupRename("missing", "x"); err == nil {
		t.Fatal("rename of an unknown group accepted")
	}
	if err := a.GroupDelete(g.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.GroupsList()) != 0 {
		t.Fatal("group not deleted")
	}
	if l := a.HostsList(); len(l) != 1 || l[0].GroupID != "" || l[0].GroupName != "" {
		t.Fatalf("host should stay without a group: %+v", l)
	}
}

func TestHostDeleteClosesItsSessions(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{}, true)
	g, err := s.GroupSave("Lab")
	if err != nil {
		t.Fatal(err)
	}
	h := s.HostsList()[0]
	h.GroupID = g.ID
	if _, err := s.HostSave(h); err != nil {
		t.Fatal(err)
	}
	id := s.open(t)
	s.ev.waitState(t, id, "connected", 5*time.Second)
	if l := s.HostsList(); !l[0].Connected {
		t.Fatalf("host not connected: %+v", l)
	}
	if gl := s.GroupsList(); gl[0].ConnectedCount != 1 {
		t.Fatalf("group connected count: %+v", gl)
	}

	if err := s.HostDelete(s.hostID); err != nil {
		t.Fatal(err)
	}
	if len(s.SessionList()) != 0 {
		t.Fatal("session of the deleted host still listed")
	}
	if len(s.HostsList()) != 0 {
		t.Fatal("host not deleted")
	}
	if l := s.KeysList(); len(l) != 1 || len(l[0].HostIDs) != 0 {
		t.Fatalf("key still used: %+v", l)
	}
}

func TestHostSaveRejectsUnknownAccess(t *testing.T) {
	a, keyID := newHostsApp(t)
	in := HostDTO{Address: "srv.example.test", Port: 22, User: "deploy", KeyID: keyID, MCPAccess: "root"}
	_, err := a.HostSave(in)
	wantField(t, err, "mcp_access")
	for _, mode := range []string{"", "read", "full"} {
		in.MCPAccess = mode
		in.ID = ""
		h, err := a.HostSave(in)
		if err != nil || h.MCPAccess != mode {
			t.Fatalf("mode %q: %+v %v", mode, h, err)
		}
	}
}
