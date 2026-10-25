package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/sshclient/sshtest"
)

// fwdStates returns the fwd:state:<id> payloads recorded so far.
func (f *fakeEmitter) fwdStates(id string) []ForwardStateDTO {
	var out []ForwardStateDTO
	for _, e := range f.snapshot() {
		if e.name == "fwd:state:"+id {
			out = append(out, e.payload.(ForwardStateDTO))
		}
	}
	return out
}

// fwdData is the decoded fwd:data:<id> output of a forward's mini-terminal.
func (f *fakeEmitter) fwdData(t *testing.T, id string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range f.snapshot() {
		if e.name == "fwd:data:"+id {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

// waitFwdState waits for a fwd:state event with the status after the first skip events.
func (f *fakeEmitter) waitFwdState(t *testing.T, id, status string, skip int, timeout time.Duration) ForwardStateDTO {
	t.Helper()
	var got ForwardStateDTO
	f.waitFor(t, timeout, "forward "+status, func([]fakeEvent) bool {
		all := f.fwdStates(id)
		for i := skip; i < len(all); i++ {
			if all[i].Status == status {
				got = all[i]
				return true
			}
		}
		return false
	})
	return got
}

func ignore(error)            {}
func ignoreCopy(int64, error) {}

// freePort returns a loopback address whose port was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// echoServer accepts connections on loopback and echoes what it reads.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ignore(ln.Close()) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { ignore(c.Close()) }()
				ignoreCopy(io.Copy(c, c))
			}()
		}
	}()
	return ln.Addr().String()
}

// roundTrip connects to addr, writes ping and expects it echoed back.
func roundTrip(t *testing.T, addr string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(c.Close()) }()
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo through forward: %q %v", buf, err)
	}
}

// portFree reports whether addr can be listened on again.
func portFree(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s still busy: %v", addr, err)
	}
	ignore(ln.Close())
}

func newForwardsApp(t *testing.T, opts sshtest.Opts, withKey bool) sessionsApp {
	t.Helper()
	s := newSessionsApp(t, opts, withKey)
	t.Cleanup(s.stopAllForwards)
	return s
}

func (s sessionsApp) saveL(t *testing.T, listen, target string, autostart bool) ForwardDTO {
	t.Helper()
	f, err := s.ForwardSave(ForwardDTO{Kind: "L", HostID: s.hostID, Listen: listen, Target: target, AutoStart: autostart})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ForwardAttach(f.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func listed(t *testing.T, a *App, id string) ForwardDTO {
	t.Helper()
	for _, f := range a.ForwardsList() {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("forward %s not listed", id)
	return ForwardDTO{}
}

func TestForwardSaveValidation(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	ok := ForwardDTO{Kind: "L", HostID: s.hostID, Listen: "127.0.0.1:15432", Target: "db.example.test:5432"}

	bad := ok
	bad.Kind = "X"
	_, err := s.ForwardSave(bad)
	wantField(t, err, "kind")
	bad = ok
	bad.HostID = ""
	_, err = s.ForwardSave(bad)
	wantField(t, err, "host")
	bad.HostID = "missing"
	_, err = s.ForwardSave(bad)
	wantField(t, err, "host")
	for _, listen := range []string{"", "127.0.0.1:0", "127.0.0.1:70000", "127.0.0.1:abc", "a:b:c"} {
		bad = ok
		bad.Listen = listen
		_, err = s.ForwardSave(bad)
		wantField(t, err, "listen")
	}
	for _, target := range []string{"", "db.example.test", ":5432", "db.example.test:0"} {
		bad = ok
		bad.Target = target
		_, err = s.ForwardSave(bad)
		wantField(t, err, "target")
	}
	bad = ok
	bad.ID = "missing"
	if _, err = s.ForwardSave(bad); err == nil {
		t.Fatal("update of an unknown forward accepted")
	}
	if len(s.ForwardsList()) != 0 {
		t.Fatal("invalid forward saved")
	}

	in := ok
	in.Listen = " 15432 "
	in.Name = "  "
	f, err := s.ForwardSave(in)
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == "" || f.Listen != "127.0.0.1:15432" || f.Name != "L 127.0.0.1:15432 → db.example.test:5432" ||
		f.HostName != "srv" || f.Status != "stopped" {
		t.Fatalf("saved %+v", f)
	}

	d, err := s.ForwardSave(ForwardDTO{Kind: "D", HostID: s.hostID, Listen: ":1080", Target: "ignored.example.test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Listen != "127.0.0.1:1080" || d.Target != "" || d.Name != "SOCKS5 127.0.0.1:1080" {
		t.Fatalf("socks %+v", d)
	}

	r, err := s.ForwardSave(ForwardDTO{Kind: "R", HostID: s.hostID, Listen: "0.0.0.0:9000", Target: "127.0.0.1:3000", Name: " Вебхук "})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Вебхук" || r.Listen != "0.0.0.0:9000" {
		t.Fatalf("remote %+v", r)
	}
	if l := s.ForwardsList(); len(l) != 3 {
		t.Fatalf("list %+v", l)
	}
}

func TestForwardStartStopList(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	target := echoServer(t)
	listen := freePort(t)
	f := s.saveL(t, listen, target, false)

	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	if st := s.ev.fwdStates(f.ID); st[0].Status != "starting" || !st[0].Retryable {
		t.Fatalf("first state %+v", st)
	}
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatalf("second start of a running forward: %v", err)
	}
	roundTrip(t, listen)
	if got := listed(t, s.App, f.ID); got.Status != "running" || got.Error != "" {
		t.Fatalf("listed %+v", got)
	}

	n := len(s.ev.fwdStates(f.ID))
	start := time.Now()
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop took %v", d)
	}
	portFree(t, listen)
	s.ev.waitFwdState(t, f.ID, "stopped", n, 2*time.Second)
	if got := listed(t, s.App, f.ID); got.Status != "stopped" {
		t.Fatalf("listed after stop %+v", got)
	}
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatalf("stop of a stopped forward: %v", err)
	}

	// A busy port puts the error on the card.
	busy, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(busy.Close()) }()
	n = len(s.ev.fwdStates(f.ID))
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	st := s.ev.waitFwdState(t, f.ID, "error", n, 5*time.Second)
	if !strings.Contains(st.Error, listen) {
		t.Fatalf("busy port error %+v", st)
	}
	if got := listed(t, s.App, f.ID); got.Status != "error" || !strings.Contains(got.Error, listen) {
		t.Fatalf("listed error %+v", got)
	}
	ignore(busy.Close())
	n = len(s.ev.fwdStates(f.ID))
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)

	if err := s.ForwardDelete(f.ID); err != nil {
		t.Fatal(err)
	}
	portFree(t, listen)
	if len(s.ForwardsList()) != 0 {
		t.Fatal("deleted forward still listed")
	}
	if err := s.ForwardStart(f.ID); err == nil {
		t.Fatal("start of a deleted forward succeeded")
	}
}

func TestForwardSaveRestartsOnlyOnEndpointChange(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	target := echoServer(t)
	listen := freePort(t)
	f := s.saveL(t, listen, target, false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)

	n := len(s.ev.fwdStates(f.ID))
	f.Name = "Postgres"
	f.AutoStart = true
	if _, err := s.ForwardSave(f); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := s.ev.fwdStates(f.ID)[n:]; len(got) != 0 {
		t.Fatalf("renaming restarted the forward: %+v", got)
	}
	if got := listed(t, s.App, f.ID); got.Name != "Postgres" || !got.AutoStart || got.Status != "running" {
		t.Fatalf("listed %+v", got)
	}

	moved := freePort(t)
	f.Listen = moved
	if _, err := s.ForwardSave(f); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
	portFree(t, listen)
	roundTrip(t, moved)
}

func TestForwardAutostartOnStartup(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	target := echoServer(t)
	auto := s.saveL(t, freePort(t), target, true)
	manual := s.saveL(t, freePort(t), target, false)

	// Restart: a fresh App over the same vault and known_hosts.
	b := &App{emit: s.ev}
	t.Cleanup(b.stopAllForwards)
	b.initVault()
	if err := b.ForwardAttach(auto.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, auto.ID, "running", 0, 5*time.Second)
	roundTrip(t, auto.Listen)
	b.autostartForwards() // idempotent: the running forward is left alone
	time.Sleep(100 * time.Millisecond)
	starts := 0
	for _, st := range s.ev.fwdStates(auto.ID) {
		if st.Status == "starting" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("autostart ran twice: %+v", s.ev.fwdStates(auto.ID))
	}
	if got := listed(t, b, manual.ID); got.Status != "stopped" {
		t.Fatalf("manual forward started: %+v", got)
	}
	b.stopAllForwards()
	portFree(t, auto.Listen)

	// A password vault starts its forwards after the unlock.
	if err := b.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	c := &App{emit: s.ev}
	t.Cleanup(c.stopAllForwards)
	c.initVault()
	if !c.VaultState().Locked || len(c.ForwardsList()) != 0 {
		t.Fatal("locked vault lists forwards")
	}
	n := len(s.ev.fwdStates(auto.ID))
	if err := c.VaultUnlock("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := c.ForwardAttach(auto.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, auto.ID, "running", n, 5*time.Second)
	roundTrip(t, auto.Listen)
}

func TestForwardKeepaliveClosesFrozenClient(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	s.fwdKeepalive = 50 * time.Millisecond
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	time.Sleep(200 * time.Millisecond) // answered keepalives keep the client
	n := len(s.ev.fwdStates(f.ID))
	if n != 2 {
		t.Fatalf("live server lost the client: %+v", s.ev.fwdStates(f.ID))
	}

	s.srv.FreezeConnections()
	frozen := time.Now()
	s.ev.waitFwdState(t, f.ID, "starting", n, 2*time.Second)
	if d := time.Since(frozen); d > 8*s.fwdKeepalive+time.Second {
		t.Fatalf("frozen client closed after %v", d)
	}
}

func TestForwardPromptAndStopDuringPrompt(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{Password: "s3cret"}, false)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)

	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFor(t, 5*time.Second, "password prompt", func([]fakeEvent) bool {
		return strings.Contains(s.ev.fwdData(t, f.ID), "Пароль alice@")
	})
	if got := listed(t, s.App, f.ID); got.Status != "starting" {
		t.Fatalf("prompting forward %+v", got)
	}
	start := time.Now()
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop during a prompt took %v", d)
	}
	portFree(t, listen)

	n := len(s.ev.fwdStates(f.ID))
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFor(t, 5*time.Second, "second prompt", func([]fakeEvent) bool {
		return strings.Count(s.ev.fwdData(t, f.ID), "Пароль alice@") == 2
	})
	if err := s.ForwardWrite(f.ID, base64.StdEncoding.EncodeToString([]byte("s3cret\r"))); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
	if strings.Contains(s.ev.fwdData(t, f.ID), "s3cret") {
		t.Fatal("password echoed")
	}
	roundTrip(t, listen)
}

func TestHostDeleteStopsForwards(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)

	if err := s.HostDelete(s.hostID); err != nil {
		t.Fatal(err)
	}
	portFree(t, f.Listen)
	if l := s.ForwardsList(); len(l) != 0 {
		t.Fatalf("forwards of the deleted host still listed: %+v", l)
	}
	if len(s.storeRef().Forwards()) != 0 {
		t.Fatal("forwards of the deleted host still stored")
	}
}

// fwdPassphrase is the test phrase of the encrypted forward key; distinctive enough to grep for.
const fwdPassphrase = "fwd-phrase-7Qx9" //nolint:gosec // G101: test-only passphrase of a key generated in the test

// newEncryptedForwardsApp is newForwardsApp whose host uses an Ed25519 key encrypted with fwdPassphrase.
func newEncryptedForwardsApp(t *testing.T) sessionsApp {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(fwdPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	s := newForwardsApp(t, sshtest.Opts{AcceptKey: sshPub}, false)
	k, err := s.storeRef().SaveKey(model.Key{Name: "work", Type: "ed25519", Private: pem.EncodeToMemory(block),
		Format: "openssh", Encrypted: true, Fingerprint: ssh.FingerprintSHA256(sshPub)})
	if err != nil {
		t.Fatal(err)
	}
	h := s.storeRef().Hosts()[0]
	h.KeyID = k.ID
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	return s
}

const passphrasePrompt = "Парольная фраза ключа work: " //nolint:gosec // G101: prompt text, not a credential

// waitPassphrasePrompts waits until the forward's mini-terminal shows the n-th passphrase question.
func (s sessionsApp) waitPassphrasePrompts(t *testing.T, id string, n int) {
	t.Helper()
	s.ev.waitFor(t, 5*time.Second, "passphrase prompt", func([]fakeEvent) bool {
		return strings.Count(s.ev.fwdData(t, id), passphrasePrompt) >= n
	})
}

func (s sessionsApp) answer(t *testing.T, id, text string) {
	t.Helper()
	if err := s.ForwardWrite(id, base64.StdEncoding.EncodeToString([]byte(text+"\r"))); err != nil {
		t.Fatal(err)
	}
}

// startWithPassphrase starts f, answers the passphrase question and waits for running.
func (s sessionsApp) startWithPassphrase(t *testing.T, f ForwardDTO) {
	t.Helper()
	n := len(s.ev.fwdStates(f.ID))
	asked := strings.Count(s.ev.fwdData(t, f.ID), passphrasePrompt)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitPassphrasePrompts(t, f.ID, asked+1)
	s.answer(t, f.ID, fwdPassphrase)
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
}

// secretForTest is the run's remembered passphrase buffer itself (not a copy).
func (r *fwdRun) secretForTest() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.secret
}

func zeroBytes(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// runSecret returns the current run's secret and checks it holds the phrase.
func (s sessionsApp) runSecret(t *testing.T, id string) (*fwdRun, []byte) {
	t.Helper()
	run := s.currentRun(id)
	if run == nil {
		t.Fatal("forward has no run")
	}
	sec := run.secretForTest()
	if string(sec) != fwdPassphrase {
		t.Fatalf("remembered passphrase %q", sec)
	}
	return run, sec
}

func checkForgotten(t *testing.T, run *fwdRun, buf []byte) {
	t.Helper()
	if run.secretForTest() != nil || !zeroBytes(buf) {
		t.Fatalf("passphrase kept: run %q, buffer %q", run.secretForTest(), buf)
	}
}

func TestForwardReconnectDoesNotReaskPassphrase(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)
	s.startWithPassphrase(t, f)
	roundTrip(t, listen)

	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	s.ev.waitFwdState(t, f.ID, "starting", n, 5*time.Second)
	s.ev.waitFwdState(t, f.ID, "running", n+1, 10*time.Second)
	roundTrip(t, listen)
	out := s.ev.fwdData(t, f.ID)
	if c := strings.Count(out, passphrasePrompt); c != 1 {
		t.Fatalf("passphrase asked %d times: %q", c, out)
	}
	if strings.Contains(out, fwdPassphrase) {
		t.Fatal("passphrase echoed")
	}
}

func TestForwardStopClearsPassphrase(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	run, buf := s.runSecret(t, f.ID)
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatal(err)
	}
	checkForgotten(t, run, buf)

	// A new start asks again.
	s.startWithPassphrase(t, f)
	if c := strings.Count(s.ev.fwdData(t, f.ID), passphrasePrompt); c != 2 {
		t.Fatalf("passphrase asked %d times", c)
	}

	// Delete and app exit forget it too.
	run, buf = s.runSecret(t, f.ID)
	if err := s.ForwardDelete(f.ID); err != nil {
		t.Fatal(err)
	}
	checkForgotten(t, run, buf)

	g := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, g)
	run, buf = s.runSecret(t, g.ID)
	s.stopAllForwards()
	checkForgotten(t, run, buf)
}

func TestForwardHostChangeClearsPassphrase(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	run, buf := s.runSecret(t, f.ID)

	other := s.storeRef().Hosts()[0]
	other.ID = ""
	other.Name = "srv2"
	h2, err := s.storeRef().SaveHost(other)
	if err != nil {
		t.Fatal(err)
	}
	f.HostID = h2.ID
	if _, err := s.ForwardSave(f); err != nil {
		t.Fatal(err)
	}
	checkForgotten(t, run, buf)
	s.waitPassphrasePrompts(t, f.ID, 2)
}

func TestForwardErrorClearsPassphrase(t *testing.T) {
	// The reconnect meets an unknown server key and the user refuses: the run ends in error and
	// stays listed until Start or Stop, but the passphrase is forgotten at once.
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	run, buf := s.runSecret(t, f.ID)

	untrust(t)
	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	q := s.ev.waitTrustAsk(t, 1)
	if err := s.TrustAnswer(q.ID, false); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "error", n, 5*time.Second)
	s.ev.waitFor(t, 2*time.Second, "passphrase forgotten", func([]fakeEvent) bool {
		return run.secretForTest() == nil
	})
	checkForgotten(t, run, buf)
}

func TestForwardSecretNeverInStore(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)
	s.startWithPassphrase(t, f)
	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	s.ev.waitFwdState(t, f.ID, "running", n+1, 10*time.Second)
	// Any store write after the phrase is known goes through the same encoder.
	g := f
	g.Name = "renamed"
	if _, err := s.ForwardSave(g); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(s.vaultRef().Data())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(fwdPassphrase)) {
		t.Fatal("passphrase in the vault data")
	}
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // G304: path is under the test's temp app dir
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(fwdPassphrase)) {
			t.Errorf("passphrase in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range s.ev.snapshot() {
		if b, err := json.Marshal(e.payload); err != nil || bytes.Contains(b, []byte(fwdPassphrase)) {
			t.Fatalf("passphrase in event %s (%v)", e.name, err)
		}
	}
	if strings.Contains(s.ev.fwdData(t, f.ID), fwdPassphrase) {
		t.Fatal("passphrase in the mini-terminal output")
	}
	if b, err := json.Marshal(s.ForwardsList()); err != nil || bytes.Contains(b, []byte(fwdPassphrase)) {
		t.Fatalf("passphrase in ForwardsList (%v)", err)
	}
}

func TestForwardStateDTOCarriesDetail(t *testing.T) {
	r := &fwdRun{}
	dto := r.dto(sshclient.ForwardState{Status: sshclient.ForwardError, Error: "Сервер h отклонил подключение", Detail: "dial tcp: connectex"})
	if dto.Detail != "dial tcp: connectex" || dto.Error == "" {
		t.Fatalf("dto %+v", dto)
	}
}
