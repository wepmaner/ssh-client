package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"sshclient/internal/appdir"
	"sshclient/internal/sshclient"
	"sshclient/internal/sshclient/sshtest"
)

// knownHostsPath is the app's known_hosts in the test's app dir.
func knownHostsPath(t *testing.T) string {
	t.Helper()
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "known_hosts")
}

// untrust empties known_hosts, so the test server's key is unknown again.
func untrust(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(knownHostsPath(t), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readKnownHosts(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(knownHostsPath(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// trustAsks returns the trust:ask payloads recorded so far.
func (f *fakeEmitter) trustAsks() []TrustAskDTO {
	var out []TrustAskDTO
	for _, e := range f.snapshot() {
		if e.name == "trust:ask" {
			out = append(out, e.payload.(TrustAskDTO))
		}
	}
	return out
}

// waitTrustAsk waits for the n-th (from 1) trust:ask event.
func (f *fakeEmitter) waitTrustAsk(t *testing.T, n int) TrustAskDTO {
	t.Helper()
	f.waitFor(t, 5*time.Second, "trust:ask", func([]fakeEvent) bool { return len(f.trustAsks()) >= n })
	return f.trustAsks()[n-1]
}

// waitTrustCancel waits for trust:cancel of the question id.
func (f *fakeEmitter) waitTrustCancel(t *testing.T, id string) {
	t.Helper()
	f.waitFor(t, 2*time.Second, "trust:cancel "+id, func(all []fakeEvent) bool {
		for _, e := range all {
			if e.name == "trust:cancel" && e.payload.(TrustCancelDTO).ID == id {
				return true
			}
		}
		return false
	})
}

// checkQuestion compares a trust:ask payload with the test server.
func checkQuestion(t *testing.T, q TrustAskDTO, srv *sshtest.Server) {
	t.Helper()
	if q.ID == "" || q.Host != "127.0.0.1" || fmt.Sprint(q.Port) != srv.Addr()[strings.LastIndex(srv.Addr(), ":")+1:] ||
		q.User != "alice" || q.KeyType != srv.HostKey().Type() || q.Fingerprint != ssh.FingerprintSHA256(srv.HostKey()) {
		t.Fatalf("question %+v, server %s %s", q, srv.Addr(), ssh.FingerprintSHA256(srv.HostKey()))
	}
}

func infoLine(srv *sshtest.Server) string {
	return "Сервер " + srv.Addr() + " неизвестен, отпечаток " + ssh.FingerprintSHA256(srv.HostKey()) + "\r\n"
}

func TestTrustAnswerUnknownID(t *testing.T) {
	a := &App{}
	if err := a.TrustAnswer("no-such-question", true); err == nil {
		t.Fatal("answer to an unknown question accepted")
	}
}

func TestTrustAnswerConnects(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{}, true)
	untrust(t)
	id := s.open(t)
	q := s.ev.waitTrustAsk(t, 1)
	checkQuestion(t, q, s.srv)
	if q.SessionID != id || q.ForwardID != "" {
		t.Fatalf("owner of %+v, session %s", q, id)
	}
	if !strings.Contains(s.ev.data(t, id), infoLine(s.srv)) {
		t.Fatalf("terminal: %q", s.ev.data(t, id))
	}
	if st := s.ev.states(id); st[len(st)-1].State != "connecting" {
		t.Fatalf("states while asking %+v", st)
	}
	if err := s.TrustAnswer(q.ID, true); err != nil {
		t.Fatal(err)
	}
	s.ev.waitState(t, id, "connected", 5*time.Second)
	want := knownhosts.Line([]string{knownhosts.Normalize(s.srv.Addr())}, s.srv.HostKey()) + "\n"
	if got := readKnownHosts(t); got != want {
		t.Fatalf("known_hosts %q, want %q", got, want)
	}
	if err := s.TrustAnswer(q.ID, true); err == nil {
		t.Fatal("a question answered twice")
	}
	// The key is known now: the next session connects without a question.
	id2 := s.open(t)
	s.ev.waitState(t, id2, "connected", 5*time.Second)
	if n := len(s.ev.trustAsks()); n != 1 {
		t.Fatalf("%d questions", n)
	}
}

func TestTrustRefusedEndsSession(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{}, true)
	untrust(t)
	id := s.open(t)
	q := s.ev.waitTrustAsk(t, 1)
	if err := s.TrustAnswer(q.ID, false); err != nil {
		t.Fatal(err)
	}
	st := s.ev.waitState(t, id, "error", 5*time.Second)
	if st.Kind != "cancelled" || st.Message != "Подключение отменено: сервер не подтверждён" || !st.Retryable {
		t.Fatalf("error payload %+v", st)
	}
	if got := readKnownHosts(t); got != "" {
		t.Fatalf("known_hosts written: %q", got)
	}
}

func TestTrustCancelledWithSession(t *testing.T) {
	s := newSessionsApp(t, sshtest.Opts{}, true)
	untrust(t)
	id := s.open(t)
	q := s.ev.waitTrustAsk(t, 1)
	start := time.Now()
	if err := s.SessionClose(id); err != nil {
		t.Fatal(err)
	}
	s.ev.waitTrustCancel(t, q.ID)
	if st := s.ev.waitState(t, id, "error", 2*time.Second); st.Kind != "cancelled" {
		t.Fatalf("payload %+v", st)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Dial returned after %v", d)
	}
	if err := s.TrustAnswer(q.ID, true); err == nil {
		t.Fatal("a withdrawn question accepted an answer")
	}
	if got := readKnownHosts(t); got != "" {
		t.Fatalf("known_hosts written: %q", got)
	}
}

func TestForwardTrustAnswer(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	untrust(t)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	q := s.ev.waitTrustAsk(t, 1)
	checkQuestion(t, q, s.srv)
	if q.ForwardID != f.ID || q.SessionID != "" {
		t.Fatalf("owner of %+v, forward %s", q, f.ID)
	}
	if !strings.Contains(s.ev.fwdData(t, f.ID), infoLine(s.srv)) {
		t.Fatalf("mini-terminal: %q", s.ev.fwdData(t, f.ID))
	}
	if err := s.TrustAnswer(q.ID, true); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	roundTrip(t, listen)
	if got := readKnownHosts(t); !strings.Contains(got, s.srv.HostKey().Type()) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestForwardTrustRefusedAndStopped(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	untrust(t)
	listen := freePort(t)
	f := s.saveL(t, listen, echoServer(t), false)

	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	q := s.ev.waitTrustAsk(t, 1)
	if err := s.TrustAnswer(q.ID, false); err != nil {
		t.Fatal(err)
	}
	st := s.ev.waitFwdState(t, f.ID, "error", 0, 5*time.Second)
	if st.Error != "Подключение отменено: сервер не подтверждён" || !st.Retryable {
		t.Fatalf("state %+v", st)
	}
	portFree(t, listen)

	// Stop while the question is open withdraws it.
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	q2 := s.ev.waitTrustAsk(t, 2)
	start := time.Now()
	if err := s.ForwardStop(f.ID); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop during a question took %v", d)
	}
	s.ev.waitTrustCancel(t, q2.ID)
	if got := readKnownHosts(t); got != "" {
		t.Fatalf("known_hosts written: %q", got)
	}
}

func TestTrustDuplicateOwnerReplacesOlder(t *testing.T) {
	ev := newFakeEmitter()
	a := &App{emit: ev}
	out := newOutStream(ev, "s1")
	out.Attach()
	q := sshclient.TrustQuestion{Host: "example.test", Port: 22, KeyType: "ssh-ed25519", FingerprintSHA256: "SHA256:abc"}
	ask := func() chan error {
		done := make(chan error, 1)
		go func() {
			_, err := a.trustAsker(trustOwner{sessionID: "s1"}, "alice", out).AskTrust(context.Background(), q)
			done <- err
		}()
		return done
	}
	first := ask()
	q1 := ev.waitTrustAsk(t, 1)
	second := ask()
	q2 := ev.waitTrustAsk(t, 2)
	ev.waitTrustCancel(t, q1.ID)
	select {
	case err := <-first:
		if !errors.Is(err, sshclient.ErrCancelled) {
			t.Fatalf("older question: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("older question not replaced")
	}
	if err := a.TrustAnswer(q1.ID, true); err == nil {
		t.Fatal("replaced question accepted an answer")
	}
	if err := a.TrustAnswer(q2.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
