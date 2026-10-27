package app

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

// ForwardStateDTO is the payload of fwd:state:<id>. Retryable is false while the forward's host is
// blocked by a changed server key: starting again cannot help until known_hosts is fixed.
type ForwardStateDTO struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	Detail    string `json:"detail"` // the system text under Error, shown small on the card
	Conns     int    `json:"conns"`
	Retryable bool   `json:"retryable"`
}

// fwdEntry is the runtime of one saved forward. out carries its fwd:state events and lives as long
// as the entry, so the card sees the states of every run in order; they are held until ForwardAttach.
// The login questions are asked in the host's forwards group (forwards_group.go), not here.
type fwdEntry struct {
	id  string
	out *outStream
	run *fwdRun // nil while stopped; guarded by App.fwdMu
}

// fwdRun is one Start…Stop cycle of a forward: a one-shot runner on its host's shared connection
// and the goroutines that start it and pass its state events on. The run is a user of the
// connection from its start until it stops or ends in error.
type fwdRun struct {
	runner *sshclient.ForwardRunner
	conn   *hostConn // the connection joined at start; nil when the host could not be looked up
	cancel context.CancelFunc
	done   chan struct{} // closed when the start goroutine and the event pump have ended

	stopOnce  sync.Once
	leaveOnce sync.Once
}

// stop cancels the run's wait for a login and stops the runner (the port is free afterwards), waits
// for the run's goroutines and leaves the connection. Safe to call more than once and concurrently.
func (r *fwdRun) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		r.runner.Stop()
	})
	<-r.done
	r.leave()
}

// leave takes the run off its connection once; the last user closes it and wipes its secrets.
func (r *fwdRun) leave() {
	r.leaveOnce.Do(func() {
		if r.conn != nil {
			r.conn.leave(r)
		}
	})
}

func fwdStateDTO(st sshclient.ForwardState, retryable bool) ForwardStateDTO {
	return ForwardStateDTO{Status: st.Status, Error: st.Error, Detail: st.Detail, Conns: st.Conns, Retryable: retryable}
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
		e = &fwdEntry{id: id, out: newOutStreamFor(a.events(), "")} // never written: states only
		a.fwds[id] = e
	}
	return e
}

// startForward starts f unless it is already connecting or running. It returns at once: the login,
// its questions (in the host's forwards group) and the outcome arrive as events.
func (a *App) startForward(f model.Forward) { a.launchForward(f, false) }

// restartForward replaces the forward's run with a new one for f (ForwardSave changed its kind,
// ports or host). The new run joins its connection before the old one leaves, so a forward that is
// the only user of the connection keeps it. When it returns the old port is free.
func (a *App) restartForward(f model.Forward) { a.launchForward(f, true) }

func (a *App) launchForward(f model.Forward, restart bool) {
	a.fwdMu.Lock()
	e := a.fwdEntryLocked(f.ID)
	prev := e.run
	if !restart && prev != nil && prev.active() {
		a.fwdMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &fwdRun{cancel: cancel, done: make(chan struct{})}
	// Joined under fwdMu (lock order fwdMu, then hcMu), so a stop that takes the run from e.run
	// always finds it on its connection.
	conn, err := a.joinHostConn(f.HostID, run)
	run.conn = conn
	run.runner = sshclient.NewSharedForwardRunner(f, a.forwardAcquire(f.HostID, conn, run, err))
	e.run = run
	a.fwdMu.Unlock()
	if prev != nil {
		prev.stop() // its port is free and its pump has ended before this run's events begin
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if ctx.Err() == nil {
			_ = run.runner.Start(ctx) //nolint:gosec // G104: the error reaches the UI as the error state
		}
	}()
	go func() {
		defer wg.Done()
		for st := range run.runner.Events() {
			if st.Status == sshclient.ForwardError {
				run.leave() // error is final for a run: it no longer uses the connection
			}
			e.out.Event(e.stateEvent(), fwdStateDTO(st, a.hostRetryable(f.HostID)), false)
		}
	}()
	go func() {
		wg.Wait()
		close(run.done)
	}()
}

// forwardAcquire is the runner's source of the shared client: the client of the host's connection,
// whose login questions go to the host's forwards group. joinErr (the host or the vault is gone)
// fails every attempt.
func (a *App) forwardAcquire(hostID string, conn *hostConn, run *fwdRun, joinErr error) func(ctx context.Context) (*ssh.Client, error) {
	return func(ctx context.Context) (*ssh.Client, error) {
		if joinErr != nil {
			return nil, joinErr
		}
		cl, err := conn.acquire(ctx, run, a.forwardsAsker(hostID))
		if errors.Is(err, sshclient.ErrHostKeyChanged) {
			a.markHostKeyChanged(hostID) // before the runner reports the error: its state is not retryable
		}
		return cl, err
	}
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

// stopForwards stops the forwards whose IDs pass pick (every forward when pick is nil) in parallel
// and waits until their ports are free.
func (a *App) stopForwards(pick func(id string) bool) {
	a.fwdMu.Lock()
	var runs []*fwdRun
	for id, e := range a.fwds {
		if e.run != nil && (pick == nil || pick(id)) {
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

// stopAllForwards stops every forward and waits for them (app shutdown).
func (a *App) stopAllForwards() { a.stopForwards(nil) }

// forwardState is the current state of a forward for ForwardsList.
func (a *App) forwardState(f model.Forward) ForwardStateDTO {
	st := sshclient.ForwardState{Status: sshclient.ForwardStopped}
	if run := a.currentRun(f.ID); run != nil {
		st = run.runner.State()
	}
	return fwdStateDTO(st, a.hostRetryable(f.HostID))
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

// autostartForwards starts every AutoStart forward that is not running yet. Those of one host share
// its connection and its one login.
func (a *App) autostartForwards() {
	if !a.storeUnlocked() {
		return
	}
	for _, f := range a.storeRef().Forwards() {
		if f.AutoStart {
			a.startForward(f)
		}
	}
}
