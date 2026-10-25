// Package app holds the application object, bound to the frontend as the one Wails service.
package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"sshclient/internal/appdir"
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

	vault    *store.Vault
	store    *store.Store
	vaultErr error

	emit emitter // term:*, fwd:* events; Wails in ServiceStartup, a recorder in tests

	sessMu   sync.Mutex
	sessions map[string]*session
	sessSeq  int

	khMu sync.Mutex
	kh   *sshclient.KnownHosts // opened on the first session

	fwdMu        sync.Mutex
	fwds         map[string]*fwdEntry // forward runtimes by forward ID
	fwdKeepalive time.Duration        // keepalive interval of forward clients; 0 means keepaliveInterval
}

// New creates an App. window is the main window, created in ServiceStartup
// once the saved geometry is known.
func New(window application.WebviewWindowOptions) *App {
	return &App{window: window}
}

const vaultFile = "vault.json"

// VaultResetCorrupt moves a damaged vault aside as vault.json.corrupt and
// starts a fresh one. It refuses unless the vault failed to open as corrupt.
func (a *App) VaultResetCorrupt() error {
	if !errors.Is(a.vaultErr, store.ErrCorrupt) {
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
	a.vault, a.store, a.vaultErr = v, store.NewStore(v), nil
	return nil
}

// openVault opens the vault file in the app directory. A damaged file is kept
// untouched and its error is stored for the UI.
func (a *App) openVault() {
	dir, err := appdir.Dir()
	if err != nil {
		a.vaultErr = err
		return
	}
	v, err := store.OpenVault(filepath.Join(dir, vaultFile))
	if err != nil {
		a.vaultErr = err
		if !errors.Is(err, store.ErrCorrupt) {
			log.Printf("open vault: %v", err)
		}
		return
	}
	a.vault, a.store = v, store.NewStore(v)
}

// initVault opens the vault and, when it needs no password, starts the autostart forwards.
// A password vault starts them in VaultUnlock.
func (a *App) initVault() {
	a.openVault()
	a.autostartForwards()
}

// storeUnlocked reports whether window state can live in the vault.
func (a *App) storeUnlocked() bool {
	return a.store != nil && !a.vault.Locked()
}

// ServiceStartup opens the vault, then creates the main window at the saved
// geometry, so it shows in place without a jump. It runs before Wails starts
// pending windows.
func (a *App) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	a.wapp = application.Get()
	a.emit = wailsEmitter{a.wapp.Event}
	a.initVault()

	ws, ok := a.startupWindowState()
	a.mainWin = a.wapp.Window.NewWithOptions(mainWindowOptions(a.window, ws, ok))
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
	a.closeAllSessions()
	a.stopAllForwards()
	return nil
}

// startupWindowState returns the geometry to open with. A locked password
// vault falls back to the plain window.json geometry.
func (a *App) startupWindowState() (model.WindowState, bool) {
	if !a.storeUnlocked() {
		return loadWindowState()
	}
	ws := a.store.Settings().Window
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
		ws := a.store.Settings().Window
		return ws, ws.W > 0
	}
	return loadWindowState()
}

// saveWindowGeometry stores the window geometry into the vault when unlocked,
// otherwise into window.json.
func (a *App) saveWindowGeometry(w geometryWindow) {
	ws := captureWindowState(w, a.previousWindowState)
	if a.storeUnlocked() {
		set := a.store.Settings()
		set.Window = ws
		if err := a.store.SaveSettings(set); err != nil {
			log.Printf("save window state: %v", err)
		}
		return
	}
	if err := saveWindowState(ws); err != nil {
		log.Printf("save window state: %v", err)
	}
}
