package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const (
	// keepaliveInterval is how often a host connection's client asks keepalive@openssh.com; a reply
	// that takes longer than one interval counts as a failure.
	keepaliveInterval = 15 * time.Second
	// keepaliveMaxFails consecutive failures close the client, so its users see the drop.
	keepaliveMaxFails = 3

	hostConnNone       = "none"
	hostConnConnecting = "connecting"
	hostConnConnected  = "connected"
	hostConnError      = "error"

	// deadClientGrace is how long acquire lets the watcher notice the end of a client that the
	// same user asks for again: a user asks again after it has seen its client's connection end.
	deadClientGrace = 200 * time.Millisecond
)

// HostConnStateDTO is the payload of fwdhost:state:<hostID>.
type HostConnStateDTO struct {
	Status    string `json:"status"` // none|connecting|connected|error
	Error     string `json:"error"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"` // false after ErrHostKeyChanged until restart
	Asker     string `json:"asker"`     // ""|"forwards"|"session": who answers the current login
	Session   string `json:"session"`   // the session's label when Asker is "session"
}

// hostParams are the host fields a connection logs in with; another value needs another connection.
type hostParams struct {
	Address string
	Port    int
	User    string
	KeyID   string
}

// hostConn is the one SSH connection of a host, shared by its forwards and terminal sessions: its
// login, the secrets that login was answered with and its reconnect while forwards use it.
//
// The secrets live in memory only (never in the store, a DTO, an event or a log) and are wiped when
// the connection closes (last user gone, app exit), on a final error, after a drop with no forwards
// left and when the host's login parameters change (markStale). Lock order: App.hcMu, then mu;
// neither is held while dialling or waiting on the network.
type hostConn struct {
	a        *App
	hostID   string
	params   hostParams
	emit     func(HostConnStateDTO)
	askFwd   func() *connAsker // who answers a reconnect: the host's forwards group
	interval time.Duration     // keepalive interval
	backoff  func(attempt int) time.Duration

	wg sync.WaitGroup // attempts, client watchers and keepalives

	mu         sync.Mutex
	users      map[any]*ssh.Client // *fwdRun and *session values, with the client each got last
	client     *ssh.Client
	dead       chan struct{} // closed when client.Wait has returned
	att        *loginAttempt // the login in progress, nil if none
	retries    int           // reconnect attempts since the last drop
	blocked    error         // ErrHostKeyChanged: every acquire fails at once until restart
	stale      bool          // detached by markStale
	shutdown   bool          // closeAllHostConns: no more logins
	st         HostConnStateDTO
	passphrase []byte
	passKeyID  string // the key the passphrase opens
	password   []byte
}

var errConnStale = errors.New("Параметры входа хоста изменились") //nolint:staticcheck // ST1005: Russian UI text starts with a capital letter

func paramsOf(h model.Host) hostParams {
	return hostParams{Address: h.Address, Port: h.Port, User: h.User, KeyID: h.KeyID}
}

// hostParamsOf returns the login parameters the host has in the store now.
func (a *App) hostParamsOf(hostID string) (hostParams, error) {
	if !a.storeUnlocked() {
		return hostParams{}, errVaultLocked
	}
	hosts := a.storeRef().Hosts()
	i := slices.IndexFunc(hosts, func(h model.Host) bool { return h.ID == hostID })
	if i < 0 {
		return hostParams{}, errHostNotFound
	}
	return paramsOf(hosts[i]), nil
}

// hostConnFor returns the host's current connection, creating it on demand. A connection whose
// login parameters no longer match the host is detached (markStale) and replaced.
func (a *App) hostConnFor(hostID string) (*hostConn, error) {
	p, err := a.hostParamsOf(hostID)
	if err != nil {
		return nil, err
	}
	a.hcMu.Lock()
	defer a.hcMu.Unlock()
	return a.currentConnLocked(hostID, p), nil
}

// joinHostConn adds u to the host's current connection and returns it, in one step: a connection
// with users is always the host's current one until it goes stale.
func (a *App) joinHostConn(hostID string, u any) (*hostConn, error) {
	p, err := a.hostParamsOf(hostID)
	if err != nil {
		return nil, err
	}
	a.hcMu.Lock()
	c := a.currentConnLocked(hostID, p)
	var drop *ssh.Client
	if prev := a.hcUsers[u]; prev != nil && prev != c {
		drop = prev.leaveLocked(u)
	}
	c.joinLocked(u)
	a.hcMu.Unlock()
	closeClient(drop)
	return c, nil
}

// moveToCurrent moves u from the stale connection old to the host's current one.
func (a *App) moveToCurrent(old *hostConn, u any) (*hostConn, error) {
	p, err := a.hostParamsOf(old.hostID)
	if err != nil {
		return nil, err
	}
	a.hcMu.Lock()
	c := a.currentConnLocked(old.hostID, p)
	var drop *ssh.Client
	if a.hcUsers[u] == old {
		drop = old.leaveLocked(u)
		c.joinLocked(u)
	}
	a.hcMu.Unlock()
	closeClient(drop)
	return c, nil
}

// connOf is the connection u has joined, nil if none.
func (a *App) connOf(u any) *hostConn {
	a.hcMu.Lock()
	defer a.hcMu.Unlock()
	return a.hcUsers[u]
}

// currentConnLocked returns the host's connection for p, creating it; the caller holds hcMu.
func (a *App) currentConnLocked(hostID string, p hostParams) *hostConn {
	if c := a.hostConns[hostID]; c != nil {
		if c.params == p {
			return c
		}
		c.markStaleLocked()
	}
	c := &hostConn{
		a: a, hostID: hostID, params: p,
		emit:     func(st HostConnStateDTO) { a.emitHostConnState(hostID, st) },
		askFwd:   func() *connAsker { return a.forwardsAsker(hostID) },
		interval: a.fwdKeepalive,
		backoff:  a.hcBackoff,
		users:    map[any]*ssh.Client{},
		shutdown: a.hcDown,
	}
	if c.interval <= 0 {
		c.interval = keepaliveInterval
	}
	if c.backoff == nil {
		c.backoff = sshclient.DefaultBackoff
	}
	if a.hostKeyChanged[hostID] {
		c.blocked = fmt.Errorf("%w: подключение к хосту заблокировано до перезапуска программы", sshclient.ErrHostKeyChanged)
	}
	c.st = HostConnStateDTO{Status: hostConnNone, Retryable: c.blocked == nil}
	if !c.shutdown {
		if a.hostConns == nil {
			a.hostConns = map[string]*hostConn{}
		}
		a.hostConns[hostID] = c
	}
	return c
}

// joinLocked adds u to c; the caller holds hcMu.
func (c *hostConn) joinLocked(u any) {
	a := c.a
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.shutdown {
		if a.hcLive == nil {
			a.hcLive = map[*hostConn]struct{}{}
		}
		a.hcLive[c] = struct{}{}
	}
	if _, ok := c.users[u]; !ok {
		c.users[u] = nil
	}
	if a.hcUsers == nil {
		a.hcUsers = map[any]*hostConn{}
	}
	a.hcUsers[u] = c
}

// leave removes u from the connection it is on now, which may be another one than c when u was
// moved off a stale connection. See leaveLocked.
func (c *hostConn) leave(u any) {
	a := c.a
	a.hcMu.Lock()
	target := a.hcUsers[u]
	if target == nil {
		target = c
	}
	drop := target.leaveLocked(u)
	a.hcMu.Unlock()
	closeClient(drop)
}

// leaveLocked removes u. The last user closes the client, stops the login or reconnect and wipes
// the secrets; the connection leaves a.hostConns. When the last forward leaves a reconnect, the
// reconnect stops; sessions waiting for it get a login of their own, which may use the secrets.
// It returns the client for the caller to close outside the locks; the caller holds hcMu.
func (c *hostConn) leaveLocked(u any) *ssh.Client {
	a := c.a
	if a.hcUsers[u] == c {
		delete(a.hcUsers, u)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.users, u)
	switch {
	case len(c.users) == 0:
		if a.hostConns[c.hostID] == c {
			delete(a.hostConns, c.hostID)
		}
		delete(a.hcLive, c)
		return c.idleLocked()
	case c.att != nil && c.att.loop && !c.hasForwardsLocked():
		att := c.att
		c.att = nil
		if len(att.waiting) > 0 {
			att.handover = true
		} else {
			c.forgetLocked()
		}
		att.cancel()
		c.setLocked(HostConnStateDTO{Status: hostConnNone})
	}
	return nil
}

// acquire returns the connection's client: at once when connected, otherwise after the login in
// progress or a new one answered by ask. It never returns a client whose connection has ended.
// While the connection reconnects for its forwards, acquire waits for that attempt. A stale
// connection serves its live client but never logs in again: without one, u moves to the host's
// current connection and acquires there, with the host's new parameters and none of the old secrets.
func (c *hostConn) acquire(ctx context.Context, u any, ask *connAsker) (*ssh.Client, error) {
	for {
		if cur := c.a.connOf(u); cur != nil && cur != c {
			return cur.acquire(ctx, u, ask) // u was moved off this stale connection
		}
		c.mu.Lock()
		if c.blocked != nil {
			err := c.blocked
			c.mu.Unlock()
			return nil, err
		}
		if c.shutdown {
			c.mu.Unlock()
			return nil, sshclient.ErrCancelled
		}
		if cl := c.liveClientLocked(); cl != nil {
			last, member := c.users[u]
			if member {
				c.users[u] = cl
			}
			dead := c.dead
			c.mu.Unlock()
			if last != cl {
				return cl, nil
			}
			// u asks again for the client it already has: it has most likely seen the connection
			// end before the watcher did. A client still alive after the grace is returned (an R
			// forward asks again after the server refused its port).
			t := time.NewTimer(deadClientGrace)
			select {
			case <-dead:
				t.Stop()
				continue
			case <-t.C:
				return cl, nil
			}
		}
		if c.stale {
			c.mu.Unlock()
			next, err := c.a.moveToCurrent(c, u)
			if err != nil {
				return nil, err
			}
			return next.acquire(ctx, u, ask)
		}
		att := c.att
		if att == nil {
			att = c.startLocked(ask, false, 0)
		}
		att.waiting = append(att.waiting, ask)
		wait := waitText(att.ask, ask)
		c.mu.Unlock()
		if wait != "" {
			_, _ = io.WriteString(ask.out, wait) //nolint:gosec // G104: outStream never fails
		}

		select {
		case <-att.done:
		case <-ctx.Done():
			c.stopWaiting(att, ask)
			return nil, ctx.Err()
		}
		switch {
		case att.err == nil:
			continue // the client is checked for life like a cached one
		case att.handover && ctx.Err() == nil:
			continue // its asker left: a new login with ask or another waiter's
		}
		return nil, att.err
	}
}

// stopWaiting removes ask from att's waiters. The login ends when nobody waits any more; when its
// asker leaves and nobody else answers through the same terminal, it is restarted for the others.
func (c *hostConn) stopWaiting(att *loginAttempt, ask *connAsker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := slices.Index(att.waiting, ask); i >= 0 {
		att.waiting = slices.Delete(att.waiting, i, i+1)
	}
	select {
	case <-att.done:
		return
	default:
	}
	if att.loop {
		return
	}
	if len(att.waiting) == 0 {
		att.cancel()
		return
	}
	sameTerminal := slices.ContainsFunc(att.waiting, func(w *connAsker) bool { return w.out == att.ask.out })
	if ask == att.ask && !sameTerminal {
		att.handover = true
		att.cancel()
	}
}

// markStale wipes the secrets and detaches the connection from the host: its users stay on its
// client while it lives, new users get a new connection. A stale connection emits no state and
// never logs in again; a reconnect in progress ends and its waiters move to the current connection.
func (c *hostConn) markStale() {
	c.a.hcMu.Lock()
	defer c.a.hcMu.Unlock()
	c.markStaleLocked()
}

// markStaleLocked is markStale; the caller holds App.hcMu.
func (c *hostConn) markStaleLocked() {
	if c.a.hostConns[c.hostID] == c {
		delete(c.a.hostConns, c.hostID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stale = true
	c.forgetLocked()
	if att := c.att; att != nil && att.loop {
		c.att = nil
		att.handover = true
		att.cancel()
	}
}

func (c *hostConn) state() HostConnStateDTO {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

// closeAllHostConns stops every connection, wipes every secret and waits for their goroutines (app exit).
func (a *App) closeAllHostConns() {
	a.hcMu.Lock()
	a.hcDown = true
	all := map[*hostConn]struct{}{}
	for c := range a.hcLive {
		all[c] = struct{}{}
	}
	for _, c := range a.hostConns {
		all[c] = struct{}{}
	}
	a.hostConns, a.hcLive, a.hcUsers = nil, nil, nil
	a.hcMu.Unlock()
	for c := range all {
		c.mu.Lock()
		c.shutdown = true
		drop := c.idleLocked()
		c.mu.Unlock()
		closeClient(drop)
	}
	for c := range all {
		c.wg.Wait()
	}
}

// startLocked starts a login answered by ask after delay; the caller holds mu and has checked shutdown.
func (c *hostConn) startLocked(ask *connAsker, loop bool, delay time.Duration) *loginAttempt {
	ctx, cancel := context.WithCancel(context.Background())
	att := &loginAttempt{ask: ask, loop: loop, delay: delay, ctx: ctx, cancel: cancel, done: make(chan struct{}), keep: true}
	c.att = att
	st := HostConnStateDTO{Status: hostConnConnecting, Asker: "forwards"}
	if ask.label != "" {
		st.Asker, st.Session = "session", ask.label
	}
	if loop { // the last failure stays visible while the reconnect waits
		st.Error, st.Detail = c.st.Error, c.st.Detail
	}
	c.setLocked(st)
	c.wg.Add(1)
	go c.run(att)
	return att
}

func (c *hostConn) run(att *loginAttempt) {
	defer c.wg.Done()
	if att.delay > 0 {
		t := time.NewTimer(att.delay)
		select {
		case <-att.ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
	var client *ssh.Client
	err := att.ctx.Err()
	if err == nil {
		client, err = c.login(att)
	}
	c.finish(att, client, err)
}

// finish publishes att's outcome: a kept client, a reconnect retry, or an error for its waiters.
func (c *hostConn) finish(att *loginAttempt, client *ssh.Client, err error) {
	c.mu.Lock()
	cancelled := att.ctx.Err() != nil
	att.cancel()
	current := c.att == att
	if current {
		c.att = nil
	}
	var drop *ssh.Client
	if err == nil && (c.shutdown || !current || cancelled && !att.handover) {
		drop, client, err = client, nil, sshclient.ErrCancelled
	}
	keyChanged := errors.Is(err, sshclient.ErrHostKeyChanged)
	switch {
	case err == nil:
		c.client, c.dead, c.retries = client, make(chan struct{}), 0
		c.wg.Add(2)
		go c.watch(client, c.dead)
		go func() {
			defer c.wg.Done()
			keepalive(client, c.interval, keepaliveMaxFails)
		}()
		c.setLocked(HostConnStateDTO{Status: hostConnConnected})
	case !current:
		// Abandoned by the last user or the shutdown, which have already reset the state.
	case errors.Is(err, errConnStale):
		att.handover = true // its waiters move to the host's current connection
		c.setLocked(HostConnStateDTO{Status: hostConnNone})
	case keyChanged || errors.Is(err, sshclient.ErrHostNotTrusted):
		if keyChanged {
			c.blocked = err
		}
		c.forgetLocked()
		c.setLocked(errorDTO(err))
	case cancelled:
		c.setLocked(HostConnStateDTO{Status: hostConnNone})
	case att.loop && !c.stale && c.hasForwardsLocked():
		if errors.Is(err, sshclient.ErrAuthDenied) {
			c.forgetLocked() // the server rejected the remembered secret: the next try asks again
		}
		c.setLocked(errorDTO(err))
		c.retries++
		c.startLocked(c.askFwd(), true, c.backoff(c.retries))
	default:
		c.forgetLocked()
		c.setLocked(errorDTO(err))
	}
	att.client, att.err = client, err
	close(att.done)
	c.mu.Unlock()
	closeClient(drop)
	if keyChanged {
		c.a.hcMu.Lock()
		if c.a.hostKeyChanged == nil {
			c.a.hostKeyChanged = map[string]bool{}
		}
		c.a.hostKeyChanged[c.hostID] = true
		c.a.hcMu.Unlock()
	}
}

func errorDTO(err error) HostConnStateDTO {
	return HostConnStateDTO{Status: hostConnError, Error: err.Error(), Detail: sshclient.ErrorDetail(err)}
}

// watch notices the end of client's connection.
func (c *hostConn) watch(client *ssh.Client, dead chan struct{}) {
	defer c.wg.Done()
	_ = client.Wait() //nolint:gosec // G104: only the end of the connection matters
	close(dead)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropLocked(client)
}

// liveClientLocked returns the client unless its connection has ended; the caller holds mu.
func (c *hostConn) liveClientLocked() *ssh.Client {
	if c.client == nil {
		return nil
	}
	select {
	case <-c.dead:
		c.dropLocked(c.client)
		return nil
	default:
		return c.client
	}
}

// dropLocked handles the end of cl's connection once: with forwards on it a reconnect starts,
// otherwise the secrets are wiped and the next acquire logs in anew (on a stale connection: moves
// to the current one). The caller holds mu.
func (c *hostConn) dropLocked(cl *ssh.Client) {
	if c.client != cl {
		return
	}
	c.client, c.dead = nil, nil
	if !c.shutdown && !c.stale && c.blocked == nil && c.hasForwardsLocked() {
		c.retries = 1
		c.startLocked(c.askFwd(), true, c.backoff(c.retries))
		return
	}
	c.forgetLocked()
	c.setLocked(HostConnStateDTO{Status: hostConnNone})
}

// idleLocked stops the login, wipes the secrets and returns the client for the caller to close
// outside mu. The caller holds mu.
func (c *hostConn) idleLocked() *ssh.Client {
	if c.att != nil {
		c.att.cancel()
		c.att = nil
	}
	drop := c.client
	c.client, c.dead, c.retries = nil, nil, 0
	c.forgetLocked()
	c.setLocked(HostConnStateDTO{Status: hostConnNone})
	return drop
}

// forgetLocked wipes the remembered secrets; a login in progress keeps none. The caller holds mu.
func (c *hostConn) forgetLocked() {
	clear(c.passphrase)
	clear(c.password)
	c.passphrase, c.passKeyID, c.password = nil, "", nil
	if c.att != nil {
		c.att.keep = false
	}
}

func (c *hostConn) hasForwardsLocked() bool {
	for u := range c.users {
		if _, ok := u.(*fwdRun); ok {
			return true
		}
	}
	return false
}

// setLocked records and emits the state; Retryable follows the host key block. A stale connection
// only records it: the host's group shows the current connection. The caller holds mu.
func (c *hostConn) setLocked(st HostConnStateDTO) {
	st.Retryable = c.blocked == nil
	c.st = st
	if c.emit != nil && !c.stale {
		c.emit(st)
	}
}

func closeClient(c *ssh.Client) {
	if c != nil {
		_ = c.Close() //nolint:gosec // G104: the connection is being discarded
	}
}

// keepalive sends keepalive@openssh.com every interval and closes the client after maxFails
// consecutive errors or replies slower than an interval, so a silently dead connection is
// noticed and the runner reconnects. It ends when the client is closed.
func keepalive(c *ssh.Client, interval time.Duration, maxFails int) {
	dead := make(chan struct{})
	go func() {
		_ = c.Wait() //nolint:gosec // G104: only the end of the connection matters
		close(dead)
	}()
	t := time.NewTimer(interval)
	defer t.Stop()
	fails := 0
	var reply chan error // nil while no request is in flight
	for {
		select {
		case <-dead:
			return
		case err := <-reply:
			reply = nil
			if err != nil {
				fails++
			} else {
				fails = 0
			}
			continue // the next request waits for the timer
		case <-t.C:
		}
		if reply != nil { // no answer within an interval
			fails++
		}
		if fails >= maxFails {
			_ = c.Close() //nolint:gosec // G104: closing ends the dead connection; its error is irrelevant
			return
		}
		if reply == nil {
			reply = make(chan error, 1)
			go func(ch chan error) {
				_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
				ch <- err
			}(reply)
		}
		t.Reset(interval)
	}
}
