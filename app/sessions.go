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

// errConnLost is the error card of a console whose shared connection dropped under its shell.
var errConnLost = errors.New("Соединение с сервером прервано") //nolint:staticcheck // ST1005: Russian UI text starts with a capital letter

// session is one terminal tab on its host's shared connection: the login with prompts in the stream
// (or a wait for the login others answer), then an interactive shell on a channel of its own. The
// session never closes the shared client, only its channel; it leaves the connection when run ends.
type session struct {
	id, hostID, hostName string
	seq                  int // opening order for SessionList

	conn      *hostConn // the connection joined in SessionOpen
	leaveOnce sync.Once

	out    *outStream
	in     *inputQueue // keystrokes for the Prompter until the shell starts
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when run has returned and every goroutine of the session ended

	wmu sync.Mutex // orders writes to the shell; never held together with mu waiting on the network

	mu         sync.Mutex
	state      string
	sess       *sshclient.Session
	cols, rows int
	latencyMs  int
	closing    bool
	elsewhere  bool // waiting for a login another terminal answers: typed keys are dropped
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

// run acquires the host connection's client, starts the shell on a channel of its own and pumps
// its output until the shell ends or close() is called. It never waits on the network once closed.
func (s *session) run(p hostParams) {
	defer close(s.done)
	defer s.leave()
	defer s.cancel()
	s.out.Event(s.stateEvent(), SessionStateDTO{State: stateConnecting, Retryable: true}, false)
	_, _ = fmt.Fprintf(s.out, "Подключение к %s@%s:%d …\r\n", p.User, p.Address, p.Port) //nolint:gosec // G104: outStream never fails

	pr := sshclient.NewPrompter(s.out, s.in)
	ask := &connAsker{out: s.out, in: s.in, trust: trustOwner{sessionID: s.id}, label: s.hostName, prompter: pr, onWait: s.waitElsewhere}
	client, err := s.conn.acquire(s.ctx, s, ask)
	if err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	closing := s.closing
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	if closing {
		s.fail(sshclient.ErrCancelled)
		return
	}

	sess, err := sshclient.NewSession(client, cols, rows, terminalType)
	if err != nil {
		s.fail(err)
		return
	}
	// Closing the channel sends a message: on a stuck connection it must not hold up run.
	defer func() { go func() { _ = sess.Close() }() }() //nolint:gosec // G104: the channel is being discarded
	if !s.handover(pr, sess, cols, rows) {
		s.fail(sshclient.ErrCancelled)
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.keepalive(client)
	}()
	closed := s.pump(sess)
	var waitErr error
	if !closed {
		waited := make(chan error, 1)
		go func() { waited <- sess.Wait() }()
		select {
		case waitErr = <-waited:
		case <-s.ctx.Done():
			closed = true
		}
	}
	s.cancel()
	wg.Wait()
	code := exitCode(waitErr)
	switch {
	case closed:
		s.finish(SessionStateDTO{State: stateClosed, Retryable: true})
	case code == nil && s.clientEnded(client):
		st := errorState(fmt.Errorf("%w: %w", errConnLost, waitErr))
		st.Message = errConnLost.Error() // the system text stays in Detail
		s.finish(st)
	default:
		s.finish(SessionStateDTO{State: stateClosed, ExitCode: code, Retryable: true})
	}
}

// clientEnded reports, after a shell ended without an exit status, whether the shared client ended
// too (the connection dropped) within deadClientGrace: the channel closes before the client does.
func (s *session) clientEnded(c *ssh.Client) bool {
	ended := make(chan struct{})
	go func() { // a live client keeps this until it closes
		_ = c.Wait() //nolint:gosec // G104: only the end of the connection matters
		close(ended)
	}()
	t := time.NewTimer(deadClientGrace)
	defer t.Stop()
	select {
	case <-ended:
		return true
	case <-t.C:
		return false
	}
}

// waitElsewhere is the connAsker's onWait: while the session waits for a login that another
// terminal answers, keys typed into it are not for that login nor for the shell, and are dropped
// (a passphrase typed there by mistake never reaches the shell).
func (s *session) waitElsewhere(waiting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.elsewhere = waiting
	if waiting {
		s.in.Discard()
	}
}

// pump copies the shell's output to the stream until the shell ends (false) or close() is called (true).
func (s *session) pump(sess *sshclient.Session) bool {
	for {
		select {
		case b, ok := <-sess.Output():
			if !ok {
				return s.ctx.Err() != nil
			}
			_, _ = s.out.Write(b) //nolint:gosec // G104: outStream never fails
		case <-s.ctx.Done():
			return true
		}
	}
}

// leave takes the session off its connection once; the last user closes it and wipes its secrets.
func (s *session) leave() {
	s.leaveOnce.Do(func() {
		if s.conn != nil {
			s.conn.leave(s)
		}
	})
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
// It ends with the session without waiting for an unanswered request (the shared client's own
// keepalive deals with a dead connection).
func (s *session) keepalive(c *ssh.Client) {
	t := time.NewTicker(latencyInterval)
	defer t.Stop()
	for {
		start := time.Now()
		reply := make(chan error, 1)
		go func() {
			_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case <-s.ctx.Done():
			return
		case err := <-reply:
			if err != nil {
				return
			}
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
	s.sess = nil
	s.mu.Unlock()
	s.out.Event(s.stateEvent(), st, true)
}

// write sends input to the Prompter while connecting and to the shell afterwards.
// Input to a finished session is dropped.
func (s *session) write(b []byte) error {
	s.mu.Lock()
	sess := s.sess
	if sess == nil && s.state == stateConnecting && !s.elsewhere {
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

// close aborts the login or the wait for it (a blocked prompt returns ErrCancelled) or ends the
// shell; run then closes the session's channel. The shared client stays open. It does not wait.
func (s *session) close() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.cancel()
	s.in.Close()
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

// dialTarget dials t and then wipes the target's copy of the private key (from hostTarget):
// authentication is over once Dial returns, and a retry looks the key up again.
func dialTarget(ctx context.Context, t sshclient.Target, p *sshclient.Prompter, kh *sshclient.KnownHosts) (*ssh.Client, error) {
	if t.Key != nil {
		defer clear(t.Key.Private)
	}
	return sshclient.Dial(ctx, t, p, kh)
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
	case errors.Is(err, sshclient.ErrHandshakeCap):
		st.Kind = "other" // the card's timeout text is about the 10-second silence
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
