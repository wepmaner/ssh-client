package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/model"
)

func TestWindowStateRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
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
	t.Setenv("APPDATA", root)
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

func TestSaveWindowStateNoAppData(t *testing.T) {
	t.Setenv("APPDATA", "")
	if err := saveWindowState(model.WindowState{W: 1}); err == nil {
		t.Fatal("expected error")
	}
}

// fakeWindow records geometry calls in place of the Wails window.
type fakeWindow struct {
	x, y, w, h int
	maximised  bool
	minimised  bool
}

func (f *fakeWindow) Position() (int, int) { return f.x, f.y }
func (f *fakeWindow) Size() (int, int)     { return f.w, f.h }
func (f *fakeWindow) IsMaximised() bool    { return f.maximised }
func (f *fakeWindow) IsMinimised() bool    { return f.minimised }

// fakeScreens are monitors with the given work areas.
type fakeScreens []application.Rect

func (f fakeScreens) GetAll() []*application.Screen {
	out := make([]*application.Screen, 0, len(f))
	for _, r := range f {
		out = append(out, &application.Screen{WorkArea: r, Bounds: r})
	}
	return out
}

// twoMonitors: a 1920x1080 primary and a 1280x1024 monitor on its left.
var twoMonitors = fakeScreens{{X: 0, Y: 0, Width: 1920, Height: 1040}, {X: -1280, Y: 0, Width: 1280, Height: 984}}

func TestMainWindowOptionsWithoutStateKeepsDefaults(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800, Title: "sshclient"}
	got := mainWindowOptions(base, model.WindowState{}, false, twoMonitors)
	if got.Width != 1280 || got.Height != 800 || got.InitialPosition != application.WindowCentered ||
		got.StartState != application.WindowStateNormal || got.Title != "sshclient" {
		t.Fatalf("got %+v", got)
	}
	if got := mainWindowOptions(base, model.WindowState{X: 5, W: 0}, true, twoMonitors); got.InitialPosition != application.WindowCentered {
		t.Fatal("zero width must not be restored")
	}
}

func TestMainWindowOptionsOffScreenIsCentred(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800}
	for name, ws := range map[string]model.WindowState{
		"minimised":            {X: -32000, Y: -32000, W: 1000, H: 700},
		"disconnected monitor": {X: 2200, Y: 100, W: 1000, H: 700},
		"above every monitor":  {X: 100, Y: -900, W: 1000, H: 700},
	} {
		got := mainWindowOptions(base, ws, true, twoMonitors)
		if got.InitialPosition != application.WindowCentered || got.X != 0 || got.Y != 0 {
			t.Errorf("%s: got %+v", name, got)
		}
		// The size is still the user's.
		if got.Width != 1000 || got.Height != 700 {
			t.Errorf("%s: size %dx%d", name, got.Width, got.Height)
		}
	}
	got := mainWindowOptions(base, model.WindowState{X: 2200, Y: 100, W: 1000, H: 700, Maximized: true}, true, twoMonitors)
	if got.InitialPosition != application.WindowCentered || got.StartState != application.WindowStateMaximised {
		t.Fatalf("maximised off screen: got %+v", got)
	}
}

func TestMainWindowOptionsOnScreenIsKept(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800}
	// On the left monitor, and half off the right edge of the primary one.
	for _, ws := range []model.WindowState{{X: -1200, Y: 50, W: 1000, H: 700}, {X: 1500, Y: 300, W: 1000, H: 700}} {
		got := mainWindowOptions(base, ws, true, twoMonitors)
		if got.InitialPosition != application.WindowXY || got.X != ws.X || got.Y != ws.Y {
			t.Errorf("%+v: got %+v", ws, got)
		}
	}
	// No monitor list: nothing to check against, the saved place is used.
	got := mainWindowOptions(base, model.WindowState{X: 2200, Y: 100, W: 1000, H: 700}, true, fakeScreens{})
	if got.InitialPosition != application.WindowXY || got.X != 2200 {
		t.Fatalf("no screens: got %+v", got)
	}
}

func TestMainWindowOptionsRestoresGeometryBeforeShow(t *testing.T) {
	base := application.WebviewWindowOptions{Width: 1280, Height: 800, MinWidth: 900}
	got := mainWindowOptions(base, model.WindowState{X: 100, Y: -20, W: 1000, H: 700}, true, twoMonitors)
	if got.X != 100 || got.Y != -20 || got.Width != 1000 || got.Height != 700 ||
		got.InitialPosition != application.WindowXY || got.StartState != application.WindowStateNormal {
		t.Fatalf("got %+v", got)
	}
	if got.MinWidth != 900 {
		t.Fatal("base options lost")
	}
	got = mainWindowOptions(base, model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}, true, twoMonitors)
	if got.StartState != application.WindowStateMaximised || got.Width != 1000 || got.X != 1 {
		t.Fatalf("maximised: got %+v", got)
	}
}

func TestCaptureWindowState(t *testing.T) {
	none := func() (model.WindowState, bool) { return model.WindowState{}, false }
	got, ok := captureWindowState(&fakeWindow{x: 10, y: 20, w: 1100, h: 750}, none)
	if want := (model.WindowState{X: 10, Y: 20, W: 1100, H: 750}); !ok || got != want {
		t.Fatalf("normal: got %+v ok=%v", got, ok)
	}

	// Maximised keeps the previous un-maximised geometry to restore into.
	prev := func() (model.WindowState, bool) { return model.WindowState{X: 1, Y: 2, W: 1000, H: 700}, true }
	got, ok = captureWindowState(&fakeWindow{w: 2560, h: 1400, maximised: true}, prev)
	if want := (model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}); !ok || got != want {
		t.Fatalf("maximised: got %+v", got)
	}
	got, ok = captureWindowState(&fakeWindow{w: 2560, h: 1400, maximised: true}, none)
	want := model.DefaultSettings().Window
	want.Maximized = true
	if !ok || got != want {
		t.Fatalf("maximised without previous: got %+v", got)
	}
}

func TestCaptureWindowStateMinimisedKeepsPrevious(t *testing.T) {
	prevMax := func() (model.WindowState, bool) {
		return model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}, true
	}
	// Windows reports a minimised window at (-32000, -32000) with a tiny size.
	for _, w := range []*fakeWindow{
		{x: -32000, y: -32000, w: 160, h: 28, minimised: true},
		{x: -32000, y: -32000, w: 160, h: 28}, // the position alone gives it away
	} {
		got, ok := captureWindowState(w, prevMax)
		if want := (model.WindowState{X: 1, Y: 2, W: 1000, H: 700, Maximized: true}); !ok || got != want {
			t.Errorf("%+v: got %+v ok=%v", w, got, ok)
		}
	}
	none := func() (model.WindowState, bool) { return model.WindowState{}, false }
	if got, ok := captureWindowState(&fakeWindow{x: -32000, y: -32000, minimised: true}, none); ok {
		t.Fatalf("minimised without previous must save nothing, got %+v", got)
	}
}

func TestSaveWindowGeometryMinimisedKeepsFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	want := model.WindowState{X: 3, Y: 4, W: 950, H: 650}
	if err := saveWindowState(want); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.saveWindowGeometry(&fakeWindow{x: -32000, y: -32000, w: 160, h: 28, minimised: true})
	if got, ok := loadWindowState(); !ok || got != want {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestSaveWindowGeometryLockedGoesToFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
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
	if got := a.storeRef().Settings().Window; got != (model.WindowState{X: 7, Y: 8, W: 1001, H: 701}) {
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
