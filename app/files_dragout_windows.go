package app

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/oledrag"
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var errDragOutStart = errors.New("Не удалось начать перетаскивание в Проводник")

// dragOutRunning keeps one drag out at a time.
var dragOutRunning atomic.Bool

// dragOutTick is how often a watched drag looks at the mouse between drop target events: it ends
// the watch once the button is up, and catches a drag that left before the watch began.
const dragOutTick = 50 * time.Millisecond

type dragPlanned struct {
	items []oledrag.Item
	srcs  []string
	err   error
}

// dragOut watches one row drag of tab t from its start (dragstart in the frontend). The selection
// is walked at once, while the drag goes on, so the system drag is ready when the cursor leaves.
// The leave comes from the window's drop target (dropforward_windows.go): the WebView runs its HTML
// drag as an OLE drag loop on the UI thread, and that loop calls the target's DragLeave when the
// cursor crosses the window's edge. Then, with the button still held outside, it ends the HTML drag
// (Esc) and runs the system drag on the UI thread, which can only start after that loop returned.
func (a *App) dragOut(t *filesTab, paths []string) error {
	if a.mainWin == nil || !dragOutRunning.CompareAndSwap(false, true) {
		return nil
	}
	defer dragOutRunning.Store(false)
	hwnd := uintptr(a.mainWin.NativeWindow())
	if hwnd == 0 {
		return nil
	}
	w := a.drops.watch()
	defer a.drops.unwatch(w)

	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	planCh := make(chan dragPlanned, 1)
	go func() {
		items, srcs, err := t.dragPlanCtx(ctx, paths)
		planCh <- dragPlanned{items, srcs, err}
	}()
	var plan *dragPlanned

	tick := time.NewTicker(dragOutTick)
	defer tick.Stop()
	for {
		ev := dragTick
		select {
		case ev = <-w:
		case <-tick.C:
		case p := <-planCh:
			plan = &p
		case <-t.ctx.Done():
			return nil
		}
		out, end := dragOutStep(ev, oledrag.LeftButtonDown(), oledrag.CursorOutside(hwnd))
		if end {
			return nil
		}
		if !out || plan == nil {
			continue // inside the window, or outside before the walk is done: wait
		}
		if plan.err != nil {
			return plan.err
		}
		return a.startDragOut(hwnd, t, plan)
	}
}

// startDragOut ends the HTML drag and runs the system drag of plan.
func (a *App) startDragOut(hwnd uintptr, t *filesTab, plan *dragPlanned) error {
	if err := oledrag.EndBrowserDrag(hwnd); err != nil {
		return errors.Join(errDragOutStart, err)
	}
	return application.InvokeSyncWithError(func() error {
		_, err := oledrag.Drag(plan.items, &dragSource{t: t, srcs: plan.srcs})
		oledrag.ReleaseButton(hwnd)
		if err != nil {
			return errors.Join(errDragOutStart, err)
		}
		return nil
	})
}
