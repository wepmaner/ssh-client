package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/sshclient/sshtest"
)

// fakeEmitter records events in order instead of sending them to the WebView.
type fakeEmitter struct {
	mu     sync.Mutex
	events []fakeEvent
	notify chan struct{}
}

type fakeEvent struct {
	name    string
	payload any
}

func newFakeEmitter() *fakeEmitter { return &fakeEmitter{notify: make(chan struct{}, 1)} }

func (f *fakeEmitter) Emit(name string, payload any) {
	f.mu.Lock()
	f.events = append(f.events, fakeEvent{name, payload})
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeEmitter) snapshot() []fakeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeEvent(nil), f.events...)
}

// waitFor polls the recorded events until ok returns true.
func (f *fakeEmitter) waitFor(t *testing.T, timeout time.Duration, what string, ok func([]fakeEvent) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if ok(f.snapshot()) {
			return
		}
		select {
		case <-f.notify:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timeout waiting for %s; events: %s", what, f.describe())
		}
	}
}

func (f *fakeEmitter) states(id string) []SessionStateDTO {
	var out []SessionStateDTO
	for _, e := range f.snapshot() {
		if e.name == "term:state:"+id {
			out = append(out, e.payload.(SessionStateDTO))
		}
	}
	return out
}

func (f *fakeEmitter) data(t *testing.T, id string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range f.snapshot() {
		if e.name == "term:data:"+id {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

func (f *fakeEmitter) describe() string {
	var b strings.Builder
	for _, e := range f.snapshot() {
		switch p := e.payload.(type) {
		case string:
			raw, err := base64.StdEncoding.DecodeString(p)
			if err != nil {
				raw = []byte("<bad base64>")
			}
			fmt.Fprintf(&b, "%s %q\n", e.name, raw)
		case SessionStateDTO:
			fmt.Fprintf(&b, "%s %s %s %q\n", e.name, p.State, p.Kind, p.Message)
		}
	}
	return b.String()
}

func (f *fakeEmitter) waitState(t *testing.T, id, state string, timeout time.Duration) SessionStateDTO {
	t.Helper()
	var got SessionStateDTO
	f.waitFor(t, timeout, "state "+state, func([]fakeEvent) bool {
		for _, s := range f.states(id) {
			if s.State == state {
				got = s
				return true
			}
		}
		return false
	})
	return got
}

func (f *fakeEmitter) waitData(t *testing.T, id, want string) {
	t.Helper()
	f.waitFor(t, 5*time.Second, "data "+strconv.Quote(want), func([]fakeEvent) bool {
		return strings.Contains(f.data(t, id), want)
	})
}

// sessionsApp is an App with a temp vault, a fake emitter and a host pointing at a test server.
type sessionsApp struct {
	*App
	ev     *fakeEmitter
	hostID string
	srv    *sshtest.Server
}

// newSessionsApp starts a test server, trusts its host key and saves a host for it.
// withKey stores a client key accepted by the server and links it to the host.
func newSessionsApp(t *testing.T, opts sshtest.Opts, withKey bool) sessionsApp {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("XDG_CACHE_HOME", tmp) // os.UserCacheDir on Linux
	a := &App{}
	a.openVault()
	if !a.storeUnlocked() {
		t.Fatal("vault not open")
	}
	ev := newFakeEmitter()
	a.emit = ev

	// Without withKey the host has no key: only the password and keyboard-interactive prompts.
	var keyID string
	if withKey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		block, err := ssh.MarshalPrivateKey(priv, "")
		if err != nil {
			t.Fatal(err)
		}
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		opts.AcceptKey = sshPub
		k, err := a.store.SaveKey(model.Key{Name: "work", Type: "ed25519", Private: pem.EncodeToMemory(block), Format: "openssh"})
		if err != nil {
			t.Fatal(err)
		}
		keyID = k.ID
	}

	srv := sshtest.StartServer(t, opts)
	addr, hostKey := srv.Addr(), srv.HostKey()
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, hostKey) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.store.SaveHost(model.Host{Name: "srv", Address: host, Port: n, User: "alice", KeyID: keyID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeAllSessions)
	return sessionsApp{App: a, ev: ev, hostID: h.ID, srv: srv}
}

// open opens a session and attaches the view the way TerminalView does: by reporting its size.
func (s sessionsApp) open(t *testing.T) string {
	t.Helper()
	id, err := s.SessionOpen(s.hostID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionResize(id, 100, 30); err != nil {
		t.Fatal(err)
	}
	return id
}

func (s sessionsApp) write(t *testing.T, id, text string) {
	t.Helper()
	if err := s.SessionWrite(id, base64.StdEncoding.EncodeToString([]byte(text))); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	type size struct{ cols, rows uint32 }
	pty := make(chan size, 1)
	resized := make(chan size, 4)
	s := newSessionsApp(t, sshtest.Opts{
		OnPty:          func(_ string, c, r uint32) { pty <- size{c, r} },
		OnWindowChange: func(c, r uint32) { resized <- size{c, r} },
	}, true)

	id := s.open(t)
	s.ev.waitState(t, id, "connected", 5*time.Second)
	if got := <-pty; got != (size{100, 30}) {
		t.Fatalf("pty size %v, want the attached view size 100x30", got)
	}
	states := s.ev.states(id)
	if len(states) != 2 || states[0].State != "connecting" || states[1].State != "connected" {
		t.Fatalf("states %+v", states)
	}
	if !strings.HasPrefix(s.ev.data(t, id), "Подключение к alice@127.0.0.1:") {
		t.Fatalf("banner: %q", s.ev.data(t, id))
	}
	s.ev.waitData(t, id, "ok\r\n")

	list := s.SessionList()
	if len(list) != 1 || list[0].ID != id || list[0].HostID != s.hostID || list[0].HostName != "srv" || list[0].State != "connected" {
		t.Fatalf("list %+v", list)
	}

	s.write(t, id, "hi\r")
	s.ev.waitData(t, id, "ok\r\nhi\r")

	if err := s.SessionResize(id, 132, 43); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resized:
		if got != (size{132, 43}) {
			t.Fatalf("window-change %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window-change not received")
	}

	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	st := s.ev.waitState(t, id, "closed", 5*time.Second)
	if st.Kind != "" || st.Message != "" {
		t.Fatalf("closed payload %+v", st)
	}
	if len(s.SessionList()) != 0 {
		t.Fatal("closed session still listed")
	}
	if err := s.SessionWrite(id, ""); err == nil {
		t.Fatal("write to a closed session succeeded")
	}
}

func TestSessionExitCode(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{ExitCode: 3}, true)
	id := s.open(t)
	st := s.ev.waitState(t, id, "closed", 5*time.Second)
	if st.ExitCode == nil || *st.ExitCode != 3 {
		t.Fatalf("exit code %+v", st)
	}
	if !strings.Contains(s.ev.data(t, id), "ok\r\n") {
		t.Fatalf("output flushed before close: %q", s.ev.data(t, id))
	}
	if l := s.SessionList(); len(l) != 1 || l[0].State != "closed" {
		t.Fatalf("finished session should stay listed until its tab closes: %+v", l)
	}
}

func TestSessionWrongPassword(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{Password: "s3cret"}, false)
	id := s.open(t)
	for i, pw := range []string{"a\r", "b\r", "c\r"} {
		s.ev.waitFor(t, 5*time.Second, "password prompt", func([]fakeEvent) bool {
			return strings.Count(s.ev.data(t, id), "Пароль alice@") > i
		})
		s.write(t, id, pw)
	}
	st := s.ev.waitState(t, id, "error", 10*time.Second)
	if st.Kind != "auth" || !st.Retryable || st.Message != "Доступ запрещён: сервер отклонил ключ и пароль" || st.Detail == "" {
		t.Fatalf("error payload %+v", st)
	}
}

func TestSessionCloseDuringPrompt(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{Password: "s3cret"}, false)
	id := s.open(t)
	s.ev.waitData(t, id, "Пароль alice@")
	start := time.Now()
	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	st := s.ev.waitState(t, id, "error", 2*time.Second)
	if st.Kind != "cancelled" {
		t.Fatalf("payload %+v", st)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("cancel took %v", d)
	}
}

func TestSessionPromptInputKeepsOrderAndPending(t *testing.T) {
	// The password and the first shell keystrokes are typed in one burst before the shell starts.
	s := newSessionsApp(t, sshtest.Opts{Password: "s3cret"}, false)
	id := s.open(t)
	s.ev.waitData(t, id, "Пароль alice@")
	s.write(t, id, "s3cret\recho1")
	s.write(t, id, "2\r")
	s.ev.waitState(t, id, "connected", 5*time.Second)
	s.ev.waitData(t, id, "echo12\r")
	if strings.Contains(s.ev.data(t, id), "s3cret") {
		t.Fatal("password echoed")
	}
}

func TestSessionOpenErrors(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{}, false)
	if _, err := s.SessionOpen("nope", 80, 24); err == nil || err.Error() != "Хост не найден" {
		t.Fatalf("unknown host: %v", err)
	}
	if err := s.SessionWrite("nope", ""); err == nil {
		t.Fatal("write to unknown session")
	}
	if err := s.SessionResize("nope", 1, 1); err == nil {
		t.Fatal("resize unknown session")
	}
	if err := s.SessionClose("nope"); err == nil {
		t.Fatal("close unknown session")
	}
	id := s.open(t)
	if err := s.SessionWrite(id, "%%%"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	locked := &App{}
	if _, err := locked.SessionOpen("x", 80, 24); err == nil || err.Error() != "Хранилище заблокировано" {
		t.Fatalf("locked: %v", err)
	}
}

func TestErrorStateKinds(t *testing.T) {
	for _, tc := range []struct {
		err       error
		kind      string
		retryable bool
	}{
		{fmt.Errorf("x: %w", sshclient.ErrTimeout), "timeout", true},
		{fmt.Errorf("x: %w", sshclient.ErrHostKeyChanged), "hostkey", false},
		{fmt.Errorf("x: %w", sshclient.ErrAuthDenied), "auth", true},
		{sshclient.ErrCancelled, "cancelled", true},
		{fmt.Errorf("подключение: %w", context.Canceled), "cancelled", true},
		{errors.New("connection refused"), "other", true},
	} {
		st := errorState(tc.err)
		if st.State != "error" || st.Kind != tc.kind || st.Retryable != tc.retryable {
			t.Errorf("%v: %+v", tc.err, st)
		}
	}
	st := errorState(fmt.Errorf("Сервер не ответил: %w", errors.Join(sshclient.ErrTimeout, errors.New("i/o timeout"))))
	if !strings.Contains(st.Detail, "i/o timeout") || !strings.HasPrefix(st.Detail, st.Message) {
		t.Fatalf("detail %q", st.Detail)
	}
}

func TestOutStreamBatchesAndHoldsUntilAttached(t *testing.T) {
	ev := newFakeEmitter()
	o := newOutStream(ev, "s")
	mustWrite(t, o, "a")
	o.Event("term:state:s", SessionStateDTO{State: "connecting"}, false)
	mustWrite(t, o, "b")
	time.Sleep(3 * flushInterval)
	if n := len(ev.snapshot()); n != 0 {
		t.Fatalf("emitted %d events before attach", n)
	}
	o.Attach()
	for range 5 {
		mustWrite(t, o, "c")
	}
	ev.waitData(t, "s", "abccccc")
	got := ev.snapshot()
	if len(got) != 4 || got[0].name != "term:data:s" || got[1].name != "term:state:s" || got[2].payload != "Yg==" {
		t.Fatalf("events %s", ev.describe())
	}
	if got[3].payload != base64.StdEncoding.EncodeToString([]byte("ccccc")) {
		t.Fatalf("writes within one interval were not batched: %s", ev.describe())
	}
	o.Event("term:state:s", SessionStateDTO{State: "closed"}, true)
	mustWrite(t, o, "late")
	time.Sleep(3 * flushInterval)
	if strings.Contains(ev.data(t, "s"), "late") {
		t.Fatal("write after the last event was emitted")
	}
}

func mustWrite(t *testing.T, o *outStream, text string) {
	t.Helper()
	if _, err := o.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
}

func TestOutStreamCapsHeldOutput(t *testing.T) {
	old := maxHeldBytes
	maxHeldBytes = 10
	t.Cleanup(func() { maxHeldBytes = old })

	ev := newFakeEmitter()
	o := newOutStream(ev, "s")
	mustWrite(t, o, "0123456789")
	o.Event("term:state:s", SessionStateDTO{State: "connecting"}, false)
	mustWrite(t, o, "abcdef")
	o.Attach()
	got := ev.snapshot()
	if len(got) != 4 || got[2].name != "term:state:s" {
		t.Fatalf("events %s", ev.describe())
	}
	if want := droppedMarker + "6789abcdef"; ev.data(t, "s") != want {
		t.Fatalf("data %q, want %q", ev.data(t, "s"), want)
	}
}

func TestOutStreamUnderCapKeepsEverything(t *testing.T) {
	ev := newFakeEmitter()
	o := newOutStream(ev, "s")
	mustWrite(t, o, "abc")
	o.Attach()
	if ev.data(t, "s") != "abc" {
		t.Fatalf("data %q", ev.data(t, "s"))
	}
}

// A host without a key (an imported one, or «Без ключа» in the dialog) offers no key: the
// server asks for the password in the console and the login works.
func TestHostWithoutKeyLogsInWithPassword(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{Password: "s3cret"}, false)
	if l := s.HostsList(); len(l) != 1 || l[0].KeyID != "" || l[0].KeyName != "" {
		t.Fatalf("list %+v", l)
	}
	if _, tgt, err := s.hostTarget(s.hostID); err != nil || tgt.Key != nil {
		t.Fatalf("target key %+v, %v", tgt.Key, err)
	}
	id := s.open(t)
	s.ev.waitData(t, id, "Пароль alice@")
	if strings.Contains(s.ev.data(t, id), "Парольная фраза") {
		t.Fatal("a keyless host asked for a key passphrase")
	}
	s.write(t, id, "s3cret\r")
	s.ev.waitState(t, id, "connected", 5*time.Second)
}
