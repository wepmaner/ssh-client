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

// groupDataOf is the decoded fwdhost:data:<hostID> output of a host's forwards group.
func (f *fakeEmitter) groupDataOf(t *testing.T, hostID string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range f.snapshot() {
		if e.name == "fwdhost:data:"+hostID {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

// hostStates returns the fwdhost:state:<hostID> payloads recorded so far.
func (f *fakeEmitter) hostStates(hostID string) []HostConnStateDTO {
	var out []HostConnStateDTO
	for _, e := range f.snapshot() {
		if e.name == "fwdhost:state:"+hostID {
			out = append(out, e.payload.(HostConnStateDTO))
		}
	}
	return out
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
	t.Cleanup(s.closeAllHostConns) // runs after stopAllForwards
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
	if err := s.ForwardHostAttach(s.hostID); err != nil {
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
	if f.ID == "" || f.Listen != "127.0.0.1:15432" || f.Name != "L 15432 → db.example.test:5432" ||
		f.HostName != "srv" || f.Status != "stopped" {
		t.Fatalf("saved %+v", f)
	}

	d, err := s.ForwardSave(ForwardDTO{Kind: "D", HostID: s.hostID, Listen: ":1080", Target: "ignored.example.test:1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Listen != "127.0.0.1:1080" || d.Target != "" || d.Name != "SOCKS5 1080" {
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
	if !strings.Contains(st.Error, "уже занят") || !strings.Contains(st.Detail, listen) {
		t.Fatalf("busy port error %+v", st)
	}
	if got := listed(t, s.App, f.ID); got.Status != "error" || !strings.Contains(got.Error, "уже занят") {
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
	t.Cleanup(b.closeAllHostConns)
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
	t.Cleanup(c.closeAllHostConns)
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
		return strings.Contains(s.groupData(t), "Пароль alice@")
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
		return strings.Count(s.groupData(t), "Пароль alice@") == 2
	})
	s.answer(t, s.hostID, "s3cret")
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
	if strings.Contains(s.groupData(t), "s3cret") {
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
	s.checkNoConns(t, s.hostID)
}

// checkNoConns fails if any connection of hostID, stale ones included, is still known.
func (s sessionsApp) checkNoConns(t *testing.T, hostID string) {
	t.Helper()
	s.hcMu.Lock()
	defer s.hcMu.Unlock()
	if s.hostConns[hostID] != nil {
		t.Fatal("the host still has a current connection")
	}
	for c := range s.hcLive {
		if c.hostID == hostID {
			t.Fatal("a connection of the host is still live")
		}
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

// waitHostPrompts waits until hostID's forwards group shows the n-th passphrase question.
func (s sessionsApp) waitHostPrompts(t *testing.T, hostID string, n int) {
	t.Helper()
	s.ev.waitFor(t, 5*time.Second, "passphrase prompt", func([]fakeEvent) bool {
		return strings.Count(s.ev.groupDataOf(t, hostID), passphrasePrompt) >= n
	})
}

func (s sessionsApp) prompts(t *testing.T) int {
	t.Helper()
	return strings.Count(s.groupData(t), passphrasePrompt)
}

// answer types text and Enter into hostID's forwards group through ForwardHostWrite.
func (s sessionsApp) answer(t *testing.T, hostID, text string) {
	t.Helper()
	if err := s.ForwardHostWrite(hostID, base64.StdEncoding.EncodeToString([]byte(text+"\r"))); err != nil {
		t.Fatal(err)
	}
}

// startWithPassphrase starts f, answers the passphrase question in its host's group and waits for running.
func (s sessionsApp) startWithPassphrase(t *testing.T, f ForwardDTO) {
	t.Helper()
	n := len(s.ev.fwdStates(f.ID))
	asked := strings.Count(s.ev.groupDataOf(t, f.HostID), passphrasePrompt)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitHostPrompts(t, f.HostID, asked+1)
	s.answer(t, f.HostID, fwdPassphrase)
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
}

func zeroBytes(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// connSecret returns the passphrase buffer of the host's current connection and checks it holds the phrase.
func (s sessionsApp) connSecret(t *testing.T, hostID string) []byte {
	t.Helper()
	s.hcMu.Lock()
	c := s.hostConns[hostID]
	s.hcMu.Unlock()
	if c == nil {
		t.Fatal("the host has no connection")
	}
	sec, _ := c.secretsForTest()
	if string(sec) != fwdPassphrase {
		t.Fatalf("remembered passphrase %q", sec)
	}
	return sec
}

func checkWiped(t *testing.T, buf []byte) {
	t.Helper()
	if !zeroBytes(buf) {
		t.Fatalf("passphrase kept: %q", buf)
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
	out := s.groupData(t)
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
	buf := s.connSecret(t, s.hostID)
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatal(err)
	}
	checkWiped(t, buf)
	s.checkNoConns(t, s.hostID)

	// A new start asks again.
	s.startWithPassphrase(t, f)
	if c := s.prompts(t); c != 2 {
		t.Fatalf("passphrase asked %d times", c)
	}

	// Delete and app exit forget it too.
	buf = s.connSecret(t, s.hostID)
	if err := s.ForwardDelete(f.ID); err != nil {
		t.Fatal(err)
	}
	checkWiped(t, buf)

	g := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, g)
	buf = s.connSecret(t, s.hostID)
	s.stopAllForwards()
	checkWiped(t, buf)
}

func TestForwardHostChangeClearsPassphrase(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	buf := s.connSecret(t, s.hostID)

	other := s.storeRef().Hosts()[0]
	other.ID = ""
	other.Name = "srv2"
	h2, err := s.storeRef().SaveHost(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ForwardHostAttach(h2.ID); err != nil {
		t.Fatal(err)
	}
	n := len(s.ev.fwdStates(f.ID))
	f.HostID = h2.ID
	if _, err := s.ForwardSave(f); err != nil {
		t.Fatal(err)
	}
	// The forward was the old host's only user: that connection closed and forgot the phrase.
	checkWiped(t, buf)
	s.checkNoConns(t, s.hostID)
	s.waitHostPrompts(t, h2.ID, 1)
	s.answer(t, h2.ID, fwdPassphrase)
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
	roundTrip(t, f.Listen)
}

func TestForwardErrorClearsPassphrase(t *testing.T) {
	// The reconnect meets an unknown server key and the user refuses: the forward ends in error and
	// the connection forgets the passphrase at once.
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.startWithPassphrase(t, f)
	buf := s.connSecret(t, s.hostID)

	untrust(t)
	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	q := s.ev.waitTrustAsk(t, 1)
	if q.ForwardHostID != s.hostID {
		t.Fatalf("trust question %+v", q)
	}
	if err := s.TrustAnswer(q.ID, false); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "error", n, 5*time.Second)
	checkWiped(t, buf)
	portFree(t, f.Listen)
}

// checkSecretNowhere fails if secret reached the vault, a file of the app dir, an event or a list.
func (s sessionsApp) checkSecretNowhere(t *testing.T, secret string) {
	t.Helper()
	data, err := json.Marshal(s.vaultRef().Data())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("secret in the vault data")
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
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("secret in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range s.ev.snapshot() {
		if b, err := json.Marshal(e.payload); err != nil || bytes.Contains(b, []byte(secret)) {
			t.Fatalf("secret in event %s (%v)", e.name, err)
		}
	}
	if strings.Contains(s.groupData(t), secret) {
		t.Fatal("secret in the group's mini-terminal output")
	}
	if b, err := json.Marshal(s.ForwardsList()); err != nil || bytes.Contains(b, []byte(secret)) {
		t.Fatalf("secret in ForwardsList (%v)", err)
	}
	if b, err := json.Marshal(s.ForwardHostsList()); err != nil || bytes.Contains(b, []byte(secret)) {
		t.Fatalf("secret in ForwardHostsList (%v)", err)
	}
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
	s.checkSecretNowhere(t, fwdPassphrase)
}

func TestForwardPasswordNeverInStore(t *testing.T) {
	const password = "pw-for-forwards-3Kd8" //nolint:gosec // G101: test-only password of the test server
	s := newForwardsApp(t, sshtest.Opts{Password: password}, false)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.waitGroupData(t, "Пароль alice@", 1)
	s.answer(t, s.hostID, password)
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	n := len(s.ev.fwdStates(f.ID))
	s.srv.DropConnections()
	s.ev.waitFwdState(t, f.ID, "running", n+1, 10*time.Second)
	if c := strings.Count(s.groupData(t), "Пароль alice@"); c != 1 {
		t.Fatalf("password asked %d times", c)
	}
	g := f
	g.Name = "renamed"
	if _, err := s.ForwardSave(g); err != nil {
		t.Fatal(err)
	}
	s.checkSecretNowhere(t, password)
}

func TestForwardStateDTOCarriesDetail(t *testing.T) {
	dto := fwdStateDTO(sshclient.ForwardState{Status: sshclient.ForwardError, Error: "Сервер h отклонил подключение", Detail: "dial tcp: connectex"}, true)
	if dto.Detail != "dial tcp: connectex" || dto.Error == "" || !dto.Retryable {
		t.Fatalf("dto %+v", dto)
	}
}

// startThree saves three L forwards of the host and starts them with ForwardStartAll, answering
// the one passphrase question.
func (s sessionsApp) startThree(t *testing.T) []ForwardDTO {
	t.Helper()
	target := echoServer(t)
	fwds := []ForwardDTO{s.saveL(t, freePort(t), target, false), s.saveL(t, freePort(t), target, false), s.saveL(t, freePort(t), target, false)}
	if err := s.ForwardStartAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	s.waitHostPrompts(t, s.hostID, 1)
	s.answer(t, s.hostID, fwdPassphrase)
	for _, f := range fwds {
		s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	}
	return fwds
}

func TestForwardStartAllAsksPassphraseOnce(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	fwds := s.startThree(t)
	time.Sleep(100 * time.Millisecond) // a second question would be on its way by now
	if c := s.prompts(t); c != 1 {
		t.Fatalf("passphrase asked %d times: %q", c, s.groupData(t))
	}
	for _, f := range fwds {
		roundTrip(t, f.Listen)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
	if err := s.ForwardStartAll(s.hostID); err != nil { // running forwards are left alone
		t.Fatal(err)
	}
	if err := s.ForwardStartAll("missing"); err == nil {
		t.Fatal("start all of an unknown host succeeded")
	}
}

func TestForwardStartOnLiveConnectionNoPrompt(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	s.startThree(t)
	before := s.groupData(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	roundTrip(t, f.Listen)
	if after := s.groupData(t); after != before {
		t.Fatalf("the group printed %q on a live connection", strings.TrimPrefix(after, before))
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
}

func TestForwardStopOneKeepsOthers(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	fwds := s.startThree(t)
	keep := fwds[1]
	c, err := net.DialTimeout("tcp", keep.Listen, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ignore(c.Close()) }()
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	echo := func(text string) {
		t.Helper()
		if _, err := c.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(text))
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != text {
			t.Fatalf("echo through the kept forward: %q %v", buf, err)
		}
	}
	echo("before")
	s.ev.waitFor(t, 2*time.Second, "one connection through the kept forward", func([]fakeEvent) bool {
		return listed(t, s.App, keep.ID).Conns == 1
	})

	if err := s.ForwardStop(fwds[0].ID); err != nil {
		t.Fatal(err)
	}
	portFree(t, fwds[0].Listen)
	echo("after") // the open connection through another forward was not cut
	if got := listed(t, s.App, keep.ID); got.Status != "running" || got.Conns != 1 {
		t.Fatalf("kept forward %+v", got)
	}
	roundTrip(t, fwds[2].Listen)
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
}

func TestForwardSaveKeepsConnection(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	old := freePort(t)
	f := s.saveL(t, old, echoServer(t), false)
	s.startWithPassphrase(t, f)

	n := len(s.ev.fwdStates(f.ID))
	f.Listen = freePort(t)
	if _, err := s.ForwardSave(f); err != nil {
		t.Fatal(err)
	}
	portFree(t, old)
	s.ev.waitFwdState(t, f.ID, "running", n, 5*time.Second)
	roundTrip(t, f.Listen)
	if c := s.prompts(t); c != 1 {
		t.Fatalf("passphrase asked %d times", c)
	}
	if n := s.srv.Accepted(); n != 1 {
		t.Fatalf("server accepted %d connections, want 1", n)
	}
}

func TestForwardStopAllClosesConnection(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	fwds := s.startThree(t)
	buf := s.connSecret(t, s.hostID)
	if st := s.ev.hostStates(s.hostID); len(st) == 0 || st[len(st)-1].Status != "connected" {
		t.Fatalf("group states %+v", st)
	}
	if err := s.ForwardStopAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	for _, f := range fwds {
		portFree(t, f.Listen)
		if got := listed(t, s.App, f.ID); got.Status != "stopped" {
			t.Fatalf("after stop all %+v", got)
		}
	}
	checkWiped(t, buf)
	s.checkNoConns(t, s.hostID)
	s.ev.waitFor(t, 2*time.Second, "group state none", func([]fakeEvent) bool {
		st := s.ev.hostStates(s.hostID)
		return st[len(st)-1].Status == "none"
	})
	if err := s.ForwardStopAll(s.hostID); err != nil { // nothing left to stop
		t.Fatal(err)
	}
}

func TestForwardHostsList(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	if l := s.ForwardHostsList(); len(l) != 0 {
		t.Fatalf("hosts without forwards listed: %+v", l)
	}
	h := s.storeRef().Hosts()[0]
	h.ID, h.Name = "", "alpha"
	alpha, err := s.storeRef().SaveHost(h)
	if err != nil {
		t.Fatal(err)
	}
	h.ID, h.Name = "", "zulu" // no forwards: not listed
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if _, err := s.ForwardSave(ForwardDTO{Kind: "D", HostID: alpha.ID, Listen: freePort(t)}); err != nil {
		t.Fatal(err)
	}
	l := s.ForwardHostsList()
	if len(l) != 2 || l[0].HostName != "alpha" || l[0].HostID != alpha.ID || l[1].HostName != "srv" ||
		l[0].Status != "none" || !l[0].Retryable {
		t.Fatalf("hosts %+v", l)
	}
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	if l := s.ForwardHostsList(); l[1].Status != "connected" {
		t.Fatalf("hosts after start %+v", l)
	}
}

func TestForwardDefaultNameShortListen(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	f, err := s.ForwardSave(ForwardDTO{Kind: "L", HostID: s.hostID, Listen: "5432", Target: "db.example.test:5432"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "L 5432 → db.example.test:5432" || f.Listen != "127.0.0.1:5432" {
		t.Fatalf("forward %+v", f)
	}
	r, err := s.ForwardSave(ForwardDTO{Kind: "R", HostID: s.hostID, Listen: "0.0.0.0:9000", Target: "127.0.0.1:3000"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Name, "0.0.0.0:9000") {
		t.Fatalf("forward %+v", r)
	}
}

func TestHostSaveChangedUserMarksStale(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	target := echoServer(t)
	a := s.saveL(t, freePort(t), target, false)
	s.startWithPassphrase(t, a)
	buf := s.connSecret(t, s.hostID)

	h, err := s.hostDTO(s.hostID)
	if err != nil {
		t.Fatal(err)
	}
	h.Name = "renamed" // not a login parameter: the connection stays current
	if _, err := s.HostSave(h); err != nil {
		t.Fatal(err)
	}
	if string(s.connSecret(t, s.hostID)) != fwdPassphrase {
		t.Fatal("renaming the host dropped the secret")
	}
	h.User = "bob"
	if _, err := s.HostSave(h); err != nil {
		t.Fatal(err)
	}
	checkWiped(t, buf)
	roundTrip(t, a.Listen) // the running forward stays on its connection

	b := s.saveL(t, freePort(t), target, false)
	s.startWithPassphrase(t, b)
	roundTrip(t, b.Listen)
	roundTrip(t, a.Listen)
	if n := s.srv.Accepted(); n != 2 {
		t.Fatalf("server accepted %d connections, want 2", n)
	}
}

func TestForwardHostErrorSurvivesLastLeave(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	target := echoServer(t)
	fwds := []ForwardDTO{s.saveL(t, freePort(t), target, false), s.saveL(t, freePort(t), target, false)}
	if err := s.ForwardStartAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		s.waitHostPrompts(t, s.hostID, i)
		s.answer(t, s.hostID, "wrong")
	}
	for _, f := range fwds {
		s.ev.waitFwdState(t, f.ID, "error", 0, 5*time.Second)
	}
	s.ev.waitFor(t, 2*time.Second, "every forward left the connection", func([]fakeEvent) bool {
		s.hcMu.Lock()
		defer s.hcMu.Unlock()
		return len(s.hcLive) == 0
	})
	s.checkNoConns(t, s.hostID)
	time.Sleep(50 * time.Millisecond) // a late none would be out by now
	st := s.ev.hostStates(s.hostID)
	last := st[len(st)-1]
	if last.Status != "error" || last.Error == "" || !last.Retryable {
		t.Fatalf("group state after the last forward left: %+v", st)
	}
	l := s.ForwardHostsList()
	if len(l) != 1 || l[0].Status != "error" || l[0].Error != last.Error || l[0].Detail != last.Detail {
		t.Fatalf("listed %+v, last state %+v", l, last)
	}

	// The next login replaces it.
	n := len(st)
	if err := s.ForwardStart(fwds[0].ID); err != nil {
		t.Fatal(err)
	}
	s.waitHostPrompts(t, s.hostID, 4)
	if st := s.ev.hostStates(s.hostID)[n:]; len(st) == 0 || st[0].Status != "connecting" {
		t.Fatalf("states of the next login %+v", st)
	}
	if l := s.ForwardHostsList(); l[0].Status != "connecting" {
		t.Fatalf("listed during the next login %+v", l)
	}
	s.answer(t, s.hostID, fwdPassphrase)
	s.ev.waitFwdState(t, fwds[0].ID, "running", 0, 5*time.Second)

	// Stopping everything by hand shows none.
	if err := s.ForwardStopAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	if l := s.ForwardHostsList(); l[0].Status != "none" || l[0].Error != "" {
		t.Fatalf("listed after stop all %+v", l)
	}
}

func TestForwardStopAllClearsHostError(t *testing.T) {
	s := newEncryptedForwardsApp(t)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		s.waitHostPrompts(t, s.hostID, i)
		s.answer(t, s.hostID, "wrong")
	}
	s.ev.waitFwdState(t, f.ID, "error", 0, 5*time.Second)
	s.ev.waitFor(t, 2*time.Second, "group error", func([]fakeEvent) bool {
		return s.ForwardHostsList()[0].Status == "error"
	})
	if err := s.ForwardStopAll(s.hostID); err != nil {
		t.Fatal(err)
	}
	st := s.ev.hostStates(s.hostID)
	if last := st[len(st)-1]; last.Status != "none" {
		t.Fatalf("group state after stop all %+v", st)
	}
	if l := s.ForwardHostsList(); l[0].Status != "none" {
		t.Fatalf("listed after stop all %+v", l)
	}
}
