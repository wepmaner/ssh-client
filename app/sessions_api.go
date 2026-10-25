package app

import (
	"encoding/base64"
	"errors"
	"slices"

	"github.com/google/uuid"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

// User-facing messages of the sessions API.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errHostNotFound    = errors.New("Хост не найден")
	errHostNoKey       = errors.New("У хоста не выбран ключ")
	errSessionNotFound = errors.New("Сессия не найдена")
	errBadInput        = errors.New("Некорректные данные ввода")
	errBadSize         = errors.New("Некорректный размер терминала")
)

// SessionDTO is an open terminal tab as listed for the UI.
type SessionDTO struct {
	ID        string `json:"id"`
	HostID    string `json:"host_id"`
	HostName  string `json:"host_name"`
	State     string `json:"state"`
	LatencyMs int    `json:"latency_ms"` // 0 until the first keepalive answer
}

// SessionOpen starts connecting to a saved host and returns the session id at once. Progress
// arrives as term:state:<id> and term:data:<id> events, released after the view's first SessionResize.
func (a *App) SessionOpen(hostID string, cols, rows int) (string, error) {
	if !a.storeUnlocked() {
		return "", errVaultLocked
	}
	if cols <= 0 || rows <= 0 {
		return "", errBadSize
	}
	h, t, err := a.hostTarget(hostID)
	if err != nil {
		return "", err
	}
	s := newSession(uuid.NewString(), h.ID, h.Name, cols, rows, a.events())
	t.Trust = a.trustAsker(trustOwner{sessionID: s.id}, t.User, s.out)
	a.addSession(s)
	go s.run(t, a.knownHosts)
	return s.id, nil
}

// hostTarget looks up a saved host and its key and returns where to dial.
func (a *App) hostTarget(hostID string) (model.Host, sshclient.Target, error) {
	all := a.store.Hosts()
	i := slices.IndexFunc(all, func(h model.Host) bool { return h.ID == hostID })
	if i < 0 {
		return model.Host{}, sshclient.Target{}, errHostNotFound
	}
	h := all[i]
	if h.KeyID == "" { // an imported host waits for its key in the host dialog
		return model.Host{}, sshclient.Target{}, errHostNoKey
	}
	k, err := a.findKey(h.KeyID)
	if err != nil {
		return model.Host{}, sshclient.Target{}, err
	}
	return h, sshclient.Target{Address: h.Address, Port: h.Port, User: h.User, Key: &k}, nil
}

// SessionWrite sends base64-encoded keystrokes: to the connection prompts while connecting,
// to the shell afterwards.
func (a *App) SessionWrite(id, dataB64 string) error {
	s, err := a.session(id)
	if err != nil {
		return err
	}
	b, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return errBadInput
	}
	return s.write(b)
}

// SessionResize reports the terminal view size in cells.
func (a *App) SessionResize(id string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errBadSize
	}
	s, err := a.session(id)
	if err != nil {
		return err
	}
	return s.resize(cols, rows)
}

// SessionClose ends the session and forgets it; a pending prompt is cancelled.
func (a *App) SessionClose(id string) error {
	s, err := a.removeSession(id)
	if err != nil {
		return err
	}
	s.close()
	return nil
}

// SessionList returns the open sessions in opening order.
func (a *App) SessionList() []SessionDTO {
	a.sessMu.Lock()
	all := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		all = append(all, s)
	}
	a.sessMu.Unlock()
	slices.SortFunc(all, func(x, y *session) int { return x.seq - y.seq })
	out := make([]SessionDTO, 0, len(all))
	for _, s := range all {
		out = append(out, s.dto())
	}
	return out
}
