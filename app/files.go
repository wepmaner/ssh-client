package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/sshclient"
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errFilesTabNotFound = errors.New("Вкладка файлов не найдена")
	errFilesClosed      = errors.New("Вкладка файлов закрыта")
	errFilesHostDeleted = errors.New("Хост удалён")
)

// filesTab is one files tab: a user of its host's shared connection, like a console. Its login
// questions go to its own stream files:login:<id>, held until FilesLoginAttach, and so do the login
// questions of its transfers. It keeps one sftp session on the connection's live client and opens
// a new one after a reconnect; each running transfer is a user of its own (*filesJob) with its own
// sftp session.
type filesTab struct {
	a                    *App
	id, hostID, hostName string

	out    *outStream // files:login:<id>; files:state:<id> goes through it too
	ctx    context.Context
	cancel context.CancelFunc // ends the tab: its calls, logins and transfers
	kick   chan struct{}      // wakes the job runner
	// runnerDone is closed when the job runner has returned: no transfer runs any more.
	runnerDone chan struct{}
	done       chan struct{} // closed when the tab has ended: transfers cleaned up, connection left

	mu      sync.Mutex
	state   FilesStateDTO
	in      *inputQueue // the input of the login running for the tab, nil before the first
	sc      *sftp.Client
	scClose func()
	scSSH   *ssh.Client // the client sc runs on
	// watched is the last client the tab got: its watcher reconnects the tab when it ends.
	watched *ssh.Client
	// reconnecting is set while the background reconnect runs; again asks it for one more round
	// (the client it got ended before it returned).
	reconnecting, again bool

	jobs    []*filesJob // in queue order
	closing bool
	ended   bool // left the connection for good (closed or host deleted)
}

func (t *filesTab) stateEvent() string { return "files:state:" + t.id }

// asker answers the logins of the tab and of its transfers in the tab's stream.
func (t *filesTab) asker() *connAsker {
	return &connAsker{
		out:   t.out,
		trust: trustOwner{sessionID: t.id},
		label: t.hostName + " · файлы",
		newInput: func() *inputQueue {
			q := newInputQueue()
			t.mu.Lock()
			t.in = q
			t.mu.Unlock()
			return q
		},
	}
}

// setState records and emits the tab's state. Until the login view attaches, the latest state is
// held in the stream and also emitted at once, so a view that never shows the login still sees it.
func (t *filesTab) setState(st FilesStateDTO) {
	t.mu.Lock()
	if t.ended && st.State != stateClosed && st.State != stateError {
		t.mu.Unlock()
		return
	}
	t.state = st
	t.mu.Unlock()
	if !t.out.isAttached() {
		t.a.events().Emit(t.stateEvent(), st)
	}
	t.out.Event(t.stateEvent(), st, st.State == stateClosed)
}

func (t *filesTab) currentState() FilesStateDTO {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// onLoginOutput is the stream's onWrite: anything written there is a login (a question, the wait
// for another terminal), so the tab shows its login card.
func (t *filesTab) onLoginOutput() {
	t.mu.Lock()
	show := !t.ended && !t.closing && t.state.State != stateConnecting
	t.mu.Unlock()
	if show {
		t.setState(FilesStateDTO{State: stateConnecting, Retryable: true})
	}
}

func filesErrorState(err error) FilesStateDTO {
	st := errorState(err)
	return FilesStateDTO{State: stateError, Message: st.Message, Detail: st.Detail, Retryable: st.Retryable}
}

// acquire returns the live client of the connection u is on, logging in through the tab's stream
// when there is none. The tab shows connected after a login; a login that failed while the tab
// showed it (connecting) shows the error.
func (t *filesTab) acquire(ctx context.Context, u any) (*ssh.Client, error) {
	c := t.a.connOf(u)
	if c == nil {
		return nil, errFilesClosed
	}
	cl, err := c.acquire(ctx, u, t.asker())
	switch {
	case err == nil:
		if t.currentState().State != stateConnected {
			t.setState(FilesStateDTO{State: stateConnected, Retryable: true})
		}
	case ctx.Err() == nil && t.currentState().State == stateConnecting && !t.isReconnecting():
		// The background reconnect shows only the errors that will not heal by themselves.
		t.setState(filesErrorState(err))
	}
	return cl, err
}

func (t *filesTab) isReconnecting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reconnecting
}

// sftpClient returns the tab's sftp session on the connection's live client, opening one after a
// reconnect. A live client already in use costs no wait.
func (t *filesTab) sftpClient(ctx context.Context) (*sftp.Client, error) {
	c := t.a.connOf(t)
	if c == nil {
		return nil, errFilesClosed
	}
	cl, _, err := c.clientFor(t)
	if err != nil {
		return nil, errFilesClosed
	}
	t.mu.Lock()
	if cl != nil && cl == t.scSSH && t.sc != nil {
		sc := t.sc
		t.mu.Unlock()
		return sc, nil
	}
	t.mu.Unlock()
	if cl == nil {
		if cl, err = t.acquire(ctx, t); err != nil {
			return nil, err
		}
	}
	sc, closeSC, err := sshclient.OpenSFTP(ctx, cl)
	if err != nil && clientEndedWithin(cl, deadClientGrace) {
		// The connection ended before the watcher noticed: acquire waits for it and logs in anew.
		if cl, err = t.acquire(ctx, t); err != nil {
			return nil, err
		}
		sc, closeSC, err = sshclient.OpenSFTP(ctx, cl)
	}
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		closeSC()
		return nil, errFilesClosed
	}
	old := t.scClose
	t.sc, t.scClose, t.scSSH = sc, closeSC, cl
	if cl != t.watched {
		t.watched = cl
		go t.watch(cl)
	}
	t.mu.Unlock()
	if old != nil {
		old()
	}
	return sc, nil
}

// dropSFTP forgets the tab's sftp session if it is still sc.
func (t *filesTab) dropSFTP(sc *sftp.Client) {
	t.mu.Lock()
	var cl func()
	if t.sc == sc {
		cl = t.scClose
		t.sc, t.scClose, t.scSSH = nil, nil, nil
	}
	t.mu.Unlock()
	if cl != nil {
		cl()
	}
}

// withSFTP runs op on the tab's sftp session; a session lost under op is opened anew once.
func (t *filesTab) withSFTP(op func(sc *sftp.Client) error) error {
	for try := 0; ; try++ {
		sc, err := t.sftpClient(t.ctx)
		if err != nil {
			return err
		}
		err = op(sc)
		if err == nil || try > 0 || !sshclient.IsLost(err) {
			return err
		}
		t.dropSFTP(sc)
	}
}

// watch waits for the end of cl, a client the tab got. When cl is still the tab's last client and
// the tab is open, the tab shows connecting (with no login output: a banner over the listing) and
// reconnects in the background.
func (t *filesTab) watch(cl *ssh.Client) {
	_ = cl.Wait() //nolint:gosec // G104: only the end of the connection matters
	t.mu.Lock()
	if t.watched != cl || t.closing || t.ended {
		t.mu.Unlock()
		return
	}
	if t.reconnecting {
		t.again = true // the running reconnect got cl and goes one more round
		t.mu.Unlock()
		return
	}
	t.reconnecting = true
	t.mu.Unlock()
	t.reconnect()
}

// filesTabFatal reports a reconnect error that waiting will not cure.
func filesTabFatal(err error) bool {
	return connFatal(err) || errors.Is(err, errConnStale)
}

// reconnect opens the tab's sftp session anew after its client ended, with pauses by the backoff
// between attempts, until it succeeds, the tab ends or an error that will not heal. The secrets stay
// while files users are on the connection, so this is silent unless the server asks again (then the
// login stream shows the card). A user action meanwhile logs in through the same hostConn.acquire,
// which shares one login between all its callers.
func (t *filesTab) reconnect() {
	backoff := t.a.filesBackoff
	if backoff == nil {
		backoff = filesDefaultBackoff
	}
	stop := func() {
		t.mu.Lock()
		t.reconnecting, t.again = false, false
		t.mu.Unlock()
	}
	for attempt := 0; ; attempt++ {
		if attempt == 0 {
			t.forgetEndedSFTP()
			t.setState(FilesStateDTO{State: stateConnecting, Retryable: true})
		} else {
			tm := time.NewTimer(backoff(attempt))
			select {
			case <-t.ctx.Done():
				tm.Stop()
				stop()
				return
			case <-tm.C:
			}
		}
		_, err := t.sftpClient(t.ctx)
		if t.ctx.Err() != nil {
			stop()
			return
		}
		if err != nil {
			if filesTabFatal(err) {
				stop()
				t.setState(filesErrorState(err))
				return
			}
			continue
		}
		if t.currentState().State != stateConnected {
			t.setState(FilesStateDTO{State: stateConnected, Retryable: true})
		}
		t.mu.Lock()
		again := t.again
		t.again = false
		if !again {
			t.reconnecting = false
		}
		t.mu.Unlock()
		if !again {
			return
		}
		attempt = -1 // the client it got has ended too: start over
	}
}

// forgetEndedSFTP drops the tab's sftp session when its client has ended, so the next sftpClient
// does not hand it out before the connection's own watcher has noticed the end.
func (t *filesTab) forgetEndedSFTP() {
	t.mu.Lock()
	var cl func()
	if t.scSSH != nil && t.scSSH == t.watched {
		cl = t.scClose
		t.sc, t.scClose, t.scSSH = nil, nil, nil
	}
	t.mu.Unlock()
	if cl != nil {
		cl()
	}
}

// connect is the tab's first login, and the one of FilesRetry.
func (t *filesTab) connect() {
	t.setState(FilesStateDTO{State: stateConnecting, Retryable: true})
	if c := t.a.connOf(t); c != nil {
		p := c.params
		_, _ = fmt.Fprintf(t.out, "Подключение к %s@%s:%d …\r\n", p.User, p.Address, p.Port) //nolint:gosec // G104: outStream never fails
	}
	if _, err := t.sftpClient(t.ctx); err != nil {
		if t.ctx.Err() == nil {
			t.setState(filesErrorState(err))
		}
		return
	}
	if t.currentState().State != stateConnected {
		t.setState(FilesStateDTO{State: stateConnected, Retryable: true})
	}
}

// end stops the tab: its transfers are cancelled and clean up their part files, then the tab
// leaves the connection. final is its last state (closed, or the error of a deleted host).
func (t *filesTab) end(final FilesStateDTO) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		<-t.done
		return
	}
	t.closing = true
	jobs := append([]*filesJob(nil), t.jobs...)
	in := t.in
	t.mu.Unlock()
	t.cancel()
	if in != nil {
		clear(in.Close())
	}
	for _, j := range jobs {
		j.cancelRun()
	}
	defer close(t.done)
	<-t.runnerDone
	for _, j := range jobs {
		j.removeParts() // the parts kept by failed and cancelled jobs
	}
	t.mu.Lock()
	t.ended = true
	cl := t.scClose
	t.sc, t.scClose, t.scSSH = nil, nil, nil
	t.mu.Unlock()
	if cl != nil {
		cl()
	}
	t.a.mcpLeave(t)
	t.setState(final)
}

// filesTab returns the open tab id.
func (a *App) filesTab(id string) (*filesTab, error) {
	a.filesMu.Lock()
	defer a.filesMu.Unlock()
	t := a.files[id]
	if t == nil {
		return nil, errFilesTabNotFound
	}
	return t, nil
}

// closeAllFiles closes every files tab and waits until each has cleaned up (app exit, restore).
func (a *App) closeAllFiles() {
	a.filesMu.Lock()
	all := a.files
	a.files = nil
	a.filesMu.Unlock()
	var wg sync.WaitGroup
	for _, t := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.end(FilesStateDTO{State: stateClosed})
		}()
	}
	wg.Wait()
}

// filesHostDeleted ends the tabs of a deleted host with an error they cannot retry; the tabs stay
// until the view closes them.
func (a *App) filesHostDeleted(hostID string) {
	a.filesMu.Lock()
	var tabs []*filesTab
	for _, t := range a.files {
		if t.hostID == hostID {
			tabs = append(tabs, t)
		}
	}
	a.filesMu.Unlock()
	var wg sync.WaitGroup
	for _, t := range tabs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.end(FilesStateDTO{State: stateError, Message: errFilesHostDeleted.Error()})
		}()
	}
	wg.Wait()
}
