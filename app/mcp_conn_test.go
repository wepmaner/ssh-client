package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"errors"
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

// newMCPApp is an App whose host needs fwdPassphrase; its holds go before its connections close.
func newMCPApp(t *testing.T) sessionsApp {
	t.Helper()
	s := newEncryptedHostConnApp(t)
	t.Cleanup(s.mcpReleaseAll)
	return s
}

// newPlainMCPApp is an App whose host logs in with a key without a passphrase.
func newPlainMCPApp(t *testing.T) sessionsApp {
	t.Helper()
	s := newHostConnApp(t, sshtest.Opts{}, true)
	t.Cleanup(s.mcpReleaseAll)
	return s
}

type begun struct {
	client *ssh.Client
	call   *mcpCall
	err    error
}

func (s sessionsApp) beginAsync(ctx context.Context) chan begun {
	ch := make(chan begun, 1)
	go func() {
		cl, call, err := s.mcpBegin(ctx, s.hostID)
		ch <- begun{cl, call, err}
	}()
	return ch
}

func waitBegun(t *testing.T, ch chan begun) begun {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(hcWait):
		t.Fatal("mcpBegin did not return")
		return begun{}
	}
}

func (s sessionsApp) mustBegin(t *testing.T) begun {
	t.Helper()
	r := waitBegun(t, s.beginAsync(context.Background()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r
}

func (f *fakeEmitter) count(name string) int {
	n := 0
	for _, e := range f.snapshot() {
		if e.name == name {
			n++
		}
	}
	return n
}

func (s sessionsApp) waitEvent(t *testing.T, name string, n int) {
	t.Helper()
	s.ev.waitFor(t, hcWait, name, func([]fakeEvent) bool { return s.ev.count(name) >= n })
}

// loginData is the decoded output of the host's login dialog.
func (s sessionsApp) loginData(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, e := range s.ev.snapshot() {
		if e.name == "mcplogin:data:"+s.hostID {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

// loginID is the id of the n-th mcplogin:open, from 1; 0 is the latest, "" before the first.
func (s sessionsApp) loginID(n int) string {
	var ids []string
	for _, e := range s.ev.snapshot() {
		if e.name == "mcplogin:open" {
			ids = append(ids, e.payload.(MCPLoginOpenDTO).LoginID)
		}
	}
	switch {
	case n > 0:
		return ids[n-1]
	case len(ids) > 0:
		return ids[len(ids)-1]
	}
	return ""
}

// typeLogin types text and Enter into the host's login dialog.
func (s sessionsApp) typeLogin(t *testing.T, text string) {
	t.Helper()
	if err := s.MCPLoginWrite(s.hostID, s.loginID(0), base64.StdEncoding.EncodeToString([]byte(text+"\r"))); err != nil {
		t.Fatal(err)
	}
}

// openLogin waits for the n-th dialog and its n-th prompt, as the dialog does: it attaches on open.
func (s sessionsApp) openLogin(t *testing.T, n int) {
	t.Helper()
	s.waitEvent(t, "mcplogin:open", n)
	if err := s.MCPLoginAttach(s.hostID, s.loginID(n)); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFor(t, hcWait, "the login prompt", func([]fakeEvent) bool {
		return strings.Count(s.loginData(t), passphrasePrompt) >= n
	})
}

// login begins a call on a host without a connection and answers its dialog.
func (s sessionsApp) login(t *testing.T) begun {
	t.Helper()
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r
}

func checkAlive(t *testing.T, cl *ssh.Client) {
	t.Helper()
	if _, _, err := cl.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("client closed: %v", err)
	}
}

func TestMCPConnectedHostNoLogin(t *testing.T) {
	s := newMCPApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)

	r := s.mustBegin(t)
	defer s.mcpEnd(r.call)
	checkAlive(t, r.client)
	if n := s.ev.count("mcplogin:open"); n != 0 {
		t.Fatalf("%d dialogs on a connected host", n)
	}
	s.checkAccepted(t, 1)
}

func TestMCPSecondCallNoPrompt(t *testing.T) {
	s := newMCPApp(t)
	first := s.login(t)
	s.mcpEnd(first.call)

	second := s.mustBegin(t)
	defer s.mcpEnd(second.call)
	if second.client != first.client {
		t.Fatal("the second call got another client")
	}
	if n := s.ev.count("mcplogin:open"); n != 1 {
		t.Fatalf("%d dialogs", n)
	}
	s.checkAccepted(t, 1)
}

func TestMCPHoldExpires(t *testing.T) {
	s := newMCPApp(t)
	s.mcpHoldTTL = 100 * time.Millisecond
	r := s.login(t)
	pass := s.connSecret(t, s.hostID)
	s.mcpEnd(r.call)

	waitClosed(t, r.client)
	checkWiped(t, pass)
	s.ev.waitFor(t, hcWait, "the connection to close", func([]fakeEvent) bool {
		s.hcMu.Lock()
		defer s.hcMu.Unlock()
		return s.hostConns[s.hostID] == nil
	})
	s.checkNoConns(t, s.hostID)
}

func TestMCPHoldWaitsForRunningCall(t *testing.T) {
	s := newMCPApp(t)
	s.mcpHoldTTL = 100 * time.Millisecond
	r := s.login(t)

	// The hold counts from the last call's end: a call longer than the hold keeps it.
	time.Sleep(300 * time.Millisecond)
	checkAlive(t, r.client)
	pass := s.connSecret(t, s.hostID)
	s.mcpEnd(r.call)
	checkAlive(t, r.client)
	waitClosed(t, r.client)
	checkWiped(t, pass)
}

func TestMCPHoldKeptWhileLoginWaits(t *testing.T) {
	s := newMCPApp(t)
	s.mcpLoginLimit = 20 * time.Millisecond
	s.mcpHoldTTL = 50 * time.Millisecond
	r := waitBegun(t, s.beginAsync(context.Background()))
	if !errors.Is(r.err, errMCPLoginPending) {
		t.Fatalf("got %v", r.err)
	}
	s.openLogin(t, 1)
	time.Sleep(200 * time.Millisecond) // the hold's time passes while the owner types
	if n := s.ev.count("mcplogin:close"); n != 0 {
		t.Fatal("the login ended with the hold's time")
	}
	s.typeLogin(t, fwdPassphrase)
	waitConnState(t, mustConn(t, s), "connected", func(st HostConnStateDTO) bool { return st.Status == hostConnConnected })
}

func TestMCPTwoCallsOneLogin(t *testing.T) {
	s := newMCPApp(t)
	first := s.beginAsync(context.Background())
	second := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	s.typeLogin(t, fwdPassphrase)
	r1, r2 := waitBegun(t, first), waitBegun(t, second)
	if r1.err != nil || r2.err != nil {
		t.Fatalf("calls: %v, %v", r1.err, r2.err)
	}
	defer s.mcpEnd(r1.call)
	defer s.mcpEnd(r2.call)
	if r1.client != r2.client {
		t.Fatal("two clients for one login")
	}
	if n := s.ev.count("mcplogin:open"); n != 1 {
		t.Fatalf("%d dialogs", n)
	}
	if n := strings.Count(s.loginData(t), passphrasePrompt); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	s.waitEvent(t, "mcplogin:close", 1)
	s.checkAccepted(t, 1)
}

func TestMCPOpenCarriesHost(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	for _, e := range s.ev.snapshot() {
		if e.name == "mcplogin:open" {
			if p := e.payload.(MCPLoginOpenDTO); p.HostID != s.hostID || p.HostName != "srv" || p.LoginID == "" {
				t.Fatalf("payload %+v", p)
			}
		}
	}
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
	s.waitEvent(t, "mcplogin:close", 1)
	for _, e := range s.ev.snapshot() {
		if e.name == "mcplogin:close" {
			if p := e.payload.(MCPLoginCloseDTO); p.HostID != s.hostID || p.LoginID != s.loginID(1) {
				t.Fatalf("payload %+v", p)
			}
		}
	}
}

func TestMCPLoginOutlivesCall(t *testing.T) {
	s := newMCPApp(t)
	s.mcpLoginLimit = 50 * time.Millisecond
	r := waitBegun(t, s.beginAsync(context.Background()))
	if !errors.Is(r.err, errMCPLoginPending) || r.call != nil {
		t.Fatalf("got %v, call %v", r.err, r.call)
	}
	if r.err.Error() != "Ожидается вход в окне клиента, повторите команду" {
		t.Fatalf("text %q", r.err)
	}
	s.openLogin(t, 1)
	s.typeLogin(t, fwdPassphrase)
	waitConnState(t, mustConn(t, s), "connected", func(st HostConnStateDTO) bool { return st.Status == hostConnConnected })

	again := s.mustBegin(t)
	defer s.mcpEnd(again.call)
	if n := strings.Count(s.loginData(t), passphrasePrompt); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	s.checkAccepted(t, 1)
}

func TestMCPRepeatedCallSitsOnPendingLogin(t *testing.T) {
	s := newMCPApp(t)
	s.mcpLoginLimit = 30 * time.Millisecond
	if r := waitBegun(t, s.beginAsync(context.Background())); !errors.Is(r.err, errMCPLoginPending) {
		t.Fatalf("got %v", r.err)
	}
	s.openLogin(t, 1)
	s.mcpLoginLimit = hcWait
	ch := s.beginAsync(context.Background())
	time.Sleep(50 * time.Millisecond)
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer s.mcpEnd(r.call)
	if n := s.ev.count("mcplogin:open"); n != 1 {
		t.Fatalf("%d dialogs", n)
	}
}

func TestMCPCallerCancelKeepsLogin(t *testing.T) {
	s := newMCPApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.beginAsync(ctx)
	s.openLogin(t, 1)
	start := time.Now()
	cancel()
	r := waitBegun(t, ch)
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("got %v", r.err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancel took %v", d)
	}
	if n := s.ev.count("mcplogin:close"); n != 0 {
		t.Fatal("the call's cancel ended the login")
	}
	s.typeLogin(t, fwdPassphrase)
	waitConnState(t, mustConn(t, s), "connected", func(st HostConnStateDTO) bool { return st.Status == hostConnConnected })
}

func TestMCPLoginCancelled(t *testing.T) {
	s := newMCPApp(t)
	first := s.beginAsync(context.Background())
	second := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	if err := s.MCPLoginCancel(s.hostID, s.loginID(0)); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []chan begun{first, second} {
		if r := waitBegun(t, ch); !errors.Is(r.err, errMCPLoginCancelled) {
			t.Fatalf("got %v", r.err)
		}
	}
	s.waitEvent(t, "mcplogin:close", 1)
	s.checkNoConns(t, s.hostID)

	// The next call asks anew.
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 2)
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
}

func TestMCPLoginWrongPassphrase(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	for i := 1; ; i++ {
		s.typeLogin(t, "wrong")
		if i == 3 {
			break
		}
		s.ev.waitFor(t, hcWait, "the next prompt", func([]fakeEvent) bool {
			return strings.Count(s.loginData(t), passphrasePrompt) > i
		})
	}
	if r := waitBegun(t, ch); r.err == nil {
		t.Fatal("three wrong answers logged in")
	}
	s.waitEvent(t, "mcplogin:close", 1)
	s.checkNoConns(t, s.hostID)
}

func TestMCPLoginWriteWithoutLoginIsDropped(t *testing.T) {
	s := newMCPApp(t)
	if err := s.MCPLoginWrite(s.hostID, "", "!!! not base64"); !errors.Is(err, errBadInput) {
		t.Fatalf("got %v", err)
	}
	s.typeLogin(t, "typed into nothing")
	if err := s.MCPLoginCancel(s.hostID, "no-such-login"); err != nil {
		t.Fatal(err)
	}
	if err := s.MCPLoginAttach(s.hostID, "no-such-login"); err != nil {
		t.Fatal(err)
	}
}

func TestMCPTrustRefusedIsError(t *testing.T) {
	s := newMCPApp(t)
	untrust(t)
	ch := s.beginAsync(context.Background())
	s.waitEvent(t, "mcplogin:open", 1)
	if err := s.MCPLoginAttach(s.hostID, s.loginID(1)); err != nil {
		t.Fatal(err)
	}
	q := s.ev.waitTrustAsk(t, 1)
	checkQuestion(t, q, s.srv)
	if q.MCPHostID != s.hostID || q.SessionID != "" || q.ForwardHostID != "" {
		t.Fatalf("owner of %+v", q)
	}
	if err := s.TrustAnswer(q.ID, false); err != nil {
		t.Fatal(err)
	}
	if r := waitBegun(t, ch); !errors.Is(r.err, sshclient.ErrHostNotTrusted) {
		t.Fatalf("got %v", r.err)
	}
	if got := readKnownHosts(t); got != "" {
		t.Fatalf("known_hosts written: %q", got)
	}
	s.waitEvent(t, "mcplogin:close", 1)
}

func TestMCPHostKeyChanged(t *testing.T) {
	s := newPlainMCPApp(t)
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
	for range 2 {
		if r := waitBegun(t, s.beginAsync(context.Background())); !errors.Is(r.err, sshclient.ErrHostKeyChanged) {
			t.Fatalf("got %v", r.err)
		}
	}
	s.ev.waitFor(t, hcWait, "the host to be blocked", func([]fakeEvent) bool { return !s.hostRetryable(s.hostID) })
	if n := s.ev.count("mcplogin:open"); n != 0 {
		t.Fatalf("%d dialogs", n)
	}
}

func TestMCPWaitsForConsoleLogin(t *testing.T) {
	s := newMCPApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	ch := s.beginAsync(context.Background())
	time.Sleep(100 * time.Millisecond)
	if n := s.ev.count("mcplogin:open"); n != 0 {
		t.Fatalf("%d dialogs for a login the console answers", n)
	}
	s.write(t, id, fwdPassphrase+"\r")
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer s.mcpEnd(r.call)
	s.checkAccepted(t, 1)
	if n := s.ev.count("mcplogin:open"); n != 0 {
		t.Fatalf("%d dialogs", n)
	}
}

func TestMCPConsoleClosedHandsLoginToDialog(t *testing.T) {
	s := newMCPApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	ch := s.beginAsync(context.Background())
	time.Sleep(100 * time.Millisecond)
	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	s.openLogin(t, 1)
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
}

func TestMCPConsoleWaitsForDialog(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	if st := mustConn(t, s).state(); st.Status != hostConnConnecting || st.Asker != "mcp" {
		t.Fatalf("state %+v", st)
	}
	id := s.open(t)
	s.ev.waitData(t, id, "Идёт вход по MCP: ответьте в окне клиента\r\n")
	s.typeLogin(t, fwdPassphrase)
	s.ev.waitState(t, id, "connected", hcWait)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
}

func TestMCPDialogCancelHandsLoginToConsole(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	id := s.open(t)
	s.ev.waitData(t, id, "Идёт вход по MCP")
	if err := s.MCPLoginCancel(s.hostID, s.loginID(0)); err != nil {
		t.Fatal(err)
	}
	if r := waitBegun(t, ch); !errors.Is(r.err, errMCPLoginCancelled) {
		t.Fatalf("got %v", r.err)
	}
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)
}

// newTwoServerMCPApp is an App with a plain key its host's server accepts, and a second server
// with the same key, already trusted.
func newTwoServerMCPApp(t *testing.T) (sessionsApp, *sshtest.Server) {
	t.Helper()
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
	s := newHostConnApp(t, sshtest.Opts{AcceptKey: sshPub}, false)
	t.Cleanup(s.mcpReleaseAll)
	k, err := s.storeRef().SaveKey(model.Key{Name: "work", Type: "ed25519", Private: pem.EncodeToMemory(block), Format: "openssh"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.storeRef().Hosts()[0]
	h.KeyID = k.ID
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	srv2 := sshtest.StartServer(t, sshtest.Opts{AcceptKey: sshPub})
	f, err := os.OpenFile(knownHostsPath(t), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(f.Close()) }()
	if _, err := f.WriteString(knownhosts.Line([]string{knownhosts.Normalize(srv2.Addr())}, srv2.HostKey()) + "\n"); err != nil {
		t.Fatal(err)
	}
	return s, srv2
}

// moveHost points the host at srv as HostSave does.
func (s sessionsApp) moveHost(t *testing.T, srv *sshtest.Server) {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	h := s.storeRef().Hosts()[0]
	if h.Port, err = strconv.Atoi(port); err != nil {
		t.Fatal(err)
	}
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	s.markHostStale(s.hostID)
}

func TestMCPNewCallAfterEditUsesNewParams(t *testing.T) {
	s, srv2 := newTwoServerMCPApp(t)
	running := s.mustBegin(t)
	s.moveHost(t, srv2)

	next := s.mustBegin(t)
	defer s.mcpEnd(next.call)
	if next.client == running.client {
		t.Fatal("a new call sat on the old connection")
	}
	if n := srv2.Accepted(); n != 1 {
		t.Fatalf("new server accepted %d", n)
	}
	checkAlive(t, running.client) // the running call finishes where it started
	s.mcpEnd(running.call)
	waitClosed(t, running.client) // the hold has moved: nobody is left on the old connection
	checkAlive(t, next.client)
}

func TestMCPEditDuringLoginMovesCallToNewParams(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)

	// Same server, another user: the login on the old parameters no longer counts.
	h := s.storeRef().Hosts()[0]
	h.User = "bob"
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	s.markHostStale(s.hostID)
	second := s.beginAsync(context.Background())
	s.waitEvent(t, "mcplogin:close", 1)
	s.openLogin(t, 2)
	s.typeLogin(t, fwdPassphrase)
	for _, c := range []chan begun{ch, second} {
		r := waitBegun(t, c)
		if r.err != nil {
			t.Fatal(r.err)
		}
		s.mcpEnd(r.call)
	}
}

func TestMCPReleaseHostKeepsConsole(t *testing.T) {
	s := newMCPApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)
	r := s.mustBegin(t)
	s.mcpEnd(r.call)
	pass := s.connSecret(t, s.hostID)

	s.mcpReleaseHost(s.hostID)
	checkAlive(t, r.client)
	if string(pass) != fwdPassphrase {
		t.Fatal("the console's connection lost its secrets")
	}
	if st := s.ev.states(id); st[len(st)-1].State != "connected" {
		t.Fatalf("console states %+v", st)
	}
}

func TestMCPReleaseHostLastUserWipes(t *testing.T) {
	s := newMCPApp(t)
	r := s.login(t)
	pass := s.connSecret(t, s.hostID)
	s.mcpEnd(r.call)
	s.mcpReleaseHost(s.hostID)
	waitClosed(t, r.client)
	checkWiped(t, pass)
	s.checkNoConns(t, s.hostID)
}

func TestMCPReleaseHostCancelsCalls(t *testing.T) {
	s := newMCPApp(t)
	r := s.login(t)
	pass := s.connSecret(t, s.hostID)
	s.mcpReleaseHost(s.hostID)
	select {
	case <-r.call.ctx.Done():
	case <-time.After(hcWait):
		t.Fatal("the running call was not cancelled")
	}
	checkAlive(t, r.client) // it stays a user until it ends
	s.mcpEnd(r.call)
	s.mcpEnd(r.call) // a repeated end does nothing
	waitClosed(t, r.client)
	checkWiped(t, pass)
	s.checkNoConns(t, s.hostID)
}

func TestMCPReleaseHostEndsLogin(t *testing.T) {
	s := newMCPApp(t)
	ch := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	s.mcpReleaseHost(s.hostID)
	if r := waitBegun(t, ch); r.err == nil {
		t.Fatal("the waiting call got a client")
	}
	s.waitEvent(t, "mcplogin:close", 1)
	s.ev.waitFor(t, hcWait, "the connection to close", func([]fakeEvent) bool {
		s.hcMu.Lock()
		defer s.hcMu.Unlock()
		return s.hostConns[s.hostID] == nil
	})
}

func TestMCPReleaseAll(t *testing.T) {
	s := newMCPApp(t)
	r := s.login(t)
	s.mcpEnd(r.call)
	s.mcpReleaseAll()
	waitClosed(t, r.client)
	s.mcpReleaseAll()
}

func TestMCPDialogOnlyWhenInput(t *testing.T) {
	s := newPlainMCPApp(t)
	r := s.mustBegin(t)
	defer s.mcpEnd(r.call)
	checkAlive(t, r.client)
	if n := s.ev.count("mcplogin:open") + s.ev.count("mcplogin:close"); n != 0 {
		t.Fatalf("%d dialog events", n)
	}
}

func TestMCPDropThenAsksAgain(t *testing.T) {
	s := newMCPApp(t)
	first := s.login(t)
	s.mcpEnd(first.call)
	s.srv.DropConnections()
	waitClosed(t, first.client)
	waitConnState(t, mustConn(t, s), "the drop", func(st HostConnStateDTO) bool { return st.Status == hostConnNone })

	ch := s.beginAsync(context.Background())
	s.openLogin(t, 2)
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
}

func TestMCPLockedVaultIsError(t *testing.T) {
	s := newMCPApp(t)
	r := s.login(t) // a live connection does not let a new call through
	s.mcpEnd(r.call)
	if err := s.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultLock(); err != nil {
		t.Fatal(err)
	}
	if r := waitBegun(t, s.beginAsync(context.Background())); !errors.Is(r.err, errVaultLocked) {
		t.Fatalf("got %v", r.err)
	}
}

func TestMCPEditWithNewConnectionLiveEndsOldLogin(t *testing.T) {
	s := newMCPApp(t)
	s.mcpLoginLimit = 30 * time.Millisecond
	if r := waitBegun(t, s.beginAsync(context.Background())); !errors.Is(r.err, errMCPLoginPending) {
		t.Fatalf("got %v", r.err)
	}
	s.openLogin(t, 1)
	h := s.storeRef().Hosts()[0]
	h.User = "bob"
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	s.markHostStale(s.hostID)

	id := s.open(t) // the console logs in with the new parameters
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)

	r := s.mustBegin(t)
	defer s.mcpEnd(r.call)
	s.waitEvent(t, "mcplogin:close", 1)
}

// TestMCPUsersBalance runs calls against releases: every user leaves, whichever comes first.
func TestMCPUsersBalance(t *testing.T) {
	s := newPlainMCPApp(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, call, err := s.mcpBegin(context.Background(), s.hostID); err == nil {
					s.mcpEnd(call)
				}
			}
		}()
	}
	for range 20 {
		s.mcpReleaseHost(s.hostID)
		time.Sleep(time.Millisecond)
	}
	wg.Wait()
	s.mcpReleaseHost(s.hostID)
	s.hcMu.Lock()
	defer s.hcMu.Unlock()
	for u := range s.hcUsers {
		t.Fatalf("user left on a connection: %T", u)
	}
}

func TestMCPUnknownHostIsError(t *testing.T) {
	s := newMCPApp(t)
	if _, _, err := s.mcpBegin(context.Background(), "no-such-host"); !errors.Is(err, errHostNotFound) {
		t.Fatalf("got %v", err)
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if len(s.mcpHolds) != 0 {
		t.Fatal("a hold for an unknown host")
	}
}

func TestMCPOutStreamOnOutputOnce(t *testing.T) {
	ev := newFakeEmitter()
	n := 0
	o := newOutStreamFor(ev, "x")
	o.onOutput = func() { n++ }
	mustWrite(t, o, "a")
	mustWrite(t, o, "b")
	if n != 1 {
		t.Fatalf("called %d times", n)
	}
}

func TestMCPOldLoginIDReachesNothing(t *testing.T) {
	s := newMCPApp(t)
	first := s.beginAsync(context.Background())
	s.openLogin(t, 1)
	old := s.loginID(1)
	if err := s.MCPLoginCancel(s.hostID, old); err != nil {
		t.Fatal(err)
	}
	if r := waitBegun(t, first); !errors.Is(r.err, errMCPLoginCancelled) {
		t.Fatalf("got %v", r.err)
	}
	s.waitEvent(t, "mcplogin:close", 1)

	ch := s.beginAsync(context.Background())
	s.waitEvent(t, "mcplogin:open", 2)
	if s.loginID(2) == old {
		t.Fatal("two logins with one id")
	}
	// Calls late from the first dialog: none of them touches the second login.
	shown := s.ev.count("mcplogin:data:" + s.hostID)
	if err := s.MCPLoginAttach(s.hostID, old); err != nil {
		t.Fatal(err)
	}
	if err := s.MCPLoginCancel(s.hostID, old); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := s.ev.count("mcplogin:data:" + s.hostID); n != shown {
		t.Fatalf("an old attach released output: %d data events, was %d", n, shown)
	}
	if n := s.ev.count("mcplogin:close"); n != 1 {
		t.Fatalf("an old cancel ended the login: %d closes", n)
	}
	s.openLogin(t, 2)
	bad := base64.StdEncoding.EncodeToString([]byte("wrong\r"))
	if err := s.MCPLoginWrite(s.hostID, old, bad); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(s.loginData(t), passphrasePrompt); n != 2 {
		t.Fatalf("old input reached the login: %d prompts", n)
	}
	s.typeLogin(t, fwdPassphrase)
	r := waitBegun(t, ch)
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.mcpEnd(r.call)
}

func TestMCPClientForRefusesStaleConnection(t *testing.T) {
	s := newPlainMCPApp(t)
	r := s.mustBegin(t)
	defer s.mcpEnd(r.call)
	c := s.connOf(r.call)
	if cl, stale, err := c.clientFor(r.call); err != nil || stale || cl != r.client {
		t.Fatalf("current connection: %v, %v, %v", cl, stale, err)
	}
	s.renameUser(t, "bob")
	if cl, stale, err := c.clientFor(r.call); err != nil || !stale || cl != nil {
		t.Fatalf("stale connection: %v, %v, %v", cl, stale, err)
	}
}

func TestMCPRefusedDuringRestoreAndExit(t *testing.T) {
	s := newPlainMCPApp(t)
	s.restoring.Store(true)
	if _, _, err := s.mcpBegin(context.Background(), s.hostID); !errors.Is(err, errRestoring) {
		t.Fatalf("got %v", err)
	}
	s.restoring.Store(false)
	s.exiting.Store(true)
	if _, _, err := s.mcpBegin(context.Background(), s.hostID); !errors.Is(err, sshclient.ErrCancelled) {
		t.Fatalf("got %v", err)
	}
}

func TestMCPCallAfterCloseAllReturns(t *testing.T) {
	s := newPlainMCPApp(t)
	s.mcpLoginLimit = 200 * time.Millisecond
	s.closeAllHostConns()
	if r := waitBegun(t, s.beginAsync(context.Background())); !errors.Is(r.err, sshclient.ErrCancelled) {
		t.Fatalf("got %v", r.err)
	}
}

// renameUser changes a login parameter of the host as HostSave does.
func (s sessionsApp) renameUser(t *testing.T, name string) {
	t.Helper()
	h := s.storeRef().Hosts()[0]
	h.User = name
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	s.markHostStale(s.hostID)
}

func (s sessionsApp) holdOf(t *testing.T) *mcpHold {
	t.Helper()
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	h := s.mcpHolds[s.hostID]
	if h == nil {
		t.Fatal("the host has no hold")
	}
	return h
}

func TestMCPFailedOldLoginKeepsHoldOnNewConnection(t *testing.T) {
	s := newPlainMCPApp(t)
	r := s.mustBegin(t)
	s.mcpEnd(r.call)
	h := s.holdOf(t)
	old := s.connOf(h)
	s.renameUser(t, "bob")
	fresh, err := s.joinHostConn(s.hostID, h)
	if err != nil {
		t.Fatal(err)
	}
	l := &mcpLogin{conn: old, cancel: func() {}, done: make(chan struct{})}
	s.mcpMu.Lock()
	h.login = l
	s.mcpMu.Unlock()
	s.mcpFinishLogin(h, l, errors.New("failed"))
	if got := s.connOf(h); got != fresh {
		t.Fatalf("the hold is on %p, want %p", got, fresh)
	}
}

func TestMCPLoginRejoinsHold(t *testing.T) {
	s := newPlainMCPApp(t)
	r := s.mustBegin(t)
	s.mcpEnd(r.call)
	h := s.holdOf(t)
	s.mcpLeave(h) // as a failed login's leave, between a call's join and its new login
	c := mustConn(t, s)
	l := s.mcpLoginOn(h, c)
	select {
	case <-l.done:
	case <-time.After(hcWait):
		t.Fatal("the login did not end")
	}
	if l.err != nil {
		t.Fatal(l.err)
	}
	if s.connOf(h) != c {
		t.Fatal("the hold is not on the connection")
	}
}
