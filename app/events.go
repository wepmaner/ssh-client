package app

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// emitter sends events to the frontend. Tests replace the Wails implementation with a recorder.
type emitter interface {
	Emit(name string, payload any)
}

// wailsEmitter emits through the Wails runtime of the running window.
type wailsEmitter struct{ ctx context.Context }

func (w wailsEmitter) Emit(name string, payload any) { runtime.EventsEmit(w.ctx, name, payload) }

// nopEmitter drops events emitted before Startup.
type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

func (a *App) events() emitter {
	if a.emit == nil {
		return nopEmitter{}
	}
	return a.emit
}
