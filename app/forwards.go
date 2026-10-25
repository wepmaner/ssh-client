package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const (
	// keepaliveInterval is how often a forward's client asks keepalive@openssh.com; a reply
	// that takes longer than one interval counts as a failure.
	keepaliveInterval = 15 * time.Second
	// keepaliveMaxFails consecutive failures close the client, so the runner reconnects.
	keepaliveMaxFails = 3
)

// ForwardStateDTO is the payload of fwd:state:<id>. Retryable is false when the last dial
// failed on a changed server key: starting again cannot help until known_hosts is fixed.
type ForwardStateDTO struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	Detail    string `json:"detail"` // the system text under Error, shown small on the card
	Conns     int    `json:"conns"`
	Retryable bool   `json:"retryable"`
}

// fwdEntry is the runtime of one saved forward. out lives as long as the entry, so the
// mini-terminal sees the prompts of every run in order; it is held until ForwardAttach.
type fwdEntry struct {
	id  string
	out *outStream
	run *fwdRun // nil while stopped; guarded by App.fwdMu
}

// fwdRun is one Start…Stop cycle of a forward: a one-shot runner, its prompt input and the
// goroutines that start it and pass its state events on.
type fwdRun struct {
	runner *sshclient.ForwardRunner
	in     *inputQueue // answers to the prompts of this run's dials
	cancel context.CancelFunc
	done   chan struct{} // closed when the start goroutine and the event pump have ended

	stopOnce       sync.Once
	hostKeyChanged atomic.Bool

	// The key passphrase the server accepted in this run, so reconnects do not ask again. It
	// lives in memory only (never in the store, a DTO, an event or a log) and dies with the run:
	// stop (Stop, Delete, app exit, a restart by ForwardSave) and a final error clear it.
	mu          sync.Mutex
	secret      []byte
	secretKeyID string // the key the secret opens
	forgotten   bool   // the run has ended: a late OnPassphrase is not kept
}

// secretFor returns a copy of the remembered passphrase of key keyID, nil if there is none.
// The caller owns the copy (sshclient.Dial clears it).
func (r *fwdRun) secretFor(keyID string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.secret == nil || r.secretKeyID != keyID {
		return nil
	}
	return bytes.Clone(r.secret)
}

// rememberSecret keeps a copy of p, the passphrase of key keyID the server just accepted.
func (r *fwdRun) rememberSecret(keyID string, p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.forgotten {
		return
	}
	clear(r.secret)
	r.secret, r.secretKeyID = bytes.Clone(p), keyID
}

// forgetSecret wipes the remembered passphrase for good.
func (r *fwdRun) forgetSecret() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.secret)
	r.secret, r.secretKeyID, r.forgotten = nil, "", true
}

// stop cancels a pending prompt and the dial, stops the runner (the port is free afterwards),
// waits for the run's goroutines and wipes the remembered passphrase. Safe to call more than
// once and concurrently.
func (r *fwdRun) stop() {
	r.stopOnce.Do(func() {
		r.in.Close()
		r.cancel()
		r.runner.Stop()
	})
	<-r.done
	r.forgetSecret()
}

func (r *fwdRun) state() ForwardStateDTO { return r.dto(r.runner.State()) }

func (r *fwdRun) dto(st sshclient.ForwardState) ForwardStateDTO {
	return ForwardStateDTO{Status: st.Status, Error: st.Error, Detail: st.Detail, Conns: st.Conns, Retryable: !r.hostKeyChanged.Load()}
}

// active reports a run that is about to start, connecting or forwarding. A run in e.run is only
// stopped before its Start (stopped runs leave e.run), so only a failed Start makes it inactive.
func (r *fwdRun) active() bool {
	return r.runner.State().Status != sshclient.ForwardError
}

func (e *fwdEntry) stateEvent() string { return "fwd:state:" + e.id }

// fwdEntryLocked returns the entry of id, creating it; the caller holds fwdMu.
func (a *App) fwdEntryLocked(id string) *fwdEntry {
	if a.fwds == nil {
		a.fwds = map[string]*fwdEntry{}
	}
	e, ok := a.fwds[id]
	if !ok {
		e = &fwdEntry{id: id, out: newOutStreamFor(a.events(), "fwd:data:"+id)}
		a.fwds[id] = e
	}
	return e
}

// startForward starts f unless it is already connecting or running. It returns at once:
// the dial, its prompts and the outcome arrive as fwd:state and fwd:data events.
func (a *App) startForward(f model.Forward) {
	a.fwdMu.Lock()
	e := a.fwdEntryLocked(f.ID)
	prev := e.run
	if prev != nil && prev.active() {
		a.fwdMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &fwdRun{in: newInputQueue(), cancel: cancel, done: make(chan struct{})}
	run.runner = sshclient.NewForwardRunner(f, a.forwardDial(f.ID, f.HostID, e.out, run))
	e.run = run
	a.fwdMu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if prev != nil {
			prev.stop() // a failed run: its pump must end before this run's events begin
		}
		if ctx.Err() == nil {
			_ = run.runner.Start(ctx) //nolint:gosec // G104: the error reaches the UI as the error state
		}
	}()
	go func() {
		defer wg.Done()
		for st := range run.runner.Events() {
			if st.Status == sshclient.ForwardError {
				run.forgetSecret() // error is final for a run: no more dials will need it
			}
			e.out.Event(e.stateEvent(), run.dto(st), false)
		}
	}()
	go func() {
		wg.Wait()
		close(run.done)
	}()
}

// stopForward stops the forward's current run, if any, and waits until its port is free.
func (a *App) stopForward(id string) {
	a.fwdMu.Lock()
	var run *fwdRun
	if e, ok := a.fwds[id]; ok {
		run, e.run = e.run, nil
	}
	a.fwdMu.Unlock()
	if run != nil {
		run.stop()
	}
}

// removeForward stops the forward and forgets its runtime.
func (a *App) removeForward(id string) {
	a.stopForward(id)
	a.fwdMu.Lock()
	delete(a.fwds, id)
	a.fwdMu.Unlock()
}

// stopAllForwards stops every forward in parallel and waits for them (app shutdown).
func (a *App) stopAllForwards() {
	a.fwdMu.Lock()
	var runs []*fwdRun
	for _, e := range a.fwds {
		if e.run != nil {
			runs = append(runs, e.run)
			e.run = nil
		}
	}
	a.fwdMu.Unlock()
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.stop()
		}()
	}
	wg.Wait()
}

// forwardState is the current state of a forward for ForwardsList.
func (a *App) forwardState(id string) ForwardStateDTO {
	run := a.currentRun(id)
	if run == nil {
		return ForwardStateDTO{Status: sshclient.ForwardStopped, Retryable: true}
	}
	return run.state()
}

// currentRun returns the forward's run, nil while it is stopped.
func (a *App) currentRun(id string) *fwdRun {
	a.fwdMu.Lock()
	defer a.fwdMu.Unlock()
	if e, ok := a.fwds[id]; ok {
		return e.run
	}
	return nil
}

// forwardActive reports a forward that is starting, connecting or running.
func (a *App) forwardActive(id string) bool {
	run := a.currentRun(id)
	return run != nil && run.active()
}

// autostartForwards starts every AutoStart forward that is not running yet.
func (a *App) autostartForwards() {
	if !a.storeUnlocked() {
		return
	}
	for _, f := range a.store.Forwards() {
		if f.AutoStart {
			a.startForward(f)
		}
	}
}

// forwardDial returns the runner's dial: like a terminal session it looks the host and key up
// at dial time, asks to trust an unknown server key through the trust dialog and asks its other
// questions through a Prompter, here over the mini-terminal of the forward's card. Cancelling
// ctx closes the run's input and withdraws the trust question, so a pending prompt returns at once.
func (a *App) forwardDial(id, hostID string, out *outStream, run *fwdRun) func(ctx context.Context) (*ssh.Client, error) {
	interval := a.fwdKeepalive
	if interval <= 0 {
		interval = keepaliveInterval
	}
	return func(ctx context.Context) (*ssh.Client, error) {
		stop := context.AfterFunc(ctx, func() { run.in.Close() })
		defer stop()
		if !a.storeUnlocked() {
			return nil, errVaultLocked
		}
		_, t, err := a.hostTarget(hostID)
		if err != nil {
			return nil, err
		}
		kh, err := a.knownHosts()
		if err != nil {
			return nil, err
		}
		t.Trust = a.trustAsker(trustOwner{forwardID: id}, t.User, out)
		if t.Key != nil {
			keyID := t.Key.ID
			t.Passphrase = run.secretFor(keyID) // Dial clears this copy
			t.OnPassphrase = func(p []byte) { run.rememberSecret(keyID, p) }
		}
		client, err := sshclient.Dial(ctx, t, sshclient.NewPrompter(out, run.in), kh)
		run.hostKeyChanged.Store(errors.Is(err, sshclient.ErrHostKeyChanged))
		if err != nil {
			return nil, err
		}
		go keepalive(client, interval, keepaliveMaxFails)
		return client, nil
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
