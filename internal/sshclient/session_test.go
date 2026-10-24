package sshclient

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func sessionTest(t *testing.T, opts testServerOpts) *Session {
	t.Helper()
	key, pub := testClientKey(t, "work", "")
	opts.AcceptKey = pub
	c, _, err := dialTest(t, opts, key, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(c, 80, 24, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignoreErr(s.Close()) })
	return s
}

// readUntil читает Output, пока накопленный вывод не содержит want.
func readUntil(t *testing.T, s *Session, want string) string {
	t.Helper()
	var got strings.Builder
	timeout := time.After(5 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case b, ok := <-s.Output():
			if !ok {
				t.Fatalf("output closed, got %q, want %q", got.String(), want)
			}
			got.Write(b)
		case <-timeout:
			t.Fatalf("timeout, got %q, want %q", got.String(), want)
		}
	}
	return got.String()
}

// waitClosed дочитывает Output до закрытия канала.
func waitClosed(t *testing.T, s *Session) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.Output():
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("output channel not closed")
		}
	}
}

func TestSessionEchoAndResize(t *testing.T) {
	type size struct {
		term       string
		cols, rows uint32
	}
	pty := make(chan size, 1)
	resized := make(chan size, 1)
	s := sessionTest(t, testServerOpts{
		OnPty:          func(term string, cols, rows uint32) { pty <- size{term, cols, rows} },
		OnWindowChange: func(cols, rows uint32) { resized <- size{"", cols, rows} },
	})
	if got := <-pty; got != (size{"xterm-256color", 80, 24}) {
		t.Fatalf("pty-req = %+v", got)
	}
	readUntil(t, s, "ok\r\n")
	if err := s.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, s, "ls")
	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resized:
		if got.cols != 100 || got.rows != 30 {
			t.Fatalf("window-change = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no window-change")
	}
}

func TestSessionExitCode(t *testing.T) {
	s := sessionTest(t, testServerOpts{ExitCode: 3})
	readUntil(t, s, "ok\r\n")
	err := s.Wait()
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitStatus() != 3 {
		t.Fatalf("err = %v", err)
	}
	waitClosed(t, s)
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	s := sessionTest(t, testServerOpts{})
	readUntil(t, s, "ok\r\n")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	waitClosed(t, s)
	if err := s.Write([]byte("x")); err == nil {
		t.Fatal("Write after Close succeeded")
	}
}

func TestSessionCloseWhileOutputUnread(t *testing.T) {
	s := sessionTest(t, testServerOpts{})
	// Никто не читает Output: насос должен выйти по Close, а не зависнуть на отправке.
	for range 50 {
		if err := s.Write([]byte(strings.Repeat("x", 1024))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, s)
}
