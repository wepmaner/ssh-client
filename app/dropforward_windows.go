package app

import (
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"sshclient/internal/oledrag"
)

// Drag and drop for the WebView in composition hosting.
//
// main.go hosts WebView2 by composition (WebView2CompositionHosting, needed for the Snap layouts of
// the maximise button). Such a WebView has no window of its own, and OLE delivers a drag only to a
// window: the host must register an IDropTarget on its window and pass every call to the WebView's
// ICoreWebView2CompositionController3. Wails v3 beta.25 does not, so without this nothing could be
// dropped: neither files from Explorer nor the rows dragged inside the file list (an HTML drag runs
// as an OLE drag too). The target also tells a watched row drag (files_dragout_windows.go) when it
// left the window.
//
// Wails keeps the controller in unexported fields. The Wails version is pinned in go.mod, so the
// path is read by reflection, each step checked by name and type; TestWailsDropFieldPath checks
// the same path in the Wails sources, so an upgrade that moves it fails the test, not the users.

// compositionControllerPath leads from *application.WebviewWindow to Wails' controller pointer.
var compositionControllerPath = []fieldStep{
	{"impl", "*application.windowsWebviewWindow"},
	{"chromium", "*edge.Chromium"},
	{"compositionController", "*edge.ICoreWebView2CompositionController"},
}

// compositionEnabledPath is the flag Wails clears when it fell back to a windowed WebView.
var compositionEnabledPath = []fieldStep{
	{"impl", "*application.windowsWebviewWindow"},
	{"chromium", "*edge.Chromium"},
	{"CompositionControllerEnabled", "bool"},
}

const (
	dropRegisterTries = 20
	dropRegisterRetry = 100 * time.Millisecond
)

// dropForward is the drop target of the main window and the row drag it reports to.
type dropForward struct {
	// UI thread only.
	wv     *oledrag.WebView
	revoke func()
	done   bool // registered, or found that it cannot be
	tries  int

	mu      sync.Mutex
	watcher chan dragEvent // the watched row drag, nil for none
}

// watch starts reporting the drop target's events to a new row drag watch.
func (d *dropForward) watch() chan dragEvent {
	ch := make(chan dragEvent, 4)
	d.mu.Lock()
	d.watcher = ch
	d.mu.Unlock()
	return ch
}

func (d *dropForward) unwatch(ch chan dragEvent) {
	d.mu.Lock()
	if d.watcher == ch {
		d.watcher = nil
	}
	d.mu.Unlock()
}

// notify reports ev to the watched drag; it never blocks the drag loop.
func (d *dropForward) notify(ev dragEvent) {
	d.mu.Lock()
	ch := d.watcher
	d.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

// setupDrops registers the drop target once the WebView is up, and revokes it as the window closes.
func (a *App) setupDrops() {
	win := a.mainWin
	// NavigationCompleted comes after the controller was created, and again on each reload.
	win.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		application.InvokeSync(func() { a.registerDrops(win) })
	})
	win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		// Posted ahead of the window's destruction, which Wails also runs on the UI thread.
		application.InvokeAsync(a.revokeDrops)
	})
}

// registerDrops makes the main window's drop target, on the UI thread.
func (a *App) registerDrops(win *application.WebviewWindow) {
	d := &a.drops
	if d.done {
		return
	}
	hwnd := uintptr(win.NativeWindow())
	if hwnd == 0 {
		return
	}
	cc, err := walkFields(reflect.ValueOf(win), compositionControllerPath)
	if err != nil {
		d.done = true
		log.Printf("drop target: %v", err)
		return
	}
	if cc.IsNil() {
		if on, err := walkFields(reflect.ValueOf(win), compositionEnabledPath); err == nil && !on.Bool() {
			d.done = true // a windowed WebView takes drops by itself
			return
		}
		if d.tries++; d.tries < dropRegisterTries {
			time.AfterFunc(dropRegisterRetry, func() { application.InvokeAsync(func() { a.registerDrops(win) }) })
		} else {
			log.Printf("drop target: the WebView2 composition controller is not ready")
		}
		return
	}
	wv, err := oledrag.NewWebView(hwnd, cc.UnsafePointer())
	if err != nil {
		d.done = true
		log.Printf("drop target: %v", err)
		return
	}
	wv.OnLeave = func() { d.notify(dragLeft) }
	wv.OnDrop = func() { d.notify(dragDropped) }
	revoke, err := oledrag.RegisterDropTarget(hwnd, wv)
	if err != nil {
		wv.Close()
		d.done = true
		log.Printf("drop target: %v", err)
		return
	}
	d.wv, d.revoke, d.done = wv, revoke, true
}

// revokeDrops takes the drop target off the main window, on the UI thread.
func (a *App) revokeDrops() {
	d := &a.drops
	if d.revoke != nil {
		d.revoke()
		d.revoke = nil
	}
	if d.wv != nil {
		d.wv.Close()
		d.wv = nil
	}
}
