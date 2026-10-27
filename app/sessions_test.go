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
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp) // os.UserConfigDir on Linux
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
		k, err := a.storeRef().SaveKey(model.Key{Name: "work", Type: "ed25519", Private: pem.EncodeToMemory(block), Format: "openssh"})
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
	h, err := a.storeRef().SaveHost(model.Host{Name: "srv", Address: host, Port: n, User: "alice", KeyID: keyID})
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
		// The card shows the message itself: its timeout text speaks of 10 seconds.
		{errors.Join(sshclient.ErrTimeout, sshclient.ErrHandshakeCap), "other", true},
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

// openWithPassphrase opens a console, answers the key passphrase in it and waits until it is connected.
func (s sessionsApp) openWithPassphrase(t *testing.T) string {
	t.Helper()
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)
	return id
}

// runningForward saves an L forward to an echo server and starts it, answering the passphrase in
// the host's forwards group.
func (s sessionsApp) runningForward(t *testing.T) ForwardDTO {
	t.Helper()
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	return f
}

func (s sessionsApp) checkAccepted(t *testing.T, want int) {
	t.Helper()
	if n := s.srv.Accepted(); n != want {
		t.Fatalf("server accepted %d connections, want %d", n, want)
	}
}

func TestSessionReusesForwardConnection(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.runningForward(t)
	id := s.open(t)
	s.ev.waitState(t, id, "connected", hcWait)
	s.ev.waitData(t, id, "ok\r\n")
	if out := s.ev.data(t, id); strings.Contains(out, passphrasePrompt) {
		t.Fatalf("the console asked the passphrase on a live connection: %q", out)
	}
	roundTrip(t, f.Listen)
	s.checkAccepted(t, 1)
}

func TestForwardReusesSessionConnection(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	s.openWithPassphrase(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, hcWait)
	roundTrip(t, f.Listen)
	if out := s.groupData(t); out != "" {
		t.Fatalf("the forwards group printed %q on a live connection", out)
	}
	s.checkAccepted(t, 1)
}

func TestSessionCloseKeepsForwards(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.runningForward(t)
	id := s.open(t)
	s.ev.waitState(t, id, "connected", hcWait)
	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	s.ev.waitState(t, id, "closed", hcWait)
	roundTrip(t, f.Listen)
	if got := listed(t, s.App, f.ID); got.Status != "running" {
		t.Fatalf("forward after the console closed %+v", got)
	}
	s.checkAccepted(t, 1)
}

func TestStopAllForwardsKeepsSession(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	id := s.openWithPassphrase(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, hcWait)
	if err := s.ForwardStopAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	portFree(t, f.Listen)
	s.write(t, id, "hi\r")
	s.ev.waitData(t, id, "ok\r\nhi\r")
	if l := s.SessionList(); len(l) != 1 || l[0].State != "connected" {
		t.Fatalf("sessions after stop all %+v", l)
	}
	s.checkAccepted(t, 1)
}

func TestSessionWaitsForForwardsLogin(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitHostPrompts(t, s.hostID, 1)
	id := s.open(t)
	s.ev.waitData(t, id, "Идёт вход для пробросов: ответьте в разделе «Пробросы»\r\n")
	s.answer(t, s.hostID, fwdPassphrase)
	s.ev.waitState(t, id, "connected", hcWait)
	s.ev.waitFwdState(t, f.ID, "running", 0, hcWait)
	if out := s.ev.data(t, id); strings.Contains(out, passphrasePrompt) {
		t.Fatalf("the waiting console was asked the passphrase: %q", out)
	}
	s.checkAccepted(t, 1)
}

func TestSessionCloseDuringLoginHandsOverToForwards(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitGroupData(t, "Идёт вход в консоли «srv»", 1)
	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	if st := s.ev.waitState(t, id, "error", hcWait); st.Kind != "cancelled" {
		t.Fatalf("closed console %+v", st)
	}
	s.waitHostPrompts(t, s.hostID, 1)
	s.answer(t, s.hostID, fwdPassphrase)
	s.ev.waitFwdState(t, f.ID, "running", 0, hcWait)
	roundTrip(t, f.Listen)
}

func TestSessionDropShowsErrorForwardsReconnect(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	f := s.runningForward(t)
	id := s.open(t)
	s.ev.waitState(t, id, "connected", hcWait)

	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	st := s.ev.waitState(t, id, "error", hcWait)
	if st.Kind != "other" || !st.Retryable || st.Message != "Соединение с сервером прервано" || st.Detail == "" {
		t.Fatalf("dropped console %+v", st)
	}
	s.ev.waitFwdState(t, f.ID, "starting", n, hcWait)
	s.ev.waitFwdState(t, f.ID, "running", n+1, hcWait)
	roundTrip(t, f.Listen)
	if c := s.prompts(t); c != 1 {
		t.Fatalf("passphrase asked %d times: %q", c, s.groupData(t))
	}
	s.checkAccepted(t, 2)
}

// A server that stopped answering requests must not hold up closing its console: the console
// never waits on the shared client, whose own keepalive ends it later.
func TestSessionCloseDoesNotWaitOnFrozenServer(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	s.srv.FreezeConnections() // the console's first latency request stays unanswered
	id := s.open(t)
	s.ev.waitState(t, id, "connected", hcWait)
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		s.closeAllSessions()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the console waited on the frozen server")
	}
}

// Keys typed into a console that waits for a login answered elsewhere never reach its shell: a
// passphrase typed there by mistake stays out of the shell.
func TestSessionInputWhileWaitingIsDropped(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitHostPrompts(t, s.hostID, 1)
	id := s.open(t)
	s.ev.waitData(t, id, waitForwardsLogin)
	s.write(t, id, "s3cret\r")
	s.answer(t, s.hostID, fwdPassphrase)
	s.ev.waitState(t, id, "connected", hcWait)
	s.write(t, id, "after\r")
	s.ev.waitData(t, id, "after\r")
	if out := s.ev.data(t, id); strings.Contains(out, "s3cret") {
		t.Fatalf("keys typed while waiting reached the shell: %q", out)
	}
}

// A shell that ends without an exit status on a live connection is closed, not a dropped connection.
func TestSessionEndWithoutExitStatusIsClosed(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{NoExitStatus: true}, true)
	t.Cleanup(s.closeAllHostConns)
	id := s.open(t)
	st := s.ev.waitState(t, id, "closed", hcWait)
	if st.ExitCode != nil || st.Kind != "" || st.Message != "" {
		t.Fatalf("closed payload %+v", st)
	}
}

func TestOutStreamHoldsOnlyTheLatestState(t *testing.T) {
	ev := newFakeEmitter()
	o := newOutStreamFor(ev, "fwdhost:data:h")
	o.latestEvent = "fwdhost:state:h"
	o.Event("fwdhost:state:h", HostConnStateDTO{Status: "connecting"}, false)
	mustWrite(t, o, "question")
	o.Event("trust:ask", "a", false)
	o.Event("fwdhost:state:h", HostConnStateDTO{Status: "connected"}, false)
	o.Event("trust:ask", "b", false)
	o.Event("fwdhost:state:h", HostConnStateDTO{Status: "none"}, false)
	o.Attach()
	var names []string
	for _, e := range ev.snapshot() {
		names = append(names, e.name)
	}
	want := "fwdhost:data:h trust:ask trust:ask fwdhost:state:h"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if st := ev.snapshot()[3].payload.(HostConnStateDTO); st.Status != "none" {
		t.Fatalf("held state %+v, want the latest", st)
	}
}
