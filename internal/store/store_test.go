package store

import (
	"errors"
	"testing"

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
