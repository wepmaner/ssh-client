package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

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

// fakeWindow records geometry calls in place of the Wails window.
type fakeWindow struct {
	x, y, w, h int
	maximised  bool
}

func (f *fakeWindow) Position() (int, int) { return f.x, f.y }
func (f *fakeWindow) Size() (int, int)     { return f.w, f.h }
func (f *fakeWindow) IsMaximised() bool    { return f.maximised }

func TestMainWindowOptionsWithoutStateKeepsDefaults(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800, Title: "sshclient"}
	got := mainWindowOptions(base, model.WindowState{}, false)
	if got.Width != 1280 || got.Height != 800 || got.InitialPosition != application.WindowCentered ||
		got.StartState != application.WindowStateNormal || got.Title != "sshclient" {
		t.Fatalf("got %+v", got)
	}
	if got := mainWindowOptions(base, model.WindowState{X: 5, W: 0}, true); got.InitialPosition != application.WindowCentered {
		t.Fatal("zero width must not be restored")
	}
}

func TestMainWindowOptionsRestoresGeometryBeforeShow(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800, MinWidth: 900}
	got := mainWindowOptions(base, model.WindowState{X: 100, Y: -20, W: 1000, H: 700}, true)
	if got.X != 100 || got.Y != -20 || got.Width != 1000 || got.Height != 700 ||
		got.InitialPosition != application.WindowXY || got.StartState != application.WindowStateNormal {
		t.Fatalf("got %+v", got)
	}
	if got.MinWidth != 900 {
		t.Fatal("base options lost")
	}
	got = mainWindowOptions(base, model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}, true)
	if got.StartState != application.WindowStateMaximised || got.Width != 1000 || got.X != 1 {
		t.Fatalf("maximised: got %+v", got)
	}
}

func TestCaptureWindowState(t *testing.T) {
	none := func() (model.WindowState, bool) { return model.WindowState{}, false }
	got := captureWindowState(&fakeWindow{x: 10, y: 20, w: 1100, h: 750}, none)
	if want := (model.WindowState{X: 10, Y: 20, W: 1100, H: 750}); got != want {
		t.Fatalf("normal: got %+v", got)
	}

	// Maximised keeps the previous un-maximised geometry to restore into.
	prev := func() (model.WindowState, bool) { return model.WindowState{X: 1, Y: 2, W: 1000, H: 700}, true }
	got = captureWindowState(&fakeWindow{w: 2560, h: 1400, maximised: true}, prev)
	if want := (model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}); got != want {
		t.Fatalf("maximised: got %+v", got)
	}
	got = captureWindowState(&fakeWindow{w: 2560, h: 1400, maximised: true}, none)
	want := model.DefaultSettings().Window
	want.Maximized = true
	if got != want {
		t.Fatalf("maximised without previous: got %+v", got)
	}
}

func TestSaveWindowGeometryLockedGoesToFile(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a := &App{}
	a.saveWindowGeometry(&fakeWindow{x: 3, y: 4, w: 950, h: 650})
	got, ok := loadWindowState()
	if want := (model.WindowState{X: 3, Y: 4, W: 950, H: 650}); !ok || got != want {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestSaveWindowGeometryUnlockedGoesToVault(t *testing.T) {
	a := newKeysApp(t)
	a.saveWindowGeometry(&fakeWindow{x: 7, y: 8, w: 1001, h: 701})
	if got := a.store.Settings().Window; got != (model.WindowState{X: 7, Y: 8, W: 1001, H: 701}) {
		t.Fatalf("vault got %+v", got)
	}
	if _, ok := loadWindowState(); ok {
		t.Fatal("window.json must not be written while the vault is open")
	}
	ws, ok := a.startupWindowState()
	if !ok || ws.W != 1001 {
		t.Fatalf("startup state %+v ok=%v", ws, ok)
	}
}
