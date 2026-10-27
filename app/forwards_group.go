package app

// fwdHostGroup is the forwards group of one host in the Forwards view: the mini-terminal that
// answers the logins its forwards start, and the input of the current one.
type fwdHostGroup struct {
	out *outStream
	in  *inputQueue // guarded by App.fwdHostMu; nil before the first login
}

// fwdHostGroup returns the host's group, creating it.
func (a *App) fwdHostGroup(hostID string) *fwdHostGroup {
	a.fwdHostMu.Lock()
	defer a.fwdHostMu.Unlock()
	if a.fwdHosts == nil {
		a.fwdHosts = map[string]*fwdHostGroup{}
	}
	g, ok := a.fwdHosts[hostID]
	if !ok {
		g = &fwdHostGroup{out: newOutStreamFor(a.events(), "fwdhost:data:"+hostID)}
		a.fwdHosts[hostID] = g
	}
	return g
}

// forwardsAsker answers a login through the host's forwards group, with a fresh input per login.
func (a *App) forwardsAsker(hostID string) *connAsker {
	g := a.fwdHostGroup(hostID)
	return &connAsker{
		out:   g.out,
		trust: trustOwner{forwardHostID: hostID},
		newInput: func() *inputQueue {
			q := newInputQueue()
			a.fwdHostMu.Lock()
			g.in = q
			a.fwdHostMu.Unlock()
			return q
		},
	}
}

// emitHostConnState sends the host connection's state as fwdhost:state:<hostID>, in order with the
// group's output.
func (a *App) emitHostConnState(hostID string, st HostConnStateDTO) {
	a.fwdHostGroup(hostID).out.Event("fwdhost:state:"+hostID, st, false)
}
