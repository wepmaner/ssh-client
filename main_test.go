package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWebviewDataDir(t *testing.T) {
	data := filepath.Join(t.TempDir(), "sshclient")
	if got := webviewDataDir(data, nil); got != filepath.Join(data, "WebView2") {
		t.Fatalf("data folder: %q", got)
	}
	want := filepath.Join(os.TempDir(), "sshclient", "WebView2")
	if got := webviewDataDir("", errors.New("no APPDATA")); got != want {
		t.Fatalf("fallback: %q, want %q", got, want)
	}
	if got := webviewDataDir("", nil); got != want {
		t.Fatalf("empty dir: %q, want %q", got, want)
	}
}

func TestMainWindowSnapLayouts(t *testing.T) {
	w := mainWindow()
	if !w.Frameless || w.Windows.DisableFramelessWindowDecorations {
		t.Fatal("frameless window with system decorations expected")
	}
	// Both are needed for --wails-non-client-region on the maximise button.
	if !w.Windows.NonClientRegionSupport || !w.Windows.WebView2CompositionHosting {
		t.Fatal("non-client regions are off: no Snap layouts on the maximise button")
	}
}
