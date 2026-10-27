package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/sshclient/sshtest"
)

const hcWait = 5 * time.Second

// testAsker answers like a session terminal: its own attached stream term:data:<name>.
func testAsker(s sessionsApp, name, label string) *connAsker {
	out := newOutStream(s.ev, name)
	out.Attach()
	return &connAsker{out: out, in: newInputQueue(), trust: trustOwner{sessionID: name}, label: label}
}

// groupData is the decoded output of the host's forwards group, fwdhost:data:<hostID>.
func (s sessionsApp) groupData(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, e := range s.ev.snapshot() {
		if e.name == "fwdhost:data:"+s.hostID {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

// waitGroupData waits until the group's output contains want n times.
func (s sessionsApp) waitGroupData(t *testing.T, want string, n int) {
	t.Helper()
	s.ev.waitFor(t, hcWait, "group data "+strconv.Quote(want), func([]fakeEvent) bool {
		return strings.Count(s.groupData(t), want) >= n
	})
}

// answerGroup types text and Enter into the current login input of the forwards group.
func (s sessionsApp) answerGroup(t *testing.T, text string) {
	t.Helper()
	g := s.fwdHostGroup(s.hostID)
	s.fwdHostMu.Lock()
	in := g.in
	s.fwdHostMu.Unlock()
	if in == nil {
		t.Fatal("the forwards group has no login input")
	}
	in.Write([]byte(text + "\r"))
}

// secretsForTest returns the remembered buffers themselves (not copies).
func (c *hostConn) secretsForTest() (passphrase, password []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.passphrase, c.password
}

type acquired struct {
	client *ssh.Client
	err    error
}

func acquireAsync(ctx context.Context, c *hostConn, u any, ask *connAsker) chan acquired {
	ch := make(chan acquired, 1)
	go func() {
		cl, err := c.acquire(ctx, u, ask)
		ch <- acquired{cl, err}
	}()
	return ch
}

func waitAcquired(t *testing.T, ch chan acquired) acquired {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(hcWait):
		t.Fatal("acquire did not return")
		return acquired{}
	}
}

func mustConn(t *testing.T, s sessionsApp) *hostConn {
	t.Helper()
	c, err := s.hostConnFor(s.hostID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitClosed(t *testing.T, cl *ssh.Client) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ignore(cl.Wait())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hcWait):
		t.Fatal("client still open")
	}
}

func waitConnState(t *testing.T, c *hostConn, what string, ok func(HostConnStateDTO) bool) {
	t.Helper()
	deadline := time.Now().Add(hcWait)
	for !ok(c.state()) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s, state %+v", what, c.state())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newHostConnApp(t *testing.T, opts sshtest.Opts, withKey bool) sessionsApp {
	t.Helper()
	s := newSessionsApp(t, opts, withKey)
	t.Cleanup(s.closeAllHostConns)
	return s
}

func newEncryptedHostConnApp(t *testing.T) sessionsApp {
	t.Helper()
	s := newEncryptedForwardsApp(t)
	t.Cleanup(s.closeAllHostConns)
	return s
}

// loginWithPassphrase acquires for u through ask, answering the passphrase question in ask.
func loginWithPassphrase(t *testing.T, c *hostConn, u any, ask *connAsker, s sessionsApp, name string) *ssh.Client {
	t.Helper()
	ch := acquireAsync(context.Background(), c, u, ask)
	s.ev.waitData(t, name, passphrasePrompt)
	ask.in.Write([]byte(fwdPassphrase + "\r"))
	r := waitAcquired(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r.client
}

func TestHostConnSharesOneClient(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	c := mustConn(t, s)
	fw, sess := &fwdRun{}, &session{}
	c.join(fw)
	c.join(sess)
	defer c.leave(fw)
	defer c.leave(sess)

	c1, err := c.acquire(context.Background(), fw, s.forwardsAsker(s.hostID))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := c.acquire(context.Background(), sess, testAsker(s, "t1", "srv"))
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Fatal("two users got two clients")
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
	if again := mustConn(t, s); again != c {
		t.Fatal("hostConnFor returned another connection for the same host")
	}
	if st := c.state(); st.Status != "connected" || !st.Retryable {
		t.Fatalf("state %+v", st)
	}
}

func TestHostConnConcurrentAcquireOneLogin(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	sess, fw := &session{}, &fwdRun{}
	c.join(sess)
	c.join(fw)
	defer c.leave(sess)
	defer c.leave(fw)

	ask := testAsker(s, "t1", "srv")
	first := acquireAsync(context.Background(), c, sess, ask)
	s.ev.waitData(t, "t1", passphrasePrompt)
	if st := c.state(); st.Status != "connecting" || st.Asker != "session" || st.Session != "srv" {
		t.Fatalf("state during the login %+v", st)
	}
	second := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Идёт вход в консоли «srv»\r\n", 1)

	ask.in.Write([]byte(fwdPassphrase + "\r"))
	r1, r2 := waitAcquired(t, first), waitAcquired(t, second)
	if r1.err != nil || r2.err != nil {
		t.Fatalf("acquire: %v, %v", r1.err, r2.err)
	}
	if r1.client != r2.client {
		t.Fatal("two clients for one login")
	}
	asked := strings.Count(s.ev.data(t, "t1"), "Парольная фраза ключа") + strings.Count(s.groupData(t), "Парольная фраза ключа")
	if asked != 1 {
		t.Fatalf("passphrase asked %d times", asked)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
}

func TestHostConnSessionWaitsForForwardsLogin(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	fw, sess := &fwdRun{}, &session{}
	c.join(fw)
	c.join(sess)
	defer c.leave(fw)
	defer c.leave(sess)

	first := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, passphrasePrompt, 1)
	second := acquireAsync(context.Background(), c, sess, testAsker(s, "t1", "srv"))
	s.ev.waitData(t, "t1", "Идёт вход для пробросов: ответьте в разделе «Пробросы»\r\n")
	s.answerGroup(t, fwdPassphrase)
	if r1, r2 := waitAcquired(t, first), waitAcquired(t, second); r1.err != nil || r2.err != nil || r1.client != r2.client {
		t.Fatalf("acquire: %v, %v", r1.err, r2.err)
	}
}

func TestHostConnLoginMovesToRemainingWaiter(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	sess, fw := &session{}, &fwdRun{}
	c.join(sess)
	c.join(fw)
	defer c.leave(fw)

	ctx, cancel := context.WithCancel(context.Background())
	first := acquireAsync(ctx, c, sess, testAsker(s, "t1", "srv"))
	s.ev.waitData(t, "t1", passphrasePrompt)
	second := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Идёт вход в консоли «srv»", 1)

	cancel() // the console that answers is closed: the question moves to the forwards group
	if r := waitAcquired(t, first); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("closed console got %v", r.err)
	}
	c.leave(sess)
	s.waitGroupData(t, passphrasePrompt, 1)
	s.answerGroup(t, fwdPassphrase)
	if r := waitAcquired(t, second); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestHostConnLastLeaveClosesAndWipes(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	c := mustConn(t, s)
	fw, sess := &fwdRun{}, &session{}
	c.join(fw)
	c.join(sess)
	cl := loginWithPassphrase(t, c, sess, testAsker(s, "t1", "srv"), s, "t1")
	pass, _ := c.secretsForTest()
	if string(pass) != fwdPassphrase {
		t.Fatalf("remembered passphrase %q", pass)
	}

	c.leave(sess)
	if _, _, err := cl.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("a remaining user lost the client: %v", err)
	}
	c.leave(fw)
	waitClosed(t, cl)
	if p, pw := c.secretsForTest(); p != nil || pw != nil || !zeroBytes(pass) {
		t.Fatalf("secrets kept: %q %q, buffer %q", p, pw, pass)
	}
	if st := c.state(); st.Status != "none" {
		t.Fatalf("state %+v", st)
	}
	if next := mustConn(t, s); next == c {
		t.Fatal("a closed connection stayed the host's connection")
	}
}

func TestHostConnReconnectUsesRememberedPassword(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{Password: "s3cret"}, false)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	fw := &fwdRun{}
	c.join(fw)

	ch := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Пароль alice@", 1)
	s.answerGroup(t, "s3cret")
	r := waitAcquired(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	_, pw := c.secretsForTest()
	if string(pw) != "s3cret" {
		t.Fatalf("remembered password %q", pw)
	}

	s.srv.DropConnections()
	waitClosed(t, r.client)
	r2, err := c.acquire(context.Background(), fw, s.forwardsAsker(s.hostID))
	if err != nil {
		t.Fatal(err)
	}
	if r2 == r.client {
		t.Fatal("acquire returned the dropped client")
	}
	if n := s.srv.Accepted(); n != 2 {
		t.Fatalf("server accepted %d connections, want 2", n)
	}
	if n := strings.Count(s.groupData(t), "Пароль alice@"); n != 1 {
		t.Fatalf("password asked %d times", n)
	}
	if strings.Contains(s.groupData(t), "s3cret") {
		t.Fatal("password echoed")
	}

	c.leave(fw)
	waitClosed(t, r2)
	if _, p := c.secretsForTest(); p != nil || !zeroBytes(pw) {
		t.Fatalf("password kept: %q, buffer %q", p, pw)
	}
}

func TestHostConnDropWithoutForwardsCloses(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	c := mustConn(t, s)
	sess := &session{}
	c.join(sess)
	defer c.leave(sess)
	cl := loginWithPassphrase(t, c, sess, testAsker(s, "t1", "srv"), s, "t1")
	pass, _ := c.secretsForTest()

	s.srv.DropConnections()
	waitClosed(t, cl)
	waitConnState(t, c, "none", func(st HostConnStateDTO) bool { return st.Status == "none" })
	if p, _ := c.secretsForTest(); p != nil || !zeroBytes(pass) {
		t.Fatalf("passphrase kept: %q, buffer %q", p, pass)
	}
	time.Sleep(500 * time.Millisecond) // fifty reconnect pauses
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1: the connection reconnected without forwards", n)
	}

	// A console's reconnect opens a new login.
	cl2 := loginWithPassphrase(t, c, sess, testAsker(s, "t2", "srv"), s, "t2")
	if cl2 == cl {
		t.Fatal("got the dropped client")
	}
}

func TestHostConnHostKeyChangedBlocksHost(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(s.srv.Addr())}, other) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	c := mustConn(t, s)
	fw := &fwdRun{}
	c.join(fw)
	if _, err := c.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); !errors.Is(err, sshclient.ErrHostKeyChanged) {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := c.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); !errors.Is(err, sshclient.ErrHostKeyChanged) {
		t.Fatalf("second acquire: %v", err)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
	if st := c.state(); st.Status != "error" || st.Retryable {
		t.Fatalf("state %+v", st)
	}

	// The block outlives the connection: a new one fails at once too.
	c.leave(fw)
	next := mustConn(t, s)
	next.join(fw)
	defer next.leave(fw)
	if _, err := next.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); !errors.Is(err, sshclient.ErrHostKeyChanged) {
		t.Fatalf("acquire on a new connection: %v", err)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
	if next.state().Retryable {
		t.Fatalf("new connection retryable: %+v", next.state())
	}
}

func TestHostConnStaleOnHostEdit(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	old := mustConn(t, s)
	sess := &session{}
	old.join(sess)
	defer old.leave(sess)
	cl := loginWithPassphrase(t, old, sess, testAsker(s, "t1", "srv"), s, "t1")
	pass, _ := old.secretsForTest()

	h := s.storeRef().Hosts()[0]
	h.User = "bob"
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	fresh := mustConn(t, s)
	if fresh == old {
		t.Fatal("hostConnFor kept the connection after the login parameters changed")
	}
	if again := mustConn(t, s); again != fresh {
		t.Fatal("the new connection is not the host's connection")
	}
	if p, _ := old.secretsForTest(); p != nil || !zeroBytes(pass) {
		t.Fatalf("stale passphrase kept: %q, buffer %q", p, pass)
	}
	got, err := old.acquire(context.Background(), sess, testAsker(s, "t2", "srv"))
	if err != nil || got != cl {
		t.Fatalf("the stale connection stopped serving its user: %v", err)
	}
	if _, _, err := cl.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("stale client closed: %v", err)
	}
}

func TestHostConnCloseDuringReconnect(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	s.hcBackoff = func(int) time.Duration { return 20 * time.Millisecond }
	c := mustConn(t, s)
	fw := &fwdRun{}
	c.join(fw)
	cl := loginWithPassphrase(t, c, fw, testAsker(s, "t1", "srv"), s, "t1")
	pass, _ := c.secretsForTest()

	// The server goes away: its port refuses, so the reconnect loop keeps failing.
	s.srv.StopListening()
	s.srv.DropConnections()
	waitClosed(t, cl)
	waitConnState(t, c, "a failed reconnect", func(st HostConnStateDTO) bool { return st.Error != "" })

	start := time.Now()
	s.closeAllHostConns()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("closeAllHostConns took %v", d)
	}
	if p, _ := c.secretsForTest(); p != nil || !zeroBytes(pass) {
		t.Fatalf("passphrase kept: %q, buffer %q", p, pass)
	}
	if _, err := c.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); err == nil {
		t.Fatal("acquire after shutdown succeeded")
	}
}

func TestHostConnKnownHostsErrorWipesKey(t *testing.T) {
	priv := []byte("private key bytes")
	tg := sshclient.Target{Key: &model.Key{ID: "k", Private: priv}}
	_, err := knownHostsFor(tg, func() (*sshclient.KnownHosts, error) { return nil, errors.New("no known_hosts") })
	if err == nil {
		t.Fatal("no error")
	}
	if !zeroBytes(priv) {
		t.Fatalf("key copy kept: %q", priv)
	}
}

func TestHostConnMarkStaleDetaches(t *testing.T) {
	s := newEncryptedHostConnApp(t)
	c := mustConn(t, s)
	sess := &session{}
	c.join(sess)
	defer c.leave(sess)
	cl := loginWithPassphrase(t, c, sess, testAsker(s, "t1", "srv"), s, "t1")
	pass, _ := c.secretsForTest()

	c.markStale()
	if p, _ := c.secretsForTest(); p != nil || !zeroBytes(pass) {
		t.Fatalf("passphrase kept: %q, buffer %q", p, pass)
	}
	if next := mustConn(t, s); next == c {
		t.Fatal("a stale connection is still the host's connection")
	}
	if got, err := c.acquire(context.Background(), sess, testAsker(s, "t2", "srv")); err != nil || got != cl {
		t.Fatalf("the stale connection stopped serving its user: %v", err)
	}
}

// join adds u through joinHostConn and checks it lands on c, the host's current connection.
func (c *hostConn) join(u any) {
	got, err := c.a.joinHostConn(c.hostID, u)
	if err != nil || got != c {
		panic(fmt.Sprintf("joinHostConn: %v, same connection %v", err, got == c))
	}
}

// groupStates counts the fwdhost:state events of the host.
func (s sessionsApp) groupStates() int {
	n := 0
	for _, e := range s.ev.snapshot() {
		if e.name == "fwdhost:state:"+s.hostID {
			n++
		}
	}
	return n
}

// editHost changes the stored host with edit.
func (s sessionsApp) editHost(t *testing.T, edit func(*model.Host)) {
	t.Helper()
	h := s.storeRef().Hosts()[0]
	edit(&h)
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
}

func TestHostConnStaleEmitsNothing(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	s.fwdHostGroup(s.hostID).out.Attach()
	old := mustConn(t, s)
	sess := &session{}
	old.join(sess)
	if _, err := old.acquire(context.Background(), sess, testAsker(s, "t1", "srv")); err != nil {
		t.Fatal(err)
	}
	s.editHost(t, func(h *model.Host) { h.User = "bob" })
	if fresh := mustConn(t, s); fresh == old {
		t.Fatal("no new connection after the edit")
	}
	n := s.groupStates()

	s.srv.DropConnections() // the stale connection's client ends
	waitConnState(t, old, "none", func(st HostConnStateDTO) bool { return st.Status == "none" })
	old.leave(sess)
	if got := s.groupStates(); got != n {
		t.Fatalf("a stale connection emitted %d host states", got-n)
	}
}

func TestHostConnStaleDropMovesForwardsToCurrent(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{Password: "s3cret"}, false)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	s.fwdHostGroup(s.hostID).out.Attach()
	// The edited host is another server with the same password: a leaked old secret would log in
	// there without a question.
	srv2 := sshtest.StartServer(t, sshtest.Opts{Password: "s3cret"})
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	lines := knownhosts.Line([]string{knownhosts.Normalize(s.srv.Addr())}, s.srv.HostKey()) + "\n" +
		knownhosts.Line([]string{knownhosts.Normalize(srv2.Addr())}, srv2.HostKey()) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	old := mustConn(t, s)
	fw := &fwdRun{}
	old.join(fw)
	ch := acquireAsync(context.Background(), old, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Пароль alice@", 1)
	s.answerGroup(t, "s3cret")
	r := waitAcquired(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	_, pw := old.secretsForTest()

	_, port, err := net.SplitHostPort(srv2.Addr())
	if err != nil {
		t.Fatal(err)
	}
	s.editHost(t, func(h *model.Host) { h.Port, err = strconv.Atoi(port) })
	if err != nil {
		t.Fatal(err)
	}
	s.srv.DropConnections()
	waitClosed(t, r.client)
	ch = acquireAsync(context.Background(), old, fw, s.forwardsAsker(s.hostID)) // the runner re-acquires
	s.waitGroupData(t, "Пароль alice@", 2)
	if n := srv2.Accepted(); n != 1 {
		t.Fatalf("edited host accepted %d connections before the answer", n)
	}
	s.answerGroup(t, "s3cret")
	r2 := waitAcquired(t, ch)
	if r2.err != nil {
		t.Fatal(r2.err)
	}

	cur := mustConn(t, s)
	if cur == old || s.connOf(fw) != cur {
		t.Fatal("the forward is not on the host's current connection")
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("the old server got %d connections: the stale connection reconnected", n)
	}
	if p, _ := old.secretsForTest(); p != nil || !zeroBytes(pw) {
		t.Fatalf("old password kept: %q, buffer %q", p, pw)
	}
	if st := old.state(); st.Status != "none" {
		t.Fatalf("old connection state %+v", st)
	}
	cur.leave(fw)
	waitClosed(t, r2.client)
}

func TestHostConnLeaveDuringReconnectHandsOverToSession(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{Password: "s3cret"}, false)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Second }
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	fw, sess := &fwdRun{}, &session{}
	c.join(fw)
	c.join(sess)
	defer c.leave(sess)
	ch := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Пароль alice@", 1)
	s.answerGroup(t, "s3cret")
	r := waitAcquired(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}

	s.srv.DropConnections()
	waitClosed(t, r.client)
	waitConnState(t, c, "the reconnect", func(st HostConnStateDTO) bool { return st.Status == "connecting" && st.Asker == "forwards" })
	ch = acquireAsync(context.Background(), c, sess, testAsker(s, "t1", "srv"))
	s.ev.waitData(t, "t1", "Идёт вход для пробросов")

	c.leave(fw) // the last forward stops: the waiting console logs in by itself
	r2 := waitAcquired(t, ch)
	if r2.err != nil {
		t.Fatalf("the waiting console got %v", r2.err)
	}
	if strings.Contains(s.ev.data(t, "t1"), "Пароль") {
		t.Fatal("the console was asked the password again")
	}
	if n := s.srv.Accepted(); n != 2 {
		t.Fatalf("server accepted %d connections, want 2", n)
	}
}

func TestHostConnLeaveWithOnlyForwardWaiterDoesNotHandOver(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{Password: "s3cret"}, false)
	s.hcBackoff = func(int) time.Duration { return 10 * time.Second }
	s.fwdHostGroup(s.hostID).out.Attach()
	c := mustConn(t, s)
	fw, sess := &fwdRun{}, &session{}
	c.join(fw)
	c.join(sess) // a console on the connection that is not waiting for a login
	defer c.leave(sess)
	ch := acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	s.waitGroupData(t, "Пароль alice@", 1)
	s.answerGroup(t, "s3cret")
	r := waitAcquired(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	_, pw := c.secretsForTest()

	s.srv.DropConnections()
	waitClosed(t, r.client)
	waitConnState(t, c, "the reconnect", func(st HostConnStateDTO) bool { return st.Status == "connecting" && st.Asker == "forwards" })
	// The forward's runner waits for the reconnect; it is stopped, and leaves before its acquire
	// has seen its own context end.
	ch = acquireAsync(context.Background(), c, fw, s.forwardsAsker(s.hostID))
	waitConnState(t, c, "the runner waiting", func(HostConnStateDTO) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.att != nil && len(c.att.waiting) == 1
	})
	c.leave(fw)
	if r := waitAcquired(t, ch); r.err == nil {
		t.Fatal("the stopped forward got a client")
	}
	time.Sleep(200 * time.Millisecond)
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections: a login ran for the stopped forward", n)
	}
	if p, _ := c.secretsForTest(); p != nil || !zeroBytes(pw) {
		t.Fatalf("password kept: %q, buffer %q", p, pw)
	}
}

func TestHostConnCancelledLoginKeepsNoSecret(t *testing.T) {
	for _, last := range []bool{false, true} {
		s := newHostConnApp(t, sshtest.Opts{}, true)
		c := mustConn(t, s)
		fw, sess := &fwdRun{}, &session{}
		c.join(fw)
		if !last {
			c.join(sess)
		}
		// A reconnect whose dial is about to be accepted while its last forward leaves.
		ctx, cancel := context.WithCancel(context.Background())
		att := &loginAttempt{ask: s.forwardsAsker(s.hostID), loop: true, ctx: ctx, cancel: cancel, done: make(chan struct{}), keep: true}
		c.mu.Lock()
		c.att = att
		c.mu.Unlock()
		c.leave(fw)
		c.remember(att, &c.password, []byte("late"), "")
		if _, p := c.secretsForTest(); p != nil {
			t.Fatalf("last user %v: a secret accepted after the leave was kept: %q", last, p)
		}
		if !last {
			c.leave(sess)
		}
	}
}

func TestHostConnAcquireAfterLeaveOnStaleConnCancels(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	old := mustConn(t, s)
	fw := &fwdRun{}
	old.join(fw)
	if _, err := old.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); err != nil {
		t.Fatal(err)
	}
	s.editHost(t, func(h *model.Host) { h.User = "bob" })
	cur := mustConn(t, s) // old goes stale
	old.leave(fw)         // the forward stops while its runner is about to acquire again

	if _, err := old.acquire(context.Background(), fw, s.forwardsAsker(s.hostID)); !errors.Is(err, sshclient.ErrCancelled) {
		t.Fatalf("acquire of a user that left: %v", err)
	}
	if st := cur.state(); st.Status != "none" {
		t.Fatalf("a login started on the current connection for a non-member: %+v", st)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
}
