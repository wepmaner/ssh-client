// Package app holds the application object, bound to the frontend as the one Wails service.
package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"sshclient/internal/appdir"
	"sshclient/internal/importers"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/store"
)

// App is the Wails service: its public methods are bound to the frontend, and
// it receives the service lifecycle callbacks.
type App struct {
	window  application.WebviewWindowOptions // the main window without saved geometry
	wapp    *application.App                 // nil before ServiceStartup and in tests
	mainWin *application.WebviewWindow

	// refs holds the vault, its store and the open error as one value: VaultResetCorrupt and
	// VaultRestore replace it while other bound calls read it. refsMu serialises the writers.
	refs   atomic.Pointer[vaultRefs]
	refsMu sync.Mutex

	emit emitter // term:*, fwd:* events; Wails in ServiceStartup, a recorder in tests

	sessMu   sync.Mutex
	sessions map[string]*session
	sessSeq  int

	khMu sync.Mutex
	kh   *sshclient.KnownHosts // opened on the first session

	trustMu      sync.Mutex
	trustByID    map[string]*pendingTrust     // open trust questions by question ID
	trustByOwner map[trustOwner]*pendingTrust // at most one open question per session or forwards group

	fwdMu        sync.Mutex
	fwds         map[string]*fwdEntry // forward runtimes by forward ID
	fwdKeepalive time.Duration        // keepalive interval of host connection clients; 0 means keepaliveInterval
	autostarted  atomic.Bool          // the autostart forwards have been started in this run
	restoring    atomic.Bool          // VaultRestore is swapping the vault: new sessions and forwards are refused
	exiting      atomic.Bool          // ServiceShutdown has begun: a restore must not reopen host connections

	// hcMu guards the host connections (hostconn.go); it is taken before a hostConn's own lock.
	hcMu           sync.Mutex
	hostConns      map[string]*hostConn            // the current connection of each host
	hcLive         map[*hostConn]struct{}          // every connection with users, stale ones included
	hcUsers        map[any]*hostConn               // the connection each user (*fwdRun, *session, *mcpHold, *mcpCall) is on
	hostKeyChanged map[string]bool                 // hosts whose server key changed: blocked until restart
	hcLastErr      map[string]HostConnStateDTO     // the error state a host's connection closed in, shown until the next login
	hcDown         bool                            // closeAllHostConns has run
	hcBackoff      func(attempt int) time.Duration // reconnect pauses; nil means sshclient.DefaultBackoff

	fwdHostMu sync.Mutex // guards fwdHosts and each group's input; taken last
	fwdHosts  map[string]*fwdHostGroup

	// mcpMu guards the MCP holds and logins (mcp_conn.go). It is taken alone: neither hcMu nor a
	// hostConn's lock is held with it, in either order.
	mcpMu         sync.Mutex
	mcpHolds      map[string]*mcpHold // the hold of each host
	mcpHoldTTL    time.Duration       // 0 means mcpHoldTime
	mcpLoginLimit time.Duration       // 0 means mcpLoginTime

	version string                   // the release version, "dev" in local builds
	putty   importers.RegistryReader // PuTTY sessions source; nil means the Windows registry
}

// New creates an App. window is the main window, created in ServiceStartup
// once the saved geometry is known; version is shown in Settings.
func New(window application.WebviewWindowOptions, version string) *App {
	return &App{window: window, version: version}
}

const vaultFile = "vault.json"

// vaultRefs is one generation of the vault: either an open vault with its store, or the error
// it failed to open with. It is never changed after it is published.
type vaultRefs struct {
	vault *store.Vault
	store *store.Store
	err   error
}

// current returns the published vault generation; the zero value before openVault.
func (a *App) current() vaultRefs {
	if r := a.refs.Load(); r != nil {
		return *r
	}
	return vaultRefs{}
}

// vaultRef and storeRef return the vault and the store of the current generation; nil when
// the vault did not open. A caller that needs both reads current once.
func (a *App) vaultRef() *store.Vault { return a.current().vault }
func (a *App) storeRef() *store.Store { return a.current().store }

// setVault publishes a new generation.
func (a *App) setVault(v *store.Vault, s *store.Store, err error) {
	a.refs.Store(&vaultRefs{vault: v, store: s, err: err})
}

// VaultResetCorrupt moves a damaged vault aside as vault.json.corrupt and
// starts a fresh one. It refuses unless the vault failed to open as corrupt.
func (a *App) VaultResetCorrupt() error {
	a.refsMu.Lock()
	defer a.refsMu.Unlock()
	if !errors.Is(a.current().err, store.ErrCorrupt) {
		return errors.New("vault is not corrupt")
	}
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	v, err := store.RecoverCorrupt(filepath.Join(dir, vaultFile))
	if err != nil {
		return err
	}
	a.setVault(v, store.NewStore(v), nil)
	return nil
}

// openVault opens the vault file in the app directory. A damaged file is kept
// untouched and its error is stored for the UI.
func (a *App) openVault() {
	a.refsMu.Lock()
	defer a.refsMu.Unlock()
	dir, err := appdir.Dir()
	if err != nil {
		a.setVault(nil, nil, err)
		return
	}
	v, err := store.OpenVault(filepath.Join(dir, vaultFile))
	if err != nil {
		a.setVault(nil, nil, err)
		if !errors.Is(err, store.ErrCorrupt) {
			log.Printf("open vault: %v", err)
		}
		return
	}
	a.setVault(v, store.NewStore(v), nil)
}

// initVault opens the vault and, when it needs no password, starts the autostart forwards.
// A password vault starts them in VaultUnlock.
func (a *App) initVault() {
	a.openVault()
	a.autostartOnce()
}

// autostartOnce starts the autostart forwards the first time the vault is open in this run.
// An unlock after the idle lock starts nothing: running forwards go on by themselves, and
// forwards the user stopped stay stopped.
func (a *App) autostartOnce() {
	if !a.storeUnlocked() || !a.autostarted.CompareAndSwap(false, true) {
		return
	}
	a.autostartForwards()
}

// storeUnlocked reports whether window state can live in the vault.
func (a *App) storeUnlocked() bool {
	r := a.current()
	return r.store != nil && !r.vault.Locked()
}

// ServiceStartup opens the vault, then creates the main window at the saved
// geometry, so it shows in place without a jump. It runs before Wails starts
// pending windows.
func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	a.wapp = application.Get()
	a.emit = wailsEmitter{a.wapp.Event}
	a.initVault()

	ws, ok := a.startupWindowState()
	// The monitors are known by now: Wails reads them when the application is created.
	a.mainWin = a.wapp.Window.NewWithOptions(mainWindowOptions(a.window, ws, ok, a.wapp.Screen))
	// The hook runs before the window goes away, while its geometry is readable;
	// it never cancels the close.
	a.mainWin.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		a.saveWindowGeometry(a.mainWin)
	})
	fixRestoreScale(a.mainWin)
	return nil
}

// ServiceShutdown runs after the window has closed; it ends every SSH session and forward.
func (a *App) ServiceShutdown() error {
	a.exiting.Store(true)
	a.closeAllSessions()
	a.stopAllForwards()
	a.closeAllHostConns()
	return nil
}

// startupWindowState returns the geometry to open with. A locked password
// vault falls back to the plain window.json geometry.
func (a *App) startupWindowState() (model.WindowState, bool) {
	if !a.storeUnlocked() {
		return loadWindowState()
	}
	ws := a.storeRef().Settings().Window
	// The untouched default of a brand-new vault carries no position.
	// A fresh vault has no geometry yet; use an existing window.json once.
	if ws == model.DefaultSettings().Window {
		return loadWindowState()
	}
	return ws, true
}

// previousWindowState returns the last stored geometry from the active backend.
func (a *App) previousWindowState() (model.WindowState, bool) {
	if a.storeUnlocked() {
		ws := a.storeRef().Settings().Window
		return ws, ws.W > 0
	}
	return loadWindowState()
}

// saveWindowGeometry stores the window geometry into the vault when unlocked,
// otherwise into window.json.
func (a *App) saveWindowGeometry(w geometryWindow) {
	ws, ok := captureWindowState(w, a.previousWindowState)
	if !ok {
		return // minimised with nothing saved before: the next start opens centred
	}
	if a.storeUnlocked() {
		set := a.storeRef().Settings()
		set.Window = ws
		if err := a.storeRef().SaveSettings(set); err != nil {
			log.Printf("save window state: %v", err)
		}
		return
	}
	if err := saveWindowState(ws); err != nil {
		log.Printf("save window state: %v", err)
	}
}
