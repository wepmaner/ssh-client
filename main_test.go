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
	if got := webviewDataDir("", errors.New("no LOCALAPPDATA")); got != want {
		t.Fatalf("fallback: %q, want %q", got, want)
	}
	if got := webviewDataDir("", nil); got != want {
		t.Fatalf("empty dir: %q, want %q", got, want)
	}
}
