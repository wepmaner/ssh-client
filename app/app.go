// Package app holds the Wails-bound application object.
package app

import (
	"context"
	"log"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshclient/internal/model"
)

// App is bound to the frontend and receives Wails lifecycle callbacks.
type App struct {
	ctx context.Context
}

// New creates an App.
func New() *App {
	return &App{}
}

// Startup stores the Wails runtime context and restores the saved window geometry.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	ws, ok := loadWindowState()
	if !ok || ws.W <= 0 {
		return
	}
	runtime.WindowSetPosition(ctx, ws.X, ws.Y)
	runtime.WindowSetSize(ctx, ws.W, ws.H)
	if ws.Maximized {
		runtime.WindowMaximise(ctx)
	}
}

// BeforeClose saves the window geometry while the window is still alive.
// It never prevents the close.
func (a *App) BeforeClose(ctx context.Context) bool {
	var ws model.WindowState
	if runtime.WindowIsMaximised(ctx) {
		// Keep the previous un-maximised geometry to restore into.
		var ok bool
		if ws, ok = loadWindowState(); !ok || ws.W <= 0 {
			ws = model.DefaultSettings().Window
		}
		ws.Maximized = true
	} else {
		ws.X, ws.Y = runtime.WindowGetPosition(ctx)
		ws.W, ws.H = runtime.WindowGetSize(ctx)
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
