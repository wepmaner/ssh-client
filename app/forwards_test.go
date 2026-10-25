package app

import (
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"

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
	if len(s.store.Forwards()) != 0 {
		t.Fatal("forwards of the deleted host still stored")
	}
}
