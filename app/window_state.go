package app

import (
	"encoding/json"
	"os"
	"path/filepath"

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
