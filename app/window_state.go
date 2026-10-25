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
}

// mainWindowOptions puts the saved geometry into the window options, so the
// window is created in place and size instead of jumping after it shows.
func mainWindowOptions(base application.WebviewWindowOptions, ws model.WindowState, ok bool) application.WebviewWindowOptions {
	if !ok || ws.W <= 0 || ws.H <= 0 {
		return base
	}
	base.X, base.Y = ws.X, ws.Y
	base.Width, base.Height = ws.W, ws.H
	base.InitialPosition = application.WindowXY
	if ws.Maximized {
		base.StartState = application.WindowStateMaximised
	}
	return base
}

// captureWindowState reads the geometry to save. A maximised window keeps the
// previous un-maximised geometry to restore into.
func captureWindowState(w geometryWindow, previous func() (model.WindowState, bool)) model.WindowState {
	var ws model.WindowState
	if w.IsMaximised() {
		var ok bool
		if ws, ok = previous(); !ok || ws.W <= 0 {
			ws = model.DefaultSettings().Window
		}
		ws.Maximized = true
		return ws
	}
	ws.X, ws.Y = w.Position()
	ws.W, ws.H = w.Size()
	return ws
}
