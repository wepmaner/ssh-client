package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
)

const windowFile = "window.json"

// loadWindowState reads the saved window geometry; ok is false when none is usable.
func loadWindowState() (model.WindowState, bool) {
	dir, err := appdir.Dir()
	if err != nil {
		return model.WindowState{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, windowFile)) //nolint:gosec // fixed file name under appdir.Dir()
	if err != nil {
		return model.WindowState{}, false
	}
	var ws model.WindowState
	if err := json.Unmarshal(data, &ws); err != nil {
		return model.WindowState{}, false
	}
	return ws, true
}

// saveWindowState writes the window geometry to the app directory.
func saveWindowState(ws model.WindowState) error {
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	data, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, windowFile), data, 0o600)
}

// geometryWindow is the part of the Wails window read when it closes.
type geometryWindow interface {
	Position() (int, int)
	Size() (int, int)
	IsMaximised() bool
	IsMinimised() bool
}

// screenList is the part of the Wails screen manager the startup geometry is checked against.
type screenList interface {
	GetAll() []*application.Screen
}

// minimisedCoord is where Windows parks a minimised window: (-32000, -32000).
const minimisedCoord = -32000

// mainWindowOptions puts the saved geometry into the window options, so the
// window is created in place and size instead of jumping after it shows. A saved
// place that no monitor shows any more (a disconnected monitor, a minimised
// window saved by an older build) keeps the size but opens centred.
func mainWindowOptions(base application.WebviewWindowOptions, ws model.WindowState, ok bool, screens screenList) application.WebviewWindowOptions {
	if !ok || ws.W <= 0 || ws.H <= 0 {
		return base
	}
	base.Width, base.Height = ws.W, ws.H
	if onScreen(application.Rect{X: ws.X, Y: ws.Y, Width: ws.W, Height: ws.H}, screens) {
		base.X, base.Y = ws.X, ws.Y
		base.InitialPosition = application.WindowXY
	}
	if ws.Maximized {
		base.StartState = application.WindowStateMaximised
		// Wails (beta.25) positions the window after maximising it. WindowXY would
		// move the maximised window to the saved corner, off its place by the frame
		// (it overhangs the work area on every side); centring it lands exactly where
		// Windows maximises it. X and Y still place the window it restores into.
		base.InitialPosition = application.WindowCentered
	}
	return base
}

// onScreen reports whether r overlaps the work area of a monitor. Without a monitor list
// there is nothing to check against, and the saved place is used.
func onScreen(r application.Rect, screens screenList) bool {
	if r.X <= minimisedCoord || r.Y <= minimisedCoord {
		return false
	}
	if screens == nil {
		return true
	}
	all := screens.GetAll()
	if len(all) == 0 {
		return true
	}
	for _, s := range all {
		if s != nil && !r.Intersect(s.WorkArea).IsEmpty() {
			return true
		}
	}
	return false
}

// captureWindowState reads the geometry to save. A maximised window keeps the
// previous un-maximised geometry to restore into; a minimised one keeps the
// previous state as it is, and with none ok is false: there is nothing to save.
func captureWindowState(w geometryWindow, previous func() (model.WindowState, bool)) (model.WindowState, bool) {
	var ws model.WindowState
	if w.IsMinimised() {
		return minimisedState(previous)
	}
	if w.IsMaximised() {
		var ok bool
		if ws, ok = previous(); !ok || ws.W <= 0 {
			ws = model.DefaultSettings().Window
		}
		ws.Maximized = true
		return ws, true
	}
	ws.X, ws.Y = w.Position()
	if ws.X <= minimisedCoord || ws.Y <= minimisedCoord {
		return minimisedState(previous)
	}
	ws.W, ws.H = w.Size()
	return ws, true
}

func minimisedState(previous func() (model.WindowState, bool)) (model.WindowState, bool) {
	if ws, ok := previous(); ok && ws.W > 0 {
		return ws, true
	}
	return model.WindowState{}, false
}
