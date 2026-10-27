package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/google/uuid"

	"sshclient/internal/sshclient"
)

// TrustAskDTO is the payload of trust:ask: a session (SessionID) or a forward (ForwardID) met an
// unknown server key and waits for TrustAnswer(ID, …). Fingerprint is the SHA256 one.
type TrustAskDTO struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ForwardID   string `json:"forward_id"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	KeyType     string `json:"key_type"`
	Fingerprint string `json:"fingerprint"`
}

// TrustCancelDTO is the payload of trust:cancel: the question was withdrawn (tab closed, forward
// stopped, a newer question of the same owner) and its dialog must close without an answer.
type TrustCancelDTO struct {
	ID string `json:"id"`
}

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errTrustNotFound = errors.New("Вопрос о доверии серверу уже снят")

// trustOwner is who asks: exactly one of the fields is set. forwardHostID is the forwards group of
// a host, which answers the logins its forwards start.
type trustOwner struct{ sessionID, forwardID, forwardHostID string }

// pendingTrust is an open question. answer is buffered so TrustAnswer never blocks; gone is
// closed when a newer question of the same owner replaced this one.
type pendingTrust struct {
	id     string
	owner  trustOwner
	answer chan bool
	gone   chan struct{}
}

// TrustAnswer answers the open trust question id: true trusts the key (it is written to
// known_hosts and the dial goes on), false ends the dial with «Подключение отменено: сервер не
// подтверждён». An unknown, answered or withdrawn id is an error.
func (a *App) TrustAnswer(id string, trust bool) error {
	a.trustMu.Lock()
	p, ok := a.trustByID[id]
	if ok {
		a.dropTrustLocked(p)
	}
	a.trustMu.Unlock()
	if !ok {
		return errTrustNotFound
	}
	p.answer <- trust
	return nil
}

// addTrust registers p and returns the older question of the same owner, now unregistered.
func (a *App) addTrust(p *pendingTrust) *pendingTrust {
	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	if a.trustByID == nil {
		a.trustByID = map[string]*pendingTrust{}
		a.trustByOwner = map[trustOwner]*pendingTrust{}
	}
	old := a.trustByOwner[p.owner]
	if old != nil {
		a.dropTrustLocked(old)
	}
	a.trustByID[p.id] = p
	a.trustByOwner[p.owner] = p
	return old
}

// removeTrust unregisters p if it is still open.
func (a *App) removeTrust(p *pendingTrust) {
	a.trustMu.Lock()
	defer a.trustMu.Unlock()
	if a.trustByID[p.id] == p {
		a.dropTrustLocked(p)
	}
}

// dropTrustLocked unregisters p; the caller holds trustMu.
func (a *App) dropTrustLocked(p *pendingTrust) {
	delete(a.trustByID, p.id)
	if a.trustByOwner[p.owner] == p {
		delete(a.trustByOwner, p.owner)
	}
}

// appTrustAsker asks through the app's trust dialog for one session or forward. Its events go
// through the owner's outStream, so they follow the terminal output in order and wait, like it,
// until the view has attached.
type appTrustAsker struct {
	a     *App
	owner trustOwner
	user  string
	out   *outStream
}

// trustAsker is the TrustAsker of a session or forward dial.
func (a *App) trustAsker(owner trustOwner, user string, out *outStream) sshclient.TrustAsker {
	return appTrustAsker{a: a, owner: owner, user: user, out: out}
}

// AskTrust emits trust:ask and blocks until TrustAnswer, a newer question of the same owner
// (ErrCancelled) or the end of ctx (ctx.Err()); a withdrawn question is announced as trust:cancel.
// No lock is held while waiting.
func (t appTrustAsker) AskTrust(ctx context.Context, q sshclient.TrustQuestion) (bool, error) {
	p := &pendingTrust{id: uuid.NewString(), owner: t.owner, answer: make(chan bool, 1), gone: make(chan struct{})}
	if old := t.a.addTrust(p); old != nil {
		close(old.gone)
	}
	addr := net.JoinHostPort(q.Host, strconv.Itoa(q.Port))
	// One line for the history; the answer comes from the dialog, not the terminal.
	_, _ = fmt.Fprintf(t.out, "Сервер %s неизвестен, отпечаток %s\r\n", addr, q.FingerprintSHA256) //nolint:gosec // G104: outStream never fails
	t.out.Event("trust:ask", TrustAskDTO{
		ID: p.id, SessionID: t.owner.sessionID, ForwardID: t.owner.forwardID,
		Host: q.Host, Port: q.Port, User: t.user, KeyType: q.KeyType, Fingerprint: q.FingerprintSHA256,
	}, false)

	withdraw := func() { t.out.Event("trust:cancel", TrustCancelDTO{ID: p.id}, false) }
	select {
	case ok := <-p.answer:
		return ok, nil
	case <-p.gone:
		withdraw()
		return false, sshclient.ErrCancelled
	case <-ctx.Done():
		t.a.removeTrust(p)
		withdraw()
		return false, ctx.Err()
	}
}
