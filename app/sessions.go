package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/appdir"
	"sshclient/internal/sshclient"
)

const (
	stateConnecting = "connecting"
	stateConnected  = "connected"
	stateError      = "error"
	stateClosed     = "closed"

	terminalType    = "xterm-256color"
	latencyInterval = 15 * time.Second
)

// SessionStateDTO is the payload of term:state:<id>.
type SessionStateDTO struct {
	State     string `json:"state"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Message   string `json:"message,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Kind      string `json:"kind,omitempty"` // timeout|hostkey|auth|cancelled|other
	Retryable bool   `json:"retryable"`
}

// session is one terminal tab: dialling with prompts in the stream, then an interactive shell.
type session struct {
	id, hostID, hostName string
	seq                  int // opening order for SessionList

	out    *outStream
	in     *inputQueue // keystrokes for the Prompter until the shell starts
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when run has returned and every goroutine of the session ended

	wmu sync.Mutex // orders writes to the shell; never held together with mu waiting on the network

	mu         sync.Mutex
	state      string
	sess       *sshclient.Session
	client     *ssh.Client
	cols, rows int
	latencyMs  int
	closing    bool
}

func newSession(id, hostID, hostName string, cols, rows int, e emitter) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		id: id, hostID: hostID, hostName: hostName,
		out: newOutStream(e, id), in: newInputQueue(),
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		state: stateConnecting, cols: cols, rows: rows,
	}
}

func (s *session) stateEvent() string { return "term:state:" + s.id }

// run dials, starts the shell and pumps its output until the session ends.
func (s *session) run(t sshclient.Target, knownHosts func() (*sshclient.KnownHosts, error)) {
	defer close(s.done)
	defer s.cancel()
	s.out.Event(s.stateEvent(), SessionStateDTO{State: stateConnecting, Retryable: true}, false)
	_, _ = fmt.Fprintf(s.out, "Подключение к %s@%s:%d …\r\n", t.User, t.Address, t.Port) //nolint:gosec // G104: outStream never fails

	kh, err := knownHosts()
	if err != nil {
		s.fail(err)
		return
	}
	p := sshclient.NewPrompter(s.out, s.in)
	client, err := sshclient.Dial(s.ctx, t, p, kh)
	if err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		_ = client.Close() //nolint:gosec // G104: best-effort cleanup of a cancelled session
		s.fail(sshclient.ErrCancelled)
		return
	}
	s.client = client
	cols, rows := s.cols, s.rows
	s.mu.Unlock()

	sess, err := sshclient.NewSession(client, cols, rows, terminalType)
	if err != nil {
		_ = client.Close() //nolint:gosec // G104: best-effort cleanup after the primary error
		s.fail(err)
		return
	}
	if !s.handover(p, sess, cols, rows) {
		_ = sess.Close()   //nolint:gosec // G104: best-effort cleanup of a cancelled session
		_ = client.Close() //nolint:gosec // G104: best-effort cleanup of a cancelled session
		s.fail(sshclient.ErrCancelled)
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.keepalive(client)
	}()
	for b := range sess.Output() {
		_, _ = s.out.Write(b) //nolint:gosec // G104: outStream never fails
	}
	code := exitCode(sess.Wait())
	s.cancel()
	_ = client.Close() //nolint:gosec // G104: the session is over, close errors are irrelevant
	wg.Wait()
	s.finish(SessionStateDTO{State: stateClosed, ExitCode: code, Retryable: true})
}

// handover switches input from the Prompter to the shell: keystrokes the Prompter buffered past
// the last answer go first, then those still queued, then live SessionWrite calls. The writes
// happen outside s.mu, behind wmu, so close() never waits on a stuck server.
func (s *session) handover(p *sshclient.Prompter, sess *sshclient.Session, cols, rows int) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	pending := append(p.Pending(), s.in.Close()...)
	resize := s.cols != cols || s.rows != rows
	cols, rows = s.cols, s.rows
	s.sess = sess
	s.state = stateConnected
	s.wmu.Lock() // taken before s.mu is released: later writes queue behind the pending bytes
	s.mu.Unlock()

	if len(pending) > 0 {
		_ = sess.Write(pending) //nolint:gosec // G104: a dead channel ends the session through its output
	}
	s.wmu.Unlock()
	if resize {
		_ = sess.Resize(cols, rows) //nolint:gosec // G104: a failed resize only leaves the old size
	}
	s.out.Event(s.stateEvent(), SessionStateDTO{State: stateConnected, Retryable: true}, false)
	return true
}

// keepalive measures the round trip of keepalive@openssh.com right away and every latencyInterval.
func (s *session) keepalive(c *ssh.Client) {
	t := time.NewTicker(latencyInterval)
	defer t.Stop()
	for {
		start := time.Now()
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			return
		}
		ms := max(int(time.Since(start).Milliseconds()), 1) // 0 means "not measured yet"
		s.mu.Lock()
		s.latencyMs = ms
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// fail ends a session that never reached the shell; after close() the error is a cancellation.
func (s *session) fail(err error) {
	s.in.Close()
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing && !errors.Is(err, sshclient.ErrCancelled) {
		err = fmt.Errorf("%w: %w", sshclient.ErrCancelled, err)
	}
	s.finish(errorState(err))
}

func (s *session) finish(st SessionStateDTO) {
	s.mu.Lock()
	s.state = st.State
	s.sess, s.client = nil, nil
	s.mu.Unlock()
	s.out.Event(s.stateEvent(), st, true)
}

// write sends input to the Prompter while connecting and to the shell afterwards.
// Input to a finished session is dropped.
func (s *session) write(b []byte) error {
	s.mu.Lock()
	sess := s.sess
	if sess == nil && s.state == stateConnecting {
		s.in.Write(b)
	}
	s.mu.Unlock()
	if sess == nil {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return sess.Write(b)
}

// resize records the view size and forwards it to the shell. The first call attaches the view.
func (s *session) resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	sess := s.sess
	s.mu.Unlock()
	s.out.Attach()
	if sess != nil {
		return sess.Resize(cols, rows)
	}
	return nil
}

// close aborts dialling (a blocked prompt returns ErrCancelled) or ends the shell. It does not wait.
func (s *session) close() {
	s.mu.Lock()
	s.closing = true
	sess, client := s.sess, s.client
	s.mu.Unlock()
	s.cancel()
	s.in.Close()
	if sess != nil {
		_ = sess.Close() //nolint:gosec // G104: closing the client below ends the session anyway
	}
	if client != nil {
		_ = client.Close() //nolint:gosec // G104: best-effort, the session is being discarded
	}
}

func (s *session) dto() SessionDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionDTO{ID: s.id, HostID: s.hostID, HostName: s.hostName, State: s.state, LatencyMs: s.latencyMs}
}

func exitCode(err error) *int {
	code := 0
	var ee *ssh.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitStatus()
	default:
		return nil
	}
	return &code
}

// errorState turns a dial or session error into the error card payload.
func errorState(err error) SessionStateDTO {
	st := SessionStateDTO{State: stateError, Message: err.Error(), Detail: sshclient.ErrorDetail(err), Retryable: true}
	switch {
	case errors.Is(err, sshclient.ErrHostNotTrusted):
		st.Kind = "cancelled"
		st.Message = "Подключение отменено: сервер не подтверждён"
	case errors.Is(err, sshclient.ErrCancelled) || errors.Is(err, context.Canceled):
		st.Kind = "cancelled"
		st.Message = "Подключение отменено"
	case errors.Is(err, sshclient.ErrHostKeyChanged):
		st.Kind, st.Retryable = "hostkey", false
	case errors.Is(err, sshclient.ErrTimeout):
		st.Kind = "timeout"
	case errors.Is(err, sshclient.ErrAuthDenied):
		st.Kind = "auth"
	default:
		st.Kind = "other"
	}
	return st
}

// knownHosts opens the app's known_hosts on first use; one instance serialises its writes.
func (a *App) knownHosts() (*sshclient.KnownHosts, error) {
	a.khMu.Lock()
	defer a.khMu.Unlock()
	if a.kh != nil {
		return a.kh, nil
	}
	dir, err := appdir.Dir()
	if err != nil {
		return nil, err
	}
	kh, err := sshclient.OpenKnownHosts(filepath.Join(dir, "known_hosts"))
	if err != nil {
		return nil, err
	}
	a.kh = kh
	return kh, nil
}

func (a *App) session(id string) (*session, error) {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, errSessionNotFound
	}
	return s, nil
}

func (a *App) addSession(s *session) {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]*session{}
	}
	a.sessSeq++
	s.seq = a.sessSeq
	a.sessions[s.id] = s
}

func (a *App) removeSession(id string) (*session, error) {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, errSessionNotFound
	}
	delete(a.sessions, id)
	return s, nil
}

// closeAllSessions closes every session and waits until their goroutines have ended.
func (a *App) closeAllSessions() {
	a.sessMu.Lock()
	all := a.sessions
	a.sessions = nil
	a.sessMu.Unlock()
	for _, s := range all {
		s.close()
	}
	for _, s := range all {
		<-s.done
	}
}

// closeSessionsOfHost closes the sessions of one host and waits until their goroutines have ended.
func (a *App) closeSessionsOfHost(hostID string) {
	a.sessMu.Lock()
	var all []*session
	for id, s := range a.sessions {
		if s.hostID == hostID {
			all = append(all, s)
			delete(a.sessions, id)
		}
	}
	a.sessMu.Unlock()
	for _, s := range all {
		s.close()
	}
	for _, s := range all {
		<-s.done
	}
}
