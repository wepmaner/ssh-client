package app

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const (
	// mcpHoldTime is how long a hold keeps the host's connection after the last call.
	mcpHoldTime = 30 * time.Minute
	// mcpLoginTime is how long one call waits for a login before it returns errMCPLoginPending.
	mcpLoginTime = 2 * time.Minute

	// mcpAskerLabel counts the login's asker among the waiters a login is handed over to.
	mcpAskerLabel = "MCP"
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errMCPLoginPending   = errors.New("Ожидается вход в окне клиента, повторите команду")
	errMCPLoginCancelled = errors.New("Вход отменён в окне клиента")
)

// MCPLoginOpenDTO is the payload of mcplogin:open: the login needs the owner's input.
type MCPLoginOpenDTO struct {
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
}

// MCPLoginCloseDTO is the payload of mcplogin:close: the login has ended.
type MCPLoginCloseDTO struct {
	HostID string `json:"host_id"`
}

// mcpHold is the MCP's lasting user of one host's connection, so a passphrase is not asked for each
// command. It rejoins the host's current connection on every call, which moves it off a stale one,
// and leaves mcpHoldTime after the last call. It never expires under a running call or login.
// Its fields are guarded by App.mcpMu.
type mcpHold struct {
	hostID string
	timer  *time.Timer
	armed  int                   // which timer is the current one
	calls  map[*mcpCall]struct{} // the calls in progress
	login  *mcpLogin             // the login in progress, nil if none
	gone   bool                  // left the host: it never rejoins
}

// mcpCall is a user of the host's current connection for one tool call: a call on a connection that
// went stale keeps that connection until it ends. Everything it runs uses ctx, which ends when the
// host is released.
type mcpCall struct {
	hostID string
	hold   *mcpHold
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

// mcpLogin is the hold's one login: it runs in its own context, so a call's limit or cancel does not
// end it. The owner answers it in the login dialog, whose stream is new for each login: the dialog
// subscribes after mcplogin:open, and the stream holds the output until MCPLoginAttach.
type mcpLogin struct {
	conn   *hostConn
	out    *outStream
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed once err is set

	// Guarded by App.mcpMu.
	in         *inputQueue // the input of the current attempt, nil before it
	opened     bool        // the dialog is open
	ended      bool        // the dialog must not open any more
	cancelled  bool        // the owner closed the dialog
	superseded bool        // replaced by a login on the host's current connection

	err error
}

// mcpBegin returns a live client of the host's current connection for one tool call, and the call,
// which the caller ends with mcpEnd. Without a client it waits for the host's login at most
// mcpLoginTime; the error errMCPLoginPending leaves that login running for the next call. No lock
// is held while waiting.
func (a *App) mcpBegin(ctx context.Context, hostID string) (*ssh.Client, *mcpCall, error) {
	if _, err := a.hostParamsOf(hostID); err != nil {
		return nil, nil, err // before the hold: a refused call leaves nothing behind
	}
	callCtx, cancel := context.WithCancel(ctx)
	call := &mcpCall{hostID: hostID, ctx: callCtx, cancel: cancel}
	a.mcpEnter(call)
	client, err := a.mcpClient(call)
	if err != nil {
		a.mcpEnd(call)
		return nil, nil, err
	}
	return client, call, nil
}

// mcpEnd ends the call: it leaves its connection, and the hold's time starts anew.
func (a *App) mcpEnd(c *mcpCall) {
	c.once.Do(func() {
		c.cancel()
		a.mcpLeave(c)
		a.mcpMu.Lock()
		defer a.mcpMu.Unlock()
		delete(c.hold.calls, c)
		if !c.hold.gone {
			a.mcpArmLocked(c.hold)
		}
	})
}

// mcpReleaseHost cancels the host's calls and login and takes its hold off the connection; the
// connection closes when nobody else uses it, after the last running call has ended. The caller
// holds no lock, as for closeHostConns.
func (a *App) mcpReleaseHost(hostID string) {
	a.mcpMu.Lock()
	h := a.mcpHolds[hostID]
	if h == nil {
		a.mcpMu.Unlock()
		return
	}
	delete(a.mcpHolds, hostID)
	h.gone = true
	h.timer.Stop()
	for c := range h.calls {
		c.cancel()
	}
	if l := h.login; l != nil {
		l.cancel()
		a.mcpCloseDialogLocked(l) // now: a next hold's dialog of this host must not be closed by it
	}
	a.mcpMu.Unlock()
	a.mcpLeave(h)
}

// mcpReleaseAll is mcpReleaseHost for every host.
func (a *App) mcpReleaseAll() {
	a.mcpMu.Lock()
	var ids []string
	for id := range a.mcpHolds {
		ids = append(ids, id)
	}
	a.mcpMu.Unlock()
	for _, id := range ids {
		a.mcpReleaseHost(id)
	}
}

// MCPLoginWrite sends base64-encoded keystrokes from the login dialog to the question of the host's
// login. Input with no login waiting is dropped.
func (a *App) MCPLoginWrite(hostID, dataB64 string) error {
	b, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return errBadInput
	}
	defer clear(b) // the keystrokes may be a passphrase or a password; the input queue keeps its own copy
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if l := a.mcpLoginLocked(hostID); l != nil && l.in != nil {
		l.in.Write(b)
	}
	return nil
}

// MCPLoginCancel ends the host's login: the owner closed its dialog. Its calls get an error; a
// console or forwards waiting for the same login take it over.
func (a *App) MCPLoginCancel(hostID string) error {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if l := a.mcpLoginLocked(hostID); l != nil {
		l.cancelled = true
		l.cancel()
	}
	return nil
}

// MCPLoginAttach tells Go the dialog listens to mcplogin:data:<hostID>: the output held since
// mcplogin:open is released. A dialog late for its login does nothing.
func (a *App) MCPLoginAttach(hostID string) error {
	a.mcpMu.Lock()
	l := a.mcpLoginLocked(hostID)
	a.mcpMu.Unlock()
	if l != nil {
		l.out.Attach()
	}
	return nil
}

// mcpLoginLocked is the host's login in progress, nil if none; the caller holds mcpMu.
func (a *App) mcpLoginLocked(hostID string) *mcpLogin {
	if h := a.mcpHolds[hostID]; h != nil {
		return h.login
	}
	return nil
}

// mcpEnter adds the call to the host's hold, creating it, and starts the hold's time anew.
func (a *App) mcpEnter(c *mcpCall) {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	h := a.mcpHolds[c.hostID]
	if h == nil {
		h = &mcpHold{hostID: c.hostID, calls: map[*mcpCall]struct{}{}}
		if a.mcpHolds == nil {
			a.mcpHolds = map[string]*mcpHold{}
		}
		a.mcpHolds[c.hostID] = h
	}
	h.calls[c] = struct{}{}
	c.hold = h
	a.mcpArmLocked(h)
}

// mcpArmLocked starts the hold's time anew; the caller holds mcpMu.
func (a *App) mcpArmLocked(h *mcpHold) {
	if h.timer != nil {
		h.timer.Stop()
	}
	ttl := a.mcpHoldTTL
	if ttl <= 0 {
		ttl = mcpHoldTime
	}
	h.armed++
	armed := h.armed
	h.timer = time.AfterFunc(ttl, func() { a.mcpExpire(h, armed) })
}

// mcpExpire ends the hold whose time ran out. A running call or login rearms it when it ends.
func (a *App) mcpExpire(h *mcpHold, armed int) {
	a.mcpMu.Lock()
	if h.gone || h.armed != armed || len(h.calls) > 0 || h.login != nil {
		a.mcpMu.Unlock()
		return
	}
	h.gone = true
	delete(a.mcpHolds, h.hostID)
	a.mcpMu.Unlock()
	a.mcpLeave(h)
}

// mcpClient returns the live client of the host's current connection for the call, waiting for the
// hold's login while there is none. A login that ends well is checked for life on the next round, and
// one replaced because the host's parameters changed sends the call to the new connection.
func (a *App) mcpClient(call *mcpCall) (*ssh.Client, error) {
	limit := a.mcpLoginLimit
	if limit <= 0 {
		limit = mcpLoginTime
	}
	timeout := time.NewTimer(limit)
	defer timeout.Stop()
	h := call.hold
	for {
		if err := call.ctx.Err(); err != nil {
			return nil, err
		}
		held, err := a.mcpJoinHold(h)
		if err != nil {
			return nil, err
		}
		c, err := a.joinHostConn(call.hostID, call)
		if err != nil {
			return nil, err
		}
		if c != held {
			continue // the host's parameters changed between the two steps
		}
		if client := c.clientFor(call); client != nil {
			a.mcpMu.Lock()
			a.mcpSupersedeLocked(h, c)
			a.mcpMu.Unlock()
			return client, nil
		}
		l := a.mcpLoginOn(h, c)
		if l == nil {
			return nil, sshclient.ErrCancelled
		}
		select {
		case <-l.done:
			if l.err != nil && !l.superseded {
				return nil, l.err
			}
		case <-call.ctx.Done():
			return nil, call.ctx.Err()
		case <-timeout.C:
			return nil, errMCPLoginPending
		}
	}
}

// mcpJoinHold puts the hold on the host's current connection. A release that ran meanwhile did not
// see this join, so it is undone here.
func (a *App) mcpJoinHold(h *mcpHold) (*hostConn, error) {
	c, err := a.joinHostConn(h.hostID, h)
	if err != nil {
		return nil, err
	}
	a.mcpMu.Lock()
	gone := h.gone
	a.mcpMu.Unlock()
	if gone {
		a.mcpLeave(h)
		return nil, sshclient.ErrCancelled
	}
	return c, nil
}

// mcpLeave takes u off the connection it is on; a u that is on none (host deleted, app exit) has
// nothing to leave.
func (a *App) mcpLeave(u any) {
	a.hcMu.Lock()
	var drop *ssh.Client
	if c := a.hcUsers[u]; c != nil {
		drop = c.leaveLocked(u)
	}
	a.hcMu.Unlock()
	closeClient(drop)
}

// clientFor returns the connection's live client for u, nil if none. It never starts a login: only
// the hold does, so a call's limit or cancel cannot end one.
func (c *hostConn) clientFor(u any) *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	client := c.liveClientLocked()
	if _, member := c.users[u]; member && client != nil {
		c.users[u] = client
	}
	return client
}

// mcpLoginOn returns the hold's login on c, starting it. It returns nil for a hold that is gone.
func (a *App) mcpLoginOn(h *mcpHold, c *hostConn) *mcpLogin {
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if h.gone {
		return nil
	}
	a.mcpSupersedeLocked(h, c)
	if h.login != nil {
		return h.login
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &mcpLogin{conn: c, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	l.out = newOutStreamFor(a.events(), "mcplogin:data:"+h.hostID)
	l.out.onOutput = func() { a.mcpOpenDialog(h.hostID, l) }
	h.login = l
	go a.mcpRunLogin(h, l)
	return l
}

// mcpSupersedeLocked ends a login on another connection than c, the host's current one: it asks for
// the host's old parameters. Its calls go on to c. The caller holds mcpMu.
func (a *App) mcpSupersedeLocked(h *mcpHold, c *hostConn) {
	if l := h.login; l != nil && l.conn != c {
		h.login = nil
		l.superseded = true
		l.cancel()
		a.mcpCloseDialogLocked(l)
	}
}

// mcpRunLogin logs the hold in. acquire waits for a login a console or forwards already answer: the
// asker's wait text stays out of the stream, and the dialog does not open for it.
func (a *App) mcpRunLogin(h *mcpHold, l *mcpLogin) {
	ask := &connAsker{
		out:   l.out,
		trust: trustOwner{mcpHostID: h.hostID},
		label: mcpAskerLabel,
		mcp:   true,
		newInput: func() *inputQueue {
			q := newInputQueue()
			a.mcpMu.Lock()
			l.in = q
			a.mcpMu.Unlock()
			return q
		},
	}
	_, err := l.conn.acquire(l.ctx, h, ask)
	a.mcpFinishLogin(h, l, err)
}

// mcpFinishLogin publishes the login's result. After a failure the hold leaves before its waiters
// wake: a call that then starts a new login rejoins, and this leave cannot undo that.
func (a *App) mcpFinishLogin(h *mcpHold, l *mcpLogin, err error) {
	if err != nil {
		a.mcpMu.Lock()
		current := h.login == l
		a.mcpMu.Unlock()
		if current {
			a.mcpLeave(h)
		}
	}
	l.cancel()
	a.mcpMu.Lock()
	defer a.mcpMu.Unlock()
	if err != nil && l.cancelled {
		err = errMCPLoginCancelled
	}
	l.err = err
	if h.login == l {
		h.login = nil
		if !h.gone {
			a.mcpArmLocked(h)
		}
	}
	a.mcpCloseDialogLocked(l)
	close(l.done)
}

// mcpOpenDialog announces the login's dialog and raises the window: the login has written something
// for the owner. A login that has ended stays closed: its attempt can outlive it for a console.
func (a *App) mcpOpenDialog(hostID string, l *mcpLogin) {
	name := a.mcpHostName(hostID)
	a.mcpMu.Lock()
	if l.ended {
		a.mcpMu.Unlock()
		return
	}
	l.opened = true
	a.events().Emit("mcplogin:open", MCPLoginOpenDTO{HostID: hostID, HostName: name})
	a.mcpMu.Unlock()
	if a.mainWin != nil {
		raiseWindow(a.mainWin)
	}
}

// mcpCloseDialogLocked closes the login's dialog if it is open and keeps it from opening. It emits
// under mcpMu, as mcpOpenDialog does, so the events of a host stay in order. The caller holds mcpMu.
func (a *App) mcpCloseDialogLocked(l *mcpLogin) {
	l.ended = true
	if l.opened {
		l.opened = false
		a.events().Emit("mcplogin:close", MCPLoginCloseDTO{HostID: l.conn.hostID})
	}
}

func (a *App) mcpHostName(hostID string) string {
	if !a.storeUnlocked() {
		return ""
	}
	hosts := a.storeRef().Hosts()
	if i := slices.IndexFunc(hosts, func(h model.Host) bool { return h.ID == hostID }); i >= 0 {
		return hosts[i].Name
	}
	return ""
}
