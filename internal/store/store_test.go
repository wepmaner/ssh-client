package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"sshclient/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	v, err := OpenVault(newVaultPath(t))
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(v)
}

func TestSaveHostAssignsIDAndDefaultName(t *testing.T) {
	s := newTestStore(t)
	h, err := s.SaveHost(model.Host{Address: "10.0.0.1", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	if h.ID == "" || h.Name != "10.0.0.1" || h.CreatedAt.IsZero() {
		t.Fatalf("host = %+v", h)
	}
	h.Name = "renamed"
	h2, err := s.SaveHost(h)
	if err != nil {
		t.Fatal(err)
	}
	if h2.ID != h.ID || !h2.CreatedAt.Equal(h.CreatedAt) || len(s.Hosts()) != 1 || s.Hosts()[0].Name != "renamed" {
		t.Fatalf("update broke: %+v %+v", h2, s.Hosts())
	}
}

func TestDeleteKeyInUseFails(t *testing.T) {
	s := newTestStore(t)
	k, err := s.SaveKey(model.Key{Name: "k"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.SaveHost(model.Host{Address: "a", KeyID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKey(k.ID); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("err = %v", err)
	}
	if err := s.DeleteHost(h.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Keys()) != 0 {
		t.Fatal("key not deleted")
	}
}

func TestHostsUsingKey(t *testing.T) {
	s := newTestStore(t)
	for _, kid := range []string{"k1", "k1", "k2"} {
		if _, err := s.SaveHost(model.Host{Address: "a", KeyID: kid}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.HostsUsingKey("k1")); n != 2 {
		t.Fatalf("k1 hosts = %d", n)
	}
	if n := len(s.HostsUsingKey("none")); n != 0 {
		t.Fatalf("none hosts = %d", n)
	}
}

func TestDeleteGroupUngroupsHosts(t *testing.T) {
	s := newTestStore(t)
	g, err := s.SaveGroup(model.Group{Name: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(model.Host{Address: "a", GroupID: g.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatal(err)
	}
	if s.Hosts()[0].GroupID != "" || len(s.Groups()) != 0 {
		t.Fatal("group not removed or host not ungrouped")
	}
}

func TestStorePersists(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(v)
	if _, err := s.SaveForward(model.Forward{Name: "f", Kind: "L"}); err != nil {
		t.Fatal(err)
	}
	set := s.Settings()
	set.TerminalSize = 18
	if err := s.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	v2, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(v2)
	if len(s2.Forwards()) != 1 || s2.Settings().TerminalSize != 18 {
		t.Fatal("not persisted")
	}
}

func TestEmptyKeyIDGuards(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveHost(model.Host{Address: "a"}); err != nil {
		t.Fatal(err)
	}
	if got := s.HostsUsingKey(""); got != nil {
		t.Fatalf("got %v", got)
	}
	if err := s.DeleteKey(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestKeysReturnsDeepCopy(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveKey(model.Key{Name: "k", Private: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	s.Keys()[0].Private[0] = 'X'
	if string(s.Keys()[0].Private) != "secret" {
		t.Fatal("internal key bytes were mutated")
	}
}

func TestKeyMetasCarryNoPrivateBytes(t *testing.T) {
	s := newTestStore(t)
	k, err := s.SaveKey(model.Key{Name: "k", Private: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	metas := s.KeyMetas()
	if len(metas) != 1 || metas[0].Name != "k" || metas[0].Private != nil {
		t.Fatalf("metas %+v", metas)
	}
	got, ok := s.Key(k.ID)
	if !ok || string(got.Private) != "secret" {
		t.Fatalf("key %+v ok=%v", got, ok)
	}
	clear(got.Private) // the caller's copy: wiping it leaves the vault intact
	if again, _ := s.Key(k.ID); string(again.Private) != "secret" {
		t.Fatal("Key returned the live bytes")
	}
	if _, ok := s.Key("missing"); ok {
		t.Fatal("unknown id found")
	}
	if _, ok := s.Key(""); ok {
		t.Fatal("empty id found")
	}
}

func TestMutateWipesOnlyTheRollbackCopy(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveKey(model.Key{Name: "k", Private: []byte("secret")}); err != nil {
		t.Fatal(err)
	}
	// A second save wipes the snapshot of the first key; the live bytes must survive it.
	if _, err := s.SaveHost(model.Host{Address: "h", Port: 22, User: "u"}); err != nil {
		t.Fatal(err)
	}
	if string(s.v.Data().Keys[0].Private) != "secret" {
		t.Fatalf("live key wiped: %q", s.v.Data().Keys[0].Private)
	}
}

func TestDecodeDataMigratesLegacyKeys(t *testing.T) {
	// Before Task 9b keys were always unencrypted OpenSSH under "private_pem".
	legacy := `{"keys":[{"id":"k1","name":"old","private_pem":"c2VjcmV0","source":"generated"}]}`
	d, err := decodeData([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	k := d.Keys[0]
	if string(k.Private) != "secret" || k.Format != "openssh" || k.Encrypted {
		t.Fatalf("legacy key not migrated: %+v", k)
	}

	current := `{"keys":[{"id":"k2","name":"new","private":"c2VjcmV0","format":"ppk","encrypted":true}]}`
	d, err = decodeData([]byte(current))
	if err != nil {
		t.Fatal(err)
	}
	k = d.Keys[0]
	if string(k.Private) != "secret" || k.Format != "ppk" || !k.Encrypted {
		t.Fatalf("current key changed: %+v", k)
	}
}

func TestKeyFormatSurvivesReopen(t *testing.T) {
	path := newVaultPath(t)
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(v).SaveKey(model.Key{Name: "k", Private: []byte("x"), Format: "pem", Encrypted: true}); err != nil {
		t.Fatal(err)
	}
	v2, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	k := NewStore(v2).Keys()[0]
	if string(k.Private) != "x" || k.Format != "pem" || !k.Encrypted {
		t.Fatalf("reopened key: %+v", k)
	}
}

func TestAddHostsSavesAllAtOnce(t *testing.T) {
	s := newTestStore(t)
	added, err := s.AddHosts([]model.Host{{Address: "a.example.test", User: "u"}, {Name: "b", Address: "b.example.test", User: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	all := s.Hosts()
	if len(added) != 2 || len(all) != 2 || all[0].ID == "" || all[0].ID == all[1].ID || all[0].Name != "a.example.test" || all[1].CreatedAt.IsZero() {
		t.Fatalf("hosts %+v", all)
	}
	if added[1].ID != all[1].ID {
		t.Fatalf("returned %+v", added)
	}
}

func TestMCPJournalKeepsLast500(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 510; i++ {
		if err := s.MCPJournalAdd(model.MCPRecord{ID: fmt.Sprint(i), Tool: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	j := s.MCPJournal()
	if len(j) != 500 || j[0].ID != "509" || j[499].ID != "10" {
		t.Fatalf("len=%d first=%s last=%s", len(j), j[0].ID, j[len(j)-1].ID)
	}
	if err := s.MCPJournalClear(); err != nil || len(s.MCPJournal()) != 0 {
		t.Fatalf("clear: %v %d", err, len(s.MCPJournal()))
	}
}

func TestMCPJournalTruncatesText(t *testing.T) {
	s := newTestStore(t)
	// "я" is two bytes: 1025 of them are 2050 bytes, the cut at 2048 is on a rune boundary;
	// the leading "a" shifts it so the cut would land inside a rune.
	text := "a" + strings.Repeat("я", 1025)
	if err := s.MCPJournalAdd(model.MCPRecord{ID: "1", Text: text}); err != nil {
		t.Fatal(err)
	}
	got := s.MCPJournal()[0].Text
	if len(got) > 2048 || !utf8.ValidString(got) || len(got) < 2046 {
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if err := s.MCPJournalAdd(model.MCPRecord{ID: "2", Text: "short"}); err != nil || s.MCPJournal()[0].Text != "short" {
		t.Fatal("short text changed")
	}
}

func TestMCPJournalFinish(t *testing.T) {
	s := newTestStore(t)
	if err := s.MCPJournalAdd(model.MCPRecord{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	code := 3
	if err := s.MCPJournalFinish("a", &code, 120); err != nil {
		t.Fatal(err)
	}
	if err := s.MCPJournalFinish("gone", &code, 1); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
	r := s.MCPJournal()[0]
	if r.ExitCode == nil || *r.ExitCode != 3 || r.DurationMs != 120 {
		t.Fatalf("record = %+v", r)
	}
}

func TestOldVaultHostsClosed(t *testing.T) {
	var d model.VaultData
	old := `{"hosts":[{"id":"h","name":"n","address":"a","port":22,"user":"u"}],"settings":{"terminal_size":13}}`
	if err := json.Unmarshal([]byte(old), &d); err != nil {
		t.Fatal(err)
	}
	if d.Hosts[0].MCPAccess != model.MCPAccessClosed || d.Settings.MCPPort != 0 || d.Settings.MCPEnabled {
		t.Fatalf("data = %+v", d)
	}
	s := newTestStore(t)
	if err := s.SaveMCP(false, 0, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().MCPPort; got != model.DefaultMCPPort {
		t.Fatalf("zero port read as %d", got)
	}
}

func TestSaveMCP(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveMCP(true, 5000, "tok"); err != nil {
		t.Fatal(err)
	}
	set := s.Settings()
	if !set.MCPEnabled || set.MCPPort != 5000 || set.MCPToken != "tok" {
		t.Fatalf("settings = %+v", set)
	}
}
