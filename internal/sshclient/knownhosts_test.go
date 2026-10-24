package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var testAddr = &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}

func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestOpenKnownHostsCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "known_hosts")
	if _, err := OpenKnownHosts(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownHostAddedAfterYes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	p, out := newTestPrompter("yes\n")
	cb := kh.Callback(p)
	if err := cb("example.com:22", testAddr, key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test temp file
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example.com") {
		t.Fatalf("file = %q", data)
	}
	for _, want := range []string{"Сервер example.com:22 неизвестен.\r\n", "Отпечаток " + ssh.FingerprintSHA256(key) + "\r\n", "Доверять и запомнить?"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out missing %q: %q", want, out.String())
		}
	}
	// Второй раз ключ известен: без вопросов.
	p2, out2 := newTestPrompter("")
	if err := kh.Callback(p2)("example.com:22", testAddr, key); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Fatalf("unexpected prompt: %q", out2.String())
	}
}

func TestUnknownHostRejectedAfterNo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := newTestPrompter("no\n")
	err = kh.Callback(p)("example.com:22", testAddr, testKey(t))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test temp file
	if err != nil || len(data) != 0 {
		t.Fatalf("file = %q, %v", data, err)
	}
}

func TestChangedKeyRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	old, fresh := testKey(t), testKey(t)
	line := knownhosts.Line([]string{"example.com:22"}, old) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	p, out := newTestPrompter("yes\n")
	err = kh.Callback(p)("example.com:22", testAddr, fresh)
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("prompt printed: %q", out.String())
	}
	for _, want := range []string{ssh.FingerprintSHA256(old), ssh.FingerprintSHA256(fresh), path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
