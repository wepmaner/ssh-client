package app

import (
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"

	"sshclient/internal/model"
)

// User-facing messages of the forwards API.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errForwardNotFound = errors.New("Проброс не найден")

const defaultListenHost = "127.0.0.1"

// ForwardDTO is a saved forward with its live state. Target is empty for D.
type ForwardDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // L|R|D
	HostID    string `json:"host_id"`
	HostName  string `json:"host_name"`
	Listen    string `json:"listen"`
	Target    string `json:"target"`
	AutoStart bool   `json:"auto_start"`
	Status    string `json:"status"` // stopped|starting|running|error
	Error     string `json:"error"`
	Detail    string `json:"detail"`
	Conns     int    `json:"conns"`
	Retryable bool   `json:"retryable"`
}

func (a *App) forwardDTO(f model.Forward, hostNames map[string]string) ForwardDTO {
	st := a.forwardState(f)
	return ForwardDTO{
		ID: f.ID, Name: f.Name, Kind: f.Kind, HostID: f.HostID, HostName: hostNames[f.HostID],
		Listen: f.Listen, Target: f.Target, AutoStart: f.AutoStart,
		Status: st.Status, Error: st.Error, Detail: st.Detail, Conns: st.Conns, Retryable: st.Retryable,
	}
}

func (a *App) hostNames() map[string]string {
	out := map[string]string{}
	for _, h := range a.storeRef().Hosts() {
		out[h.ID] = h.Name
	}
	return out
}

// ForwardsList returns the saved forwards sorted by name, with their current state.
func (a *App) ForwardsList() []ForwardDTO {
	out := []ForwardDTO{}
	if !a.storeUnlocked() {
		return out
	}
	names := a.hostNames()
	for _, f := range a.storeRef().Forwards() {
		out = append(out, a.forwardDTO(f, names))
	}
	slices.SortStableFunc(out, byName(func(f ForwardDTO) string { return f.Name }))
	return out
}

func (a *App) findForward(id string) (model.Forward, error) {
	all := a.storeRef().Forwards()
	i := slices.IndexFunc(all, func(f model.Forward) bool { return f.ID == id })
	if i < 0 {
		return model.Forward{}, errForwardNotFound
	}
	return all[i], nil
}

// splitAddr checks host:port with a port of 1–65535; defaultHost fills an omitted host
// ("5432" or ":5432"), an empty defaultHost makes the host required.
func splitAddr(field, s, defaultHost string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fieldError(field, "Укажите адрес и порт")
	}
	if defaultHost != "" && !strings.Contains(s, ":") {
		s = ":" + s
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fieldError(field, "Формат адрес:порт, например 127.0.0.1:5432")
	}
	if host == "" {
		if defaultHost == "" {
			return "", fieldError(field, "Укажите адрес перед портом")
		}
		host = defaultHost
	}
	if strings.ContainsAny(host, " \t") {
		return "", fieldError(field, "Адрес не должен содержать пробелов")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fieldError(field, "Порт от 1 до 65535")
	}
	return net.JoinHostPort(host, port), nil
}

// validateForward trims and checks the form; the first problem is returned as a field error.
func (a *App) validateForward(in ForwardDTO) (model.Forward, error) {
	f := model.Forward{ID: in.ID, Name: strings.TrimSpace(in.Name), Kind: in.Kind, HostID: in.HostID, AutoStart: in.AutoStart}
	if f.Kind != "L" && f.Kind != "R" && f.Kind != "D" {
		return f, fieldError("kind", "Выберите тип проброса")
	}
	if f.HostID == "" || !slices.ContainsFunc(a.storeRef().Hosts(), func(h model.Host) bool { return h.ID == f.HostID }) {
		return f, fieldError("host", "Выберите хост")
	}
	var err error
	if f.Listen, err = splitAddr("listen", in.Listen, defaultListenHost); err != nil {
		return f, err
	}
	if f.Kind != "D" {
		if f.Target, err = splitAddr("target", in.Target, ""); err != nil {
			return f, err
		}
	}
	if f.ID != "" {
		if _, err := a.findForward(f.ID); err != nil {
			return f, err
		}
	}
	if f.Name == "" {
		f.Name = f.Kind + " " + shortListen(f.Listen) + " → " + f.Target
		if f.Kind == "D" {
			f.Name = "SOCKS5 " + shortListen(f.Listen)
		}
	}
	return f, nil
}

// shortListen drops the default listen host: «127.0.0.1:5432» is shown as «5432».
func shortListen(addr string) string {
	return strings.TrimPrefix(addr, defaultListenHost+":")
}

// endpointChanged reports a change that needs a new connection or port.
func endpointChanged(x, y model.Forward) bool {
	return x.Kind != y.Kind || x.HostID != y.HostID || x.Listen != y.Listen || x.Target != y.Target
}

// ForwardSave creates a forward (empty ID) or updates one. Listen may omit the host
// (127.0.0.1), D has no Target, an empty name becomes «<Kind> <Listen> → <Target>» or
// «SOCKS5 <Listen>», without a 127.0.0.1 host. A connecting or running forward restarts only when
// Kind, HostID, Listen or Target changed, on the same connection when the host stays; a forward in
// error with a changed endpoint becomes stopped.
func (a *App) ForwardSave(in ForwardDTO) (ForwardDTO, error) {
	if !a.storeUnlocked() {
		return ForwardDTO{}, errVaultLocked
	}
	f, err := a.validateForward(in)
	if err != nil {
		return ForwardDTO{}, err
	}
	old, existed := model.Forward{}, false
	if f.ID != "" {
		old, err = a.findForward(f.ID)
		existed = err == nil
	}
	saved, err := a.storeRef().SaveForward(f)
	if err != nil {
		return ForwardDTO{}, err
	}
	if existed && endpointChanged(old, saved) {
		if a.forwardActive(saved.ID) {
			a.restartForward(saved)
		} else {
			a.stopForward(saved.ID)
		}
	}
	return a.forwardDTO(saved, a.hostNames()), nil
}

// ForwardDelete stops the forward and removes it.
func (a *App) ForwardDelete(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if _, err := a.findForward(id); err != nil {
		return err
	}
	if err := a.storeRef().DeleteForward(id); err != nil {
		return err
	}
	a.removeForward(id)
	return nil
}

// ForwardStart starts the forward and returns at once; it does nothing to a forward that is
// connecting or running. Progress and errors (a busy port, a failed login) arrive as fwd:state:<id>
// events; the login questions are asked in the host's forwards group (fwdhost:data:<hostID>).
func (a *App) ForwardStart(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	f, err := a.findForward(id)
	if err != nil {
		return err
	}
	a.startForward(f)
	return nil
}

// ForwardStop stops the forward; when it returns the local port is free. The host's connection and
// its other users go on; the login it waited for ends when nobody else waits for it.
func (a *App) ForwardStop(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if _, err := a.findForward(id); err != nil {
		return err
	}
	a.stopForward(id)
	return nil
}

// ForwardAttach tells Go the UI listens to the forward's events: states held since startup
// (autostart) are released, later ones are sent at once.
func (a *App) ForwardAttach(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if _, err := a.findForward(id); err != nil {
		return err
	}
	a.fwdMu.Lock()
	e := a.fwdEntryLocked(id)
	a.fwdMu.Unlock()
	e.out.Attach()
	return nil
}

// deleteForwardsOfHost stops and removes the forwards of a deleted host.
func (a *App) deleteForwardsOfHost(hostID string) error {
	var errs []error
	for _, f := range a.storeRef().Forwards() {
		if f.HostID != hostID {
			continue
		}
		a.stopForward(f.ID)
		if err := a.storeRef().DeleteForward(f.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		a.removeForward(f.ID)
	}
	return errors.Join(errs...)
}
