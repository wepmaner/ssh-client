package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshclient/internal/model"
)

// testClientKey создаёт ключ Ed25519; passphrase != "" шифрует PEM этой фразой.
func testClientKey(t *testing.T, name, passphrase string) (*model.Key, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return &model.Key{Name: name, Type: "ed25519", PrivatePEM: pem.EncodeToMemory(block)}, sshPub
}

// trustedKnownHosts возвращает known_hosts, в котором уже записан ключ сервера addr.
func trustedKnownHosts(t *testing.T, addr string, key ssh.PublicKey) *KnownHosts {
	t.Helper()
	kh, err := OpenKnownHosts(writeKnown(t, knownhosts.Line([]string{knownhosts.Normalize(addr)}, key)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return kh
}

func targetFor(t *testing.T, addr string, key *model.Key) Target {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return Target{Address: host, Port: n, User: "alice", Key: key}
}

func dialTest(t *testing.T, opts testServerOpts, key *model.Key, in io.Reader) (*ssh.Client, *bytes.Buffer, error) {
	t.Helper()
	addr, hostKey := startTestServer(t, opts)
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), targetFor(t, addr, key), NewPrompter(out, in), trustedKnownHosts(t, addr, hostKey))
	if c != nil {
		t.Cleanup(func() { ignoreErr(c.Close()) })
	}
	return c, out, err
}

func TestDialWithKey(t *testing.T) {
	key, pub := testClientKey(t, "work", "")
	c, out, err := dialTest(t, testServerOpts{AcceptKey: pub}, key, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if c.User() != "alice" {
		t.Fatalf("user = %q", c.User())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected prompts: %q", out.String())
	}
}

func TestDialPassphraseKeyAsksInTerminal(t *testing.T) {
	key, pub := testClientKey(t, "work", "right")
	_, out, err := dialTest(t, testServerOpts{AcceptKey: pub}, key, strings.NewReader("wrong\nright\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "Парольная фраза ключа work: "); n != 2 {
		t.Fatalf("prompt printed %d times: %q", n, out.String())
	}
	if strings.Contains(out.String(), "right") || strings.Contains(out.String(), "wrong") {
		t.Fatalf("passphrase echoed: %q", out.String())
	}
}

func TestDialFallsBackToPassword(t *testing.T) {
	key, _ := testClientKey(t, "work", "")
	_, out, err := dialTest(t, testServerOpts{Password: "s3cret"}, key, strings.NewReader("s3cret\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Пароль alice@127.0.0.1: ") {
		t.Fatalf("out = %q", out.String())
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Fatalf("password echoed: %q", out.String())
	}
}

func TestDialWrongPasswordRetriesThenDenied(t *testing.T) {
	_, out, err := dialTest(t, testServerOpts{Password: "s3cret"}, nil, strings.NewReader("a\nb\nc\n"))
	if err == nil || err.Error() != "Доступ запрещён: сервер отклонил ключ и пароль" || !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(out.String(), "Пароль alice@127.0.0.1: "); n != 3 {
		t.Fatalf("password asked %d times: %q", n, out.String())
	}
}

func TestDialKeyboardInteractive(t *testing.T) {
	_, out, err := dialTest(t, testServerOpts{KBIAnswer: "123456"}, nil, strings.NewReader("123456\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Введите одноразовый код\r\n") || !strings.Contains(out.String(), "Код: ") {
		t.Fatalf("out = %q", out.String())
	}
	if strings.Contains(out.String(), "123456") {
		t.Fatalf("answer echoed: %q", out.String())
	}
}

func TestDialCancelledAtPasswordPrompt(t *testing.T) {
	_, out, err := dialTest(t, testServerOpts{Password: "s3cret"}, nil, strings.NewReader("\x03"))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(out.String(), "Пароль"); n != 1 {
		t.Fatalf("password asked %d times: %q", n, out.String())
	}
}

func TestDialCancelStopsFurtherMethods(t *testing.T) {
	// Ctrl+C на вопросе keyboard-interactive: пароль после этого уже не спрашивается.
	_, out, err := dialTest(t, testServerOpts{Password: "s3cret", KBIAnswer: "1"}, nil, strings.NewReader("\x03"))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "Код: ") || strings.Contains(out.String(), "Пароль") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestDialChangedHostKeyRefused(t *testing.T) {
	key, pub := testClientKey(t, "work", "")
	addr, _ := startTestServer(t, testServerOpts{AcceptKey: pub})
	kh := trustedKnownHosts(t, addr, testKey(t)) // записан чужой ключ
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), targetFor(t, addr, key), NewPrompter(out, strings.NewReader("yes\n")), kh)
	if c != nil {
		ignoreErr(c.Close())
		t.Fatal("connection established despite changed host key")
	}
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("asked user: %q", out.String())
	}
}

// silentListener принимает соединения и молчит: баннер SSH не приходит никогда.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				ignoreErr(c.Close())
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() { ignoreErr(ln.Close()); <-done })
	return ln.Addr().String()
}

func setHandshakeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := handshakeTimeout
	handshakeTimeout = d
	t.Cleanup(func() { handshakeTimeout = old })
}

func TestDialTimeoutMessage(t *testing.T) {
	check := func(t *testing.T, err error, start time.Time) {
		t.Helper()
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v", err)
		}
		if err.Error() != "Сервер не ответил за 10 секунд" {
			t.Fatalf("message = %q", err.Error())
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("took %v", d)
		}
	}
	t.Run("handshake", func(t *testing.T) {
		setHandshakeTimeout(t, 300*time.Millisecond)
		addr := silentListener(t)
		start := time.Now()
		_, err := Dial(context.Background(), targetFor(t, addr, nil), NewPrompter(io.Discard, strings.NewReader("")), trustedKnownHosts(t, addr, testKey(t)))
		check(t, err, start)
	})
	t.Run("context", func(t *testing.T) {
		addr := silentListener(t)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := Dial(ctx, targetFor(t, addr, nil), NewPrompter(io.Discard, strings.NewReader("")), trustedKnownHosts(t, addr, testKey(t)))
		check(t, err, start)
	})
}

// slowReader отдаёт данные только после задержки — как пользователь, который долго вводит пароль.
type slowReader struct {
	delay time.Duration
	r     io.Reader
}

func (s *slowReader) Read(b []byte) (int, error) {
	time.Sleep(s.delay)
	return s.r.Read(b)
}

func TestDialPromptDoesNotCountTowardsTimeout(t *testing.T) {
	setHandshakeTimeout(t, time.Second)
	_, _, err := dialTest(t, testServerOpts{Password: "s3cret"}, nil, &slowReader{delay: 2 * time.Second, r: strings.NewReader("s3cret\n")})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDialServerDisconnectsAfterMaxAuthTries(t *testing.T) {
	// Сервер рвёт соединение после двух неудач раньше, чем клиент исчерпает свои попытки.
	key, _ := testClientKey(t, "work", "")
	_, _, err := dialTest(t, testServerOpts{Password: "s3cret", MaxAuthTries: 2}, key, strings.NewReader("a\nb\nc\n"))
	if err == nil || err.Error() != "Доступ запрещён: сервер отклонил ключ и пароль" || !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("err = %v", err)
	}
}

func TestDialKeyboardInteractiveWithoutQuestionsKeepsDeadline(t *testing.T) {
	// Раунд keyboard-interactive без вопросов не должен приостанавливать срок ожидания сервера.
	d := &dialState{p: NewPrompter(io.Discard, strings.NewReader(""))}
	c1, c2 := net.Pipe()
	t.Cleanup(func() { ignoreErr(c1.Close()); ignoreErr(c2.Close()) })
	d.conn = c1
	d.armDeadline()
	before := d.deadline
	answers, err := d.keyboardInteractive("", "Добро пожаловать", nil, nil)
	if err != nil || len(answers) != 0 {
		t.Fatalf("answers %q, err %v", answers, err)
	}
	if !d.deadline.Equal(before) {
		t.Fatalf("deadline changed: %v -> %v", before, d.deadline)
	}
}

func TestDialHandshakeWallTimeCapped(t *testing.T) {
	old := maxHandshakeTime
	maxHandshakeTime = 300 * time.Millisecond
	t.Cleanup(func() { maxHandshakeTime = old })
	start := time.Now()
	_, _, err := dialTest(t, testServerOpts{Password: "s3cret"}, nil, &slowReader{delay: time.Second, r: strings.NewReader("s3cret\n")})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}
