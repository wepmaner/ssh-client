package sshclient

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	for _, want := range []string{"Сервер example.com:22 неизвестен.\r\n", "Отпечаток " + ssh.FingerprintSHA256(key) + "\r\n", "Доверять и запомнить? "} {
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

func testRSAKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func writeKnown(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnknownHostNonDefaultPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	p, _ := newTestPrompter("y\n")
	if err := kh.Callback(p)("example.com:2222", testAddr, key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test temp file
	if err != nil || !strings.HasPrefix(string(data), "[example.com]:2222 ") {
		t.Fatalf("file = %q, %v", data, err)
	}
	p2, out := newTestPrompter("")
	if err := kh.Callback(p2)("example.com:2222", testAddr, key); err != nil || out.Len() != 0 {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

func TestDifferentKeyTypeRejectedAsChanged(t *testing.T) {
	path := writeKnown(t, knownhosts.Line([]string{"example.com"}, testRSAKey(t))+"\n")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	p, out := newTestPrompter("yes\n")
	err = kh.Callback(p)("example.com:22", testAddr, testKey(t))
	if !errors.Is(err, ErrHostKeyChanged) || out.Len() != 0 {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

func TestAppendAfterMissingTrailingNewline(t *testing.T) {
	first := testKey(t)
	path := writeKnown(t, knownhosts.Line([]string{"a.example"}, first))
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	second := testKey(t)
	p, _ := newTestPrompter("yes\n")
	if err := kh.Callback(p)("b.example:22", testAddr, second); err != nil {
		t.Fatal(err)
	}
	for host, key := range map[string]ssh.PublicKey{"a.example:22": first, "b.example:22": second} {
		p2, out := newTestPrompter("")
		if err := kh.Callback(p2)(host, testAddr, key); err != nil || out.Len() != 0 {
			t.Fatalf("%s: err=%v out=%q", host, err, out.String())
		}
	}
}

func TestManualEditTakesEffect(t *testing.T) {
	key := testKey(t)
	path := writeKnown(t, knownhosts.Line([]string{"example.com"}, key)+"\n")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p, out := newTestPrompter("no\n")
	if err := kh.Callback(p)("example.com:22", testAddr, key); !errors.Is(err, ErrCancelled) || out.Len() == 0 {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

func TestNilRemoteDoesNotPanic(t *testing.T) {
	kh, err := OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := newTestPrompter("")
	if err := kh.Callback(p)("example.com:22", nil, testKey(t)); err == nil {
		t.Fatal("expected error")
	}
}

func TestPendingPromptDoesNotBlockOtherHosts(t *testing.T) {
	known := testKey(t)
	path := writeKnown(t, knownhosts.Line([]string{"known.example"}, known)+"\n")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }() //nolint:gosec // test cleanup
	blocked := NewPrompter(&bytes.Buffer{}, pr)
	go func() { _ = kh.Callback(blocked)("new.example:22", testAddr, testKey(t)) }() //nolint:gosec // result is irrelevant: the prompt never gets an answer
	time.Sleep(50 * time.Millisecond)                                                // дать первому дойти до ожидания ответа

	done := make(chan error, 1)
	go func() {
		p, _ := newTestPrompter("")
		done <- kh.Callback(p)("known.example:22", testAddr, known)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("check of a trusted host blocked by a pending prompt")
	}
}
