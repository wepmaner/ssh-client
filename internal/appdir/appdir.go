// Package appdir resolves the per-user application data directory.
package appdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns %LOCALAPPDATA%\sshclient, creating it on first use.
func Dir() (string, error) {
	// On Windows UserCacheDir resolves %LOCALAPPDATA% and errors when it is empty.
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("appdir: locate LOCALAPPDATA: %w", err)
	}
	dir := filepath.Join(base, "sshclient")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("appdir: create %s: %w", dir, err)
	}
	return dir, nil
}
