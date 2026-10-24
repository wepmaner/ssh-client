package app

import (
	"os"
	"path/filepath"
	"testing"

	"sshclient/internal/model"
)

func TestWindowStateRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, ok := loadWindowState(); ok {
		t.Fatal("expected no state before save")
	}
	want := model.WindowState{X: 10, Y: 20, W: 900, H: 600, Maximized: true}
	if err := saveWindowState(want); err != nil {
		t.Fatal(err)
	}
	got, ok := loadWindowState()
	if !ok || got != want {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestLoadWindowStateCorrupt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	dir := filepath.Join(root, "sshclient")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, windowFile), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadWindowState(); ok {
		t.Fatal("corrupt file must not load")
	}
}

func TestSaveWindowStateNoLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	if err := saveWindowState(model.WindowState{W: 1}); err == nil {
		t.Fatal("expected error")
	}
}
