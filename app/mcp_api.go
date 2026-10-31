package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"

	"sshclient/internal/mcpserver"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const (
	mcpStateOff     = "off"
	mcpStateRunning = "running"
	mcpStateError   = "error"

	mcpMinPort   = 1024
	mcpMaxPort   = 65535
	mcpTokenSize = 32
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errMCPOff = errors.New("Сначала разрешите доступ по MCP")

// MCPStatusDTO is the state of the MCP server for the Settings screen. State is off, running or
// error; Error is the Russian text of a failed start, Detail its system text. It never carries the
// token.
type MCPStatusDTO struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	State   string `json:"state"`
	Error   string `json:"error"`
	Detail  string `json:"detail"`
}

// mcpStart brings the server in line with the settings of an open vault: it is called when the
// vault opens and after a restore. A locked vault starts nothing: the token is unknown until the
// unlock. A running server stays as it is.
func (a *App) mcpStart() {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	a.mcpStartLocked()
}

// mcpStartLocked is mcpStart for a caller holding mcpLife.
func (a *App) mcpStartLocked() {
	if a.mcpSrv != nil || a.exiting.Load() || a.restoring.Load() || !a.storeUnlocked() {
		return
	}
	set := a.storeRef().Settings()
	st := MCPStatusDTO{Enabled: set.MCPEnabled, Port: set.MCPPort, State: mcpStateOff}
	a.mcpToken = ""
	if set.MCPEnabled {
		a.mcpToken = set.MCPToken
		if l, err := mcpserver.Listen(set.MCPPort); err != nil {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(set.MCPPort))
			st.State, st.Error, st.Detail = mcpStateError, sshclient.ListenError(addr, err).Error(), err.Error()
		} else {
			b := &mcpBackend{a: a}
			a.mcpLive.Store(b)
			a.mcpLn = l
			a.mcpSrv = mcpserver.Serve(l, set.MCPToken, a.version, b)
			st.State = mcpStateRunning
		}
	}
	a.mcpPublishLocked(st)
}

// mcpCloseLocked closes the port and ends the HTTP requests, whose calls end with them; the holds
// stay, so a new token or port does not ask for a passphrase again. The listener is closed here
// too: Serve may not have taken it yet, and the port must be free on return.
func (a *App) mcpCloseLocked() {
	if a.mcpSrv == nil {
		return
	}
	a.mcpLive.Store(nil)
	_ = a.mcpSrv.Close() //nolint:gosec // G104: the server is being discarded
	_ = a.mcpLn.Close()  //nolint:gosec // G104: Serve may have closed it already
	a.mcpSrv, a.mcpLn = nil, nil
}

// mcpStop closes the port, cancels the running calls and lets go of the connections. The caller
// runs it before closeAllHostConns.
func (a *App) mcpStop() {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	a.mcpStopLocked()
}

func (a *App) mcpStopLocked() {
	a.mcpCloseLocked()
	a.mcpReleaseAll()
	a.mcpToken = ""
	st := a.mcpStatusLocked()
	st.Enabled, st.State, st.Error, st.Detail = false, mcpStateOff, "", ""
	a.mcpPublishLocked(st)
}

func (a *App) mcpPublishLocked(st MCPStatusDTO) {
	a.mcpState = st
	a.events().Emit("mcp:status", st)
}

// mcpStatusLocked is the last state, or the default one before the first start.
func (a *App) mcpStatusLocked() MCPStatusDTO {
	if a.mcpState.State != "" {
		return a.mcpState
	}
	st := MCPStatusDTO{Port: model.DefaultMCPPort, State: mcpStateOff}
	if a.storeUnlocked() {
		st.Port = a.storeRef().Settings().MCPPort
	}
	return st
}

// mcpEditable refuses a change of the settings while the vault cannot take it.
func (a *App) mcpEditable() error {
	if a.restoring.Load() {
		return errRestoring
	}
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	return nil
}

func newMCPToken() (string, error) {
	b := make([]byte, mcpTokenSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// MCPStatus returns the state of the server. It works on a locked vault: the server goes on then.
func (a *App) MCPStatus() MCPStatusDTO {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	return a.mcpStatusLocked()
}

// MCPSetEnabled switches the server. The first switch-on creates the token. A port that cannot be
// taken is no failure: the status says why, and the rest of the app works.
func (a *App) MCPSetEnabled(on bool) (MCPStatusDTO, error) {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	if err := a.mcpEditable(); err != nil {
		return a.mcpStatusLocked(), err
	}
	set := a.storeRef().Settings()
	if !on {
		a.mcpStopLocked() // first: the port never stays open behind a failed save
		st := a.mcpStatusLocked()
		return st, a.storeRef().SaveMCP(false, set.MCPPort, set.MCPToken)
	}
	if set.MCPToken == "" {
		token, err := newMCPToken()
		if err != nil {
			return a.mcpStatusLocked(), err
		}
		set.MCPToken = token
	}
	if err := a.storeRef().SaveMCP(true, set.MCPPort, set.MCPToken); err != nil {
		return a.mcpStatusLocked(), err
	}
	a.mcpStartLocked()
	return a.mcpStatusLocked(), nil
}

// MCPSetPort stores the port and moves a server that is on to it. A refusal is a field error
// (field:port: text).
func (a *App) MCPSetPort(port int) (MCPStatusDTO, error) {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	if err := a.mcpEditable(); err != nil {
		return a.mcpStatusLocked(), err
	}
	if port < mcpMinPort || port > mcpMaxPort {
		return a.mcpStatusLocked(), fieldError("port", "Порт от 1024 до 65535")
	}
	set := a.storeRef().Settings()
	if port == set.MCPPort && a.mcpSrv != nil {
		return a.mcpStatusLocked(), nil
	}
	if err := a.storeRef().SaveMCP(set.MCPEnabled, port, set.MCPToken); err != nil {
		return a.mcpStatusLocked(), err
	}
	a.mcpCloseLocked()
	a.mcpStartLocked()
	return a.mcpStatusLocked(), nil
}

// MCPRotateToken replaces the token: the old one stops working at once. The server restarts on the
// same port, ending the calls of its requests; a port lost meanwhile shows in the status.
func (a *App) MCPRotateToken() error {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	if err := a.mcpEditable(); err != nil {
		return err
	}
	token, err := newMCPToken()
	if err != nil {
		return err
	}
	set := a.storeRef().Settings()
	if err := a.storeRef().SaveMCP(set.MCPEnabled, set.MCPPort, token); err != nil {
		return err
	}
	a.mcpCloseLocked()
	a.mcpStartLocked()
	return nil
}

// mcpCopyText builds a text with the token for the clipboard. Only these two methods let the token
// out of Go.
func (a *App) mcpCopyText(build func(port int, token string) string) (string, error) {
	a.mcpLife.Lock()
	defer a.mcpLife.Unlock()
	if !a.storeUnlocked() {
		return "", errVaultLocked
	}
	st := a.mcpStatusLocked()
	if !st.Enabled {
		return "", errMCPOff
	}
	return build(st.Port, a.mcpToken), nil
}

// MCPAddCommand returns the command that registers the server in Claude Code.
func (a *App) MCPAddCommand() (string, error) { return a.mcpCopyText(mcpserver.AddCommand) }

// MCPPrompt returns the text that makes an AI session register the server by itself.
func (a *App) MCPPrompt() (string, error) { return a.mcpCopyText(mcpserver.Prompt) }

// MCPJournalList returns the journal, newest first; empty while the vault is locked.
func (a *App) MCPJournalList() []model.MCPRecord {
	if !a.storeUnlocked() {
		return []model.MCPRecord{}
	}
	if j := a.storeRef().MCPJournal(); j != nil {
		return j
	}
	return []model.MCPRecord{}
}

// MCPJournalClear empties the journal.
func (a *App) MCPJournalClear() error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	return a.storeRef().MCPJournalClear()
}
