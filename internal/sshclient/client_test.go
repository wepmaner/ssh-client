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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshclient/internal/keys"
	"sshclient/internal/model"
	"sshclient/internal/sshclient/sshtest"
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
	k := &model.Key{Name: name, Type: "ed25519", Private: pem.EncodeToMemory(block), Format: "openssh", Encrypted: passphrase != ""}
	k.Fingerprint = ssh.FingerprintSHA256(sshPub)
	return k, sshPub
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

func dialTest(t *testing.T, opts sshtest.Opts, key *model.Key, in io.Reader) (*ssh.Client, *bytes.Buffer, error) {
	t.Helper()
	addr, hostKey := sshtest.Start(t, opts)
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), targetFor(t, addr, key), NewPrompter(out, in), trustedKnownHosts(t, addr, hostKey))
	if c != nil {
		t.Cleanup(func() { ignoreErr(c.Close()) })
	}
	return c, out, err
}

func TestDialWithKey(t *testing.T) {
	key, pub := testClientKey(t, "work", "")
	c, out, err := dialTest(t, sshtest.Opts{AcceptKey: pub}, key, strings.NewReader(""))
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
	_, out, err := dialTest(t, sshtest.Opts{AcceptKey: pub}, key, strings.NewReader("wrong\nright\n"))
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

func TestDialEncryptedPPKAsksInTerminal(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "keys", "testdata", "v3.ppk"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := keys.Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	key.Name = "putty"
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.PublicLine))
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := dialTest(t, sshtest.Opts{AcceptKey: pub}, &key, strings.NewReader("nope\ntestkey\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "Парольная фраза ключа putty: "); n != 2 {
		t.Fatalf("prompt printed %d times: %q", n, out.String())
	}
	if strings.Contains(out.String(), "testkey") {
		t.Fatalf("passphrase echoed: %q", out.String())
	}
	if !bytes.Equal(key.Private, data) {
		t.Fatal("the stored key must stay encrypted")
	}
}

func TestDialFallsBackToPassword(t *testing.T) {
	key, _ := testClientKey(t, "work", "")
	_, out, err := dialTest(t, sshtest.Opts{Password: "s3cret"}, key, strings.NewReader("s3cret\n"))
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
	_, out, err := dialTest(t, sshtest.Opts{Password: "s3cret"}, nil, strings.NewReader("a\nb\nc\n"))
	if err == nil || err.Error() != "Доступ запрещён: сервер отклонил ключ и пароль" || !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(out.String(), "Пароль alice@127.0.0.1: "); n != 3 {
		t.Fatalf("password asked %d times: %q", n, out.String())
	}
}

func TestDialKeyboardInteractive(t *testing.T) {
	_, out, err := dialTest(t, sshtest.Opts{KBIAnswer: "123456"}, nil, strings.NewReader("123456\n"))
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
	_, out, err := dialTest(t, sshtest.Opts{Password: "s3cret"}, nil, strings.NewReader("\x03"))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if n := strings.Count(out.String(), "Пароль"); n != 1 {
		t.Fatalf("password asked %d times: %q", n, out.String())
	}
}

func TestDialCancelStopsFurtherMethods(t *testing.T) {
	// Ctrl+C на вопросе keyboard-interactive: пароль после этого уже не спрашивается.
	_, out, err := dialTest(t, sshtest.Opts{Password: "s3cret", KBIAnswer: "1"}, nil, strings.NewReader("\x03"))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "Код: ") || strings.Contains(out.String(), "Пароль") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestDialChangedHostKeyRefused(t *testing.T) {
	key, pub := testClientKey(t, "work", "")
	addr, _ := sshtest.Start(t, sshtest.Opts{AcceptKey: pub})
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
	_, _, err := dialTest(t, sshtest.Opts{Password: "s3cret"}, nil, &slowReader{delay: 2 * time.Second, r: strings.NewReader("s3cret\n")})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDialServerDisconnectsAfterMaxAuthTries(t *testing.T) {
	// Сервер рвёт соединение после двух неудач раньше, чем клиент исчерпает свои попытки.
	key, _ := testClientKey(t, "work", "")
	_, _, err := dialTest(t, sshtest.Opts{Password: "s3cret", MaxAuthTries: 2}, key, strings.NewReader("a\nb\nc\n"))
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
	_, _, err := dialTest(t, sshtest.Opts{Password: "s3cret"}, nil, &slowReader{delay: time.Second, r: strings.NewReader("s3cret\n")})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, ErrHandshakeCap) || err.Error() != "Подключение не завершилось за 10 минут" {
		t.Fatalf("message = %q", err.Error())
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
}

// ignoreErr явно отбрасывает ошибку закрытия в тестах: проверять её нечем и незачем.
func ignoreErr(error) {}

// slowAsker отвечает answer после задержки — как пользователь, который долго сверяет отпечаток.
type slowAsker struct {
	delay  time.Duration
	answer bool
	asked  chan TrustQuestion
}

func (s *slowAsker) AskTrust(_ context.Context, q TrustQuestion) (bool, error) {
	s.asked <- q
	time.Sleep(s.delay)
	return s.answer, nil
}

func TestDialTrustAskerPausesDeadline(t *testing.T) {
	setHandshakeTimeout(t, time.Second)
	key, pub := testClientKey(t, "work", "")
	addr, hostKey := sshtest.Start(t, sshtest.Opts{AcceptKey: pub})
	path := writeKnown(t, "")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &slowAsker{delay: 2 * time.Second, answer: true, asked: make(chan TrustQuestion, 1)}
	tg := targetFor(t, addr, key)
	tg.Trust = a
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), tg, NewPrompter(out, strings.NewReader("")), kh)
	if err != nil {
		t.Fatal(err)
	}
	ignoreErr(c.Close())
	if q := <-a.asked; q.FingerprintSHA256 != ssh.FingerprintSHA256(hostKey) || q.Port != tg.Port {
		t.Fatalf("question %+v", q)
	}
	if out.Len() != 0 {
		t.Fatalf("console asked although a TrustAsker is set: %q", out.String())
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test temp file
	if err != nil || string(data) != knownhosts.Line([]string{knownhosts.Normalize(addr)}, hostKey)+"\n" {
		t.Fatalf("known_hosts = %q, %v", data, err)
	}
}

func TestDialHostNotTrusted(t *testing.T) {
	key, pub := testClientKey(t, "work", "")
	addr, _ := sshtest.Start(t, sshtest.Opts{AcceptKey: pub})
	path := writeKnown(t, "")
	kh, err := OpenKnownHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	tg := targetFor(t, addr, key)
	tg.Trust = &slowAsker{answer: false, asked: make(chan TrustQuestion, 1)}
	c, err := Dial(context.Background(), tg, NewPrompter(io.Discard, strings.NewReader("")), kh)
	if c != nil {
		ignoreErr(c.Close())
		t.Fatal("connected to an untrusted server")
	}
	if !errors.Is(err, ErrHostNotTrusted) || err.Error() != "Подключение отменено: сервер не подтверждён" {
		t.Fatalf("err = %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) != 0 { //nolint:gosec // G304: path is a test temp file
		t.Fatalf("known_hosts = %q, %v", data, err)
	}
}

// passphraseRecorder собирает копии фраз, переданных в OnPassphrase, и сами буферы Dial,
// чтобы проверить, что Dial их затёр.
type passphraseRecorder struct {
	copies  [][]byte
	buffers [][]byte
}

func (r *passphraseRecorder) on(p []byte) {
	r.copies = append(r.copies, append([]byte(nil), p...))
	r.buffers = append(r.buffers, p)
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func dialWithPassphrase(t *testing.T, opts sshtest.Opts, key *model.Key, cached []byte, in string) (*bytes.Buffer, *passphraseRecorder, error) {
	t.Helper()
	addr, hostKey := sshtest.Start(t, opts)
	rec := &passphraseRecorder{}
	tg := targetFor(t, addr, key)
	tg.Passphrase = cached
	tg.OnPassphrase = rec.on
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), tg, NewPrompter(out, strings.NewReader(in)), trustedKnownHosts(t, addr, hostKey))
	if c != nil {
		ignoreErr(c.Close())
	}
	return out, rec, err
}

func TestDialUsesGivenPassphraseWithoutPrompt(t *testing.T) {
	key, pub := testClientKey(t, "work", "right")
	cached := []byte("right")
	out, rec, err := dialWithPassphrase(t, sshtest.Opts{AcceptKey: pub}, key, cached, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("asked although the passphrase was given: %q", out.String())
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "right" {
		t.Fatalf("OnPassphrase got %q", rec.copies)
	}
	if !allZero(cached) || !allZero(rec.buffers[0]) {
		t.Fatalf("buffers not zeroed: cached %q, callback %q", cached, rec.buffers[0])
	}
}

func TestDialFallsBackToPromptOnWrongCachedPassphrase(t *testing.T) {
	key, pub := testClientKey(t, "work", "right")
	cached := []byte("stale")
	out, rec, err := dialWithPassphrase(t, sshtest.Opts{AcceptKey: pub}, key, cached, "right\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "Парольная фраза ключа work: "); n != 1 {
		t.Fatalf("prompt printed %d times: %q", n, out.String())
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "right" {
		t.Fatalf("OnPassphrase got %q", rec.copies)
	}
	if !allZero(cached) || !allZero(rec.buffers[0]) {
		t.Fatalf("buffers not zeroed: cached %q, callback %q", cached, rec.buffers[0])
	}
}

func TestDialZeroesPassphraseBuffers(t *testing.T) {
	t.Run("failed dial", func(t *testing.T) {
		key, pub := testClientKey(t, "work", "right")
		cached := []byte("stale")
		_, rec, err := dialWithPassphrase(t, sshtest.Opts{AcceptKey: pub}, key, cached, "")
		if err == nil {
			t.Fatal("connected without the right passphrase")
		}
		if !allZero(cached) {
			t.Fatalf("cached passphrase not zeroed: %q", cached)
		}
		if len(rec.copies) != 0 {
			t.Fatalf("OnPassphrase called on failure: %q", rec.copies)
		}
	})
	t.Run("unencrypted key", func(t *testing.T) {
		key, pub := testClientKey(t, "work", "")
		cached := []byte("unused")
		_, rec, err := dialWithPassphrase(t, sshtest.Opts{AcceptKey: pub}, key, cached, "")
		if err != nil {
			t.Fatal(err)
		}
		if !allZero(cached) || len(rec.copies) != 0 {
			t.Fatalf("cached %q, OnPassphrase %q", cached, rec.copies)
		}
	})
	t.Run("prompt cancelled", func(t *testing.T) {
		key, pub := testClientKey(t, "work", "right")
		cached := []byte("stale")
		_, rec, err := dialWithPassphrase(t, sshtest.Opts{AcceptKey: pub}, key, cached, "\x03")
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("err = %v", err)
		}
		if !allZero(cached) || len(rec.copies) != 0 {
			t.Fatalf("cached %q, OnPassphrase %q", cached, rec.copies)
		}
	})
	t.Run("context cancelled during prompt", func(t *testing.T) {
		key, pub := testClientKey(t, "work", "right")
		addr, hostKey := sshtest.Start(t, sshtest.Opts{AcceptKey: pub})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pr, pw := io.Pipe() // the user never answers
		stop := time.AfterFunc(300*time.Millisecond, func() { cancel(); ignoreErr(pw.Close()) })
		defer stop.Stop()
		cached := []byte("stale")
		tg := targetFor(t, addr, key)
		tg.Passphrase = cached
		called := false
		tg.OnPassphrase = func([]byte) { called = true }
		c, err := Dial(ctx, tg, NewPrompter(io.Discard, pr), trustedKnownHosts(t, addr, hostKey))
		if c != nil {
			ignoreErr(c.Close())
			t.Fatal("connected without an answer")
		}
		if err == nil || !allZero(cached) || called {
			t.Fatalf("err %v, cached %q, OnPassphrase called %v", err, cached, called)
		}
	})
	t.Run("unreachable server", func(t *testing.T) {
		key, _ := testClientKey(t, "work", "right")
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ignoreErr(ln.Close())
		cached := []byte("right")
		tg := targetFor(t, addr, key)
		tg.Passphrase = cached
		if _, err := Dial(context.Background(), tg, NewPrompter(io.Discard, strings.NewReader("")), trustedKnownHosts(t, addr, testKey(t))); err == nil {
			t.Fatal("dial to a closed port succeeded")
		}
		if !allZero(cached) {
			t.Fatalf("cached passphrase not zeroed: %q", cached)
		}
	})
}

func TestDialOnPassphraseOnlyWhenServerAcceptsKey(t *testing.T) {
	// Фраза верная, но сервер не принимает этот ключ и пускает по паролю: фраза не возвращается.
	key, _ := testClientKey(t, "work", "right")
	cached := []byte("right")
	_, rec, err := dialWithPassphrase(t, sshtest.Opts{Password: "s3cret"}, key, cached, "s3cret\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.copies) != 0 {
		t.Fatalf("OnPassphrase called although the server rejected the key: %q", rec.copies)
	}
	if !allZero(cached) {
		t.Fatalf("cached passphrase not zeroed: %q", cached)
	}
}

func TestPassphraseSignersSecondCallAsks(t *testing.T) {
	// После частичного успеха publickey x/crypto может вызвать колбэк ключа снова: кэшированная
	// фраза уже потрачена и затёрта, второй вызов сразу задаёт вопрос.
	key, _ := testClientKey(t, "work", "right")
	out := &bytes.Buffer{}
	d := &dialState{p: NewPrompter(out, strings.NewReader("right\n"))}
	c1, c2 := net.Pipe()
	t.Cleanup(func() { ignoreErr(c1.Close()); ignoreErr(c2.Close()) })
	d.conn = c1
	defer d.forgetSecrets()
	cached := []byte("right")
	signers := d.passphraseSigners(key, cached)

	if s, err := signers(); err != nil || len(s) != 1 {
		t.Fatalf("first call: %v, %v", s, err)
	}
	if out.Len() != 0 || !allZero(cached) {
		t.Fatalf("first call asked %q or kept cached %q", out.String(), cached)
	}
	if s, err := signers(); err != nil || len(s) != 1 {
		t.Fatalf("second call: %v, %v", s, err)
	}
	if n := strings.Count(out.String(), "Парольная фраза ключа work: "); n != 1 {
		t.Fatalf("second call asked %d times: %q", n, out.String())
	}
}

// dialWithPassword — как dialWithPassphrase, но для пароля: возвращает вывод Prompter,
// копии паролей, переданных в OnPassword, и буфер Target.Password.
func dialWithPassword(t *testing.T, opts sshtest.Opts, remembered []byte, in string) (*bytes.Buffer, *passphraseRecorder, error) {
	t.Helper()
	addr, hostKey := sshtest.Start(t, opts)
	rec := &passphraseRecorder{}
	tg := targetFor(t, addr, nil)
	tg.Password = remembered
	tg.OnPassword = rec.on
	out := &bytes.Buffer{}
	c, err := Dial(context.Background(), tg, NewPrompter(out, strings.NewReader(in)), trustedKnownHosts(t, addr, hostKey))
	if c != nil {
		ignoreErr(c.Close())
	}
	return out, rec, err
}

func TestDialPasswordFromMemoryNoPrompt(t *testing.T) {
	pw := []byte("s3cret")
	out, rec, err := dialWithPassword(t, sshtest.Opts{Password: "s3cret"}, pw, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Пароль") {
		t.Fatalf("asked although the password was given: %q", out.String())
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "s3cret" {
		t.Fatalf("OnPassword got %q", rec.copies)
	}
	if !allZero(pw) || !allZero(rec.buffers[0]) {
		t.Fatalf("buffers not zeroed: %q, %q", pw, rec.buffers[0])
	}
}

func TestDialKBIPasswordFromMemoryNoPrompt(t *testing.T) {
	pw := []byte("s3cret")
	out, rec, err := dialWithPassword(t, sshtest.Opts{KBIAnswer: "s3cret"}, pw, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Код") {
		t.Fatalf("asked although the password was given: %q", out.String())
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "s3cret" {
		t.Fatalf("OnPassword got %q", rec.copies)
	}
	if !allZero(pw) {
		t.Fatalf("buffer not zeroed: %q", pw)
	}
}

func TestDialOnPasswordGetsAcceptedSecret(t *testing.T) {
	_, rec, err := dialWithPassword(t, sshtest.Opts{Password: "s3cret"}, nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "s3cret" {
		t.Fatalf("OnPassword got %q", rec.copies)
	}
}

func TestDialWrongRememberedPasswordAsks(t *testing.T) {
	old := []byte("old")
	out, rec, err := dialWithPassword(t, sshtest.Opts{Password: "s3cret"}, old, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "Пароль alice@127.0.0.1: "); n != 1 {
		t.Fatalf("password asked %d times: %q", n, out.String())
	}
	if len(rec.copies) != 1 || string(rec.copies[0]) != "s3cret" {
		t.Fatalf("OnPassword got %q", rec.copies)
	}
	if !allZero(old) {
		t.Fatalf("buffer not zeroed: %q", old)
	}
}

func TestDialOnPasswordNotCalledOnFailure(t *testing.T) {
	_, rec, err := dialWithPassword(t, sshtest.Opts{Password: "s3cret"}, nil, "abc")
	if !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("err = %v", err)
	}
	if len(rec.copies) != 0 {
		t.Fatalf("OnPassword called: %q", rec.copies)
	}
}
