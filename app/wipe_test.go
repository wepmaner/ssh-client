package app

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

func allZero(b []byte) bool { return len(bytes.Trim(b, "\x00")) == 0 }

func TestInputQueueWipesConsumedBytes(t *testing.T) {
	q := newInputQueue()
	q.Write([]byte("s3cret\n"))
	backing := q.buf
	p := make([]byte, 3)
	if n, err := q.Read(p); n != 3 || err != nil || string(p) != "s3c" {
		t.Fatalf("read %q n=%d err=%v", p, n, err)
	}
	if !allZero(backing[:3]) || string(backing[3:]) != "ret\n" {
		t.Fatalf("queue backing %q", backing)
	}
}

func TestDialTargetWipesTheKeyCopy(t *testing.T) {
	kh, err := sshclient.OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	// A closed port: Dial returns at once, the key copy is wiped all the same.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close() //nolint:gosec // G104: the port only has to be closed
	k := model.Key{ID: "k", Name: "work", Format: "openssh", Private: []byte("-----BEGIN not a key-----")}
	tg := sshclient.Target{Address: "127.0.0.1", Port: addr.Port, User: "u", Key: &k}
	if _, err := dialTarget(context.Background(), tg, sshclient.NewPrompter(io.Discard, strings.NewReader("")), kh); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if !allZero(k.Private) {
		t.Fatalf("key copy not wiped: %q", k.Private)
	}
}

func TestWriteKeyFileWipesThePlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519")
	data := []byte("private key")
	if err := writeKeyFile(path, data); err != nil {
		t.Fatal(err)
	}
	if !allZero(data) {
		t.Fatalf("buffer not wiped: %q", data)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "private key" { //nolint:gosec // G304: test temp path
		t.Fatalf("file %q, %v", got, err)
	}
}

func TestKeyListingsCarryNoPrivateBytes(t *testing.T) {
	a := newKeysApp(t)
	k, err := a.storeRef().SaveKey(model.Key{Name: "work", Private: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := a.keyMeta(k.ID)
	if err != nil || meta.Private != nil {
		t.Fatalf("meta %+v, %v", meta, err)
	}
	full, err := a.findKey(k.ID)
	if err != nil || string(full.Private) != "secret" {
		t.Fatalf("key %+v, %v", full, err)
	}
	clear(full.Private)
	if again, err := a.findKey(k.ID); err != nil || string(again.Private) != "secret" {
		t.Fatal("findKey handed out the live bytes")
	}
}
