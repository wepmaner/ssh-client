package app

import (
	"errors"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/oledrag"
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errDragOutStart = errors.New("Не удалось начать перетаскивание в Проводник")

// dragOutRunning keeps one drag out at a time: the frontend may report the same leave twice.
var dragOutRunning atomic.Bool

// dragOut walks the selection while the HTML drag of the rows still runs, then, if the button is
// still held outside the window, ends that drag (Esc) and runs the system drag on the UI thread.
// The WebView runs its HTML drag as an OLE drag loop on the same thread, so the system drag can
// only start after that loop has returned.
func (a *App) dragOut(t *filesTab, paths []string) error {
	if a.mainWin == nil || !dragOutRunning.CompareAndSwap(false, true) {
		return nil
	}
	defer dragOutRunning.Store(false)
	hwnd := uintptr(a.mainWin.NativeWindow())
	if hwnd == 0 {
		return nil
	}
	items, srcs, err := t.dragPlan(paths)
	if err != nil {
		return err
	}
	if !oledrag.LeftButtonDown() || !oledrag.CursorOutside(hwnd) {
		return nil // dropped meanwhile, or back over the window: the HTML drag goes on
	}
	if err := oledrag.EndBrowserDrag(hwnd); err != nil {
		return errors.Join(errDragOutStart, err)
	}
	return application.InvokeSyncWithError(func() error {
		_, err := oledrag.Drag(items, &dragSource{t: t, srcs: srcs})
		oledrag.ReleaseButton(hwnd)
		if err != nil {
			return errors.Join(errDragOutStart, err)
		}
		return nil
	})
}
