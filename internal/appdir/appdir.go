// Package appdir resolves the per-user application data directory.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// name is the data folder name; Stash requires it to match the exe name (sshclient.exe).
const name = "sshclient"

// fallback is the old folder when Migrate could not move it: this run keeps using it,
// so no new data lands beside the unmoved one, and the next start tries again.
var fallback atomic.Pointer[string]

// Dir returns %APPDATA%\sshclient, creating it on first use.
func Dir() (string, error) {
	if old := fallback.Load(); old != nil {
		return *old, nil
	}
	dir, err := newDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("appdir: create %s: %w", dir, err)
	}
	return dir, nil
}

// newDir is %APPDATA%\sshclient without creating it.
func newDir() (string, error) {
	// On Windows UserConfigDir resolves %APPDATA% and errors when it is empty.
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("appdir: locate APPDATA: %w", err)
	}
	return filepath.Join(base, name), nil
}
