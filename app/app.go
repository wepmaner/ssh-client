// Package app holds the Wails-bound application object.
package app

import (
	"context"
	"errors"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
	"sshclient/internal/store"
)

// App is bound to the frontend and receives Wails lifecycle callbacks.
type App struct {
	ctx      context.Context
	vault    *store.Vault
	store    *store.Store
	vaultErr error
}

// New creates an App.
func New() *App {
	return &App{}
}

const vaultFile = "vault.json"

// VaultResetCorrupt moves a damaged vault aside as vault.json.corrupt and
// starts a fresh one.
func (a *App) VaultResetCorrupt() error {
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

// storeUnlocked reports whether window state can live in the vault.
func (a *App) storeUnlocked() bool {
	return a.store != nil && !a.vault.Locked()
}

// Startup opens the vault and restores the saved window geometry. A locked
// password vault falls back to the plain window.json geometry.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.openVault()
	var ws model.WindowState
	ok := false
	if a.storeUnlocked() {
		ws = a.store.Settings().Window
		// The untouched default of a brand-new vault carries no position.
		// A fresh vault has no geometry yet; use an existing window.json once.
		ok = ws != model.DefaultSettings().Window
		if !ok {
			ws, ok = loadWindowState()
		}
	} else {
		ws, ok = loadWindowState()
	}
	if !ok || ws.W <= 0 {
		return
	}
	runtime.WindowSetPosition(ctx, ws.X, ws.Y)
	runtime.WindowSetSize(ctx, ws.W, ws.H)
	if ws.Maximized {
		runtime.WindowMaximise(ctx)
	}
}

// previousWindowState returns the last stored geometry from the active backend.
func (a *App) previousWindowState() (model.WindowState, bool) {
	if a.storeUnlocked() {
		ws := a.store.Settings().Window
		return ws, ws.W > 0
	}
	return loadWindowState()
}

// BeforeClose saves the window geometry while the window is still alive,
// into the vault when unlocked, otherwise into window.json.
// It never prevents the close.
func (a *App) BeforeClose(ctx context.Context) bool {
	var ws model.WindowState
	if runtime.WindowIsMaximised(ctx) {
		// Keep the previous un-maximised geometry to restore into.
		var ok bool
		if ws, ok = a.previousWindowState(); !ok || ws.W <= 0 {
			ws = model.DefaultSettings().Window
		}
		ws.Maximized = true
	} else {
		ws.X, ws.Y = runtime.WindowGetPosition(ctx)
		ws.W, ws.H = runtime.WindowGetSize(ctx)
	}
	if a.storeUnlocked() {
		set := a.store.Settings()
		set.Window = ws
		if err := a.store.SaveSettings(set); err != nil {
			log.Printf("save window state: %v", err)
		}
		return false
	}
	if err := saveWindowState(ws); err != nil {
		log.Printf("save window state: %v", err)
	}
	return false
}

// Shutdown is called after the window has closed.
func (a *App) Shutdown(ctx context.Context) {
	_ = ctx
}
