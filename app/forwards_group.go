package app

import (
	"encoding/base64"
	"slices"

	"sshclient/internal/model"
)

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
		g.out.latestEvent = "fwdhost:state:" + hostID // every connection of the host emits it
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

// ForwardHostDTO is a host in the Forwards view with the state of its connection.
type ForwardHostDTO struct {
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
	HostConnStateDTO
}

// ForwardHostsList returns the hosts that have forwards, sorted by host name, with the state of
// their connection.
func (a *App) ForwardHostsList() []ForwardHostDTO {
	out := []ForwardHostDTO{}
	if !a.storeUnlocked() {
		return out
	}
	has := map[string]bool{}
	for _, f := range a.storeRef().Forwards() {
		has[f.HostID] = true
	}
	for _, h := range a.storeRef().Hosts() {
		if has[h.ID] {
			out = append(out, ForwardHostDTO{HostID: h.ID, HostName: h.Name, HostConnStateDTO: a.hostConnState(h.ID)})
		}
	}
	slices.SortStableFunc(out, byName(func(h ForwardHostDTO) string { return h.HostName }))
	return out
}

// forwardsOfHost returns the saved forwards of a host that exists.
func (a *App) forwardsOfHost(hostID string) ([]model.Forward, error) {
	if _, err := a.hostParamsOf(hostID); err != nil {
		return nil, err
	}
	var out []model.Forward
	for _, f := range a.storeRef().Forwards() {
		if f.HostID == hostID {
			out = append(out, f)
		}
	}
	return out, nil
}

// ForwardStartAll starts the host's stopped and failed forwards and returns at once. They share the
// host's connection: its login asks once, in the host's forwards group.
func (a *App) ForwardStartAll(hostID string) error {
	if a.restoring.Load() {
		return errRestoring
	}
	fwds, err := a.forwardsOfHost(hostID)
	if err != nil {
		return err
	}
	for _, f := range fwds {
		a.startForward(f)
	}
	return nil
}

// ForwardStopAll stops every forward of the host; when it returns their ports are free. The host's
// terminal sessions are left alone; without them the connection closes and forgets its secrets.
func (a *App) ForwardStopAll(hostID string) error {
	fwds, err := a.forwardsOfHost(hostID)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, f := range fwds {
		ids[f.ID] = true
	}
	a.stopForwards(func(id string) bool { return ids[id] })
	a.clearHostError(hostID)
	return nil
}

// ForwardHostWrite sends base64-encoded keystrokes from the host group's mini-terminal to the
// question of the login in progress. Input with no login waiting is dropped.
func (a *App) ForwardHostWrite(hostID, dataB64 string) error {
	b, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return errBadInput
	}
	defer clear(b) // the keystrokes may be a passphrase or a password; the input queue keeps its own copy
	a.fwdHostMu.Lock()
	defer a.fwdHostMu.Unlock()
	if g := a.fwdHosts[hostID]; g != nil && g.in != nil {
		g.in.Write(b)
	}
	return nil
}

// ForwardHostAttach tells Go the UI listens to the host group's events: the output and states held
// since startup (autostart logins) are released, later ones are sent at once.
func (a *App) ForwardHostAttach(hostID string) error {
	if _, err := a.hostParamsOf(hostID); err != nil {
		return err
	}
	a.fwdHostGroup(hostID).out.Attach()
	return nil
}

// dropAllFwdHostGroups forgets the groups of every host (a restore replaces the hosts).
func (a *App) dropAllFwdHostGroups() {
	a.fwdHostMu.Lock()
	defer a.fwdHostMu.Unlock()
	a.fwdHosts = nil
}

// dropFwdHostGroup forgets the group of a deleted host.
func (a *App) dropFwdHostGroup(hostID string) {
	a.fwdHostMu.Lock()
	defer a.fwdHostMu.Unlock()
	delete(a.fwdHosts, hostID)
}
