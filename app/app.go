// Package app holds the Wails-bound application object.
package app

import "context"

// App is bound to the frontend and receives Wails lifecycle callbacks.
type App struct {
	ctx context.Context
}

// New creates an App.
func New() *App {
	return &App{}
}

// Startup stores the Wails runtime context.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// Shutdown is called when the window closes.
func (a *App) Shutdown(ctx context.Context) {
	_ = ctx
}
