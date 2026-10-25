//go:build !windows

package app

import "github.com/wailsapp/wails/v3/pkg/application"

// fixRestoreScale works around a Windows-only DPI bug (restorefix_windows.go);
// other systems need nothing. The stub lets the package build and vet there.
func fixRestoreScale(*application.WebviewWindow) {}
