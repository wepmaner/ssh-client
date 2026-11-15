package oledrag

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Drops on a WebView2 in composition (visual) hosting. Such a WebView has no window of its own, so
// OLE has nowhere to deliver a drag: the host registers an IDropTarget on its window and hands
// every call to ICoreWebView2CompositionController3, which turns them into the HTML drag events.
// That covers files from Explorer and the WebView's own HTML drags, which also run as OLE drags.

var (
	procRegisterDragDrop = ole32.NewProc("RegisterDragDrop")
	procRevokeDragDrop   = ole32.NewProc("RevokeDragDrop")
)

var (
	iidIDropTarget = windows.GUID{Data1: 0x00000122, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	// ICoreWebView2CompositionController3 (WebView2.idl).
	iidCompositionController3 = windows.GUID{Data1: 0x9570570e, Data2: 0x4d76, Data3: 0x4361, Data4: [8]byte{0x9e, 0xe1, 0xf0, 0x4d, 0x0d, 0xbd, 0xfb, 0x1e}}
)

// Vtable slots of ICoreWebView2CompositionController3: IUnknown (3), CompositionController (8:
// RootVisualTarget get/put, SendMouseInput, SendPointerInput, Cursor, SystemCursorId,
// CursorChanged add/remove), CompositionController2 (AutomationProvider), then its own four in
// this order. The same order as the CompositionController4 vtable Wails calls in production.
const (
	ccDragEnter = 12
	ccDragLeave = 13
	ccDragOver  = 14
	ccDrop      = 15
)

// Point is a point in physical pixels.
type Point struct{ X, Y int32 }

// pointOf unpacks a POINTL passed by value: 8 bytes in one register on amd64 and arm64.
func pointOf(v uintptr) Point {
	return Point{X: int32(uint32(v)), Y: int32(uint32(v >> 32))} //nolint:gosec // G115: the two halves of a packed POINT
}

// arg packs p for a by-value POINT argument.
func (p Point) arg() uintptr {
	return uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32 //nolint:gosec // G115: the two halves of a packed POINT
}

// DropHandler gets the IDropTarget calls of a window on its UI thread; pt is in screen pixels.
// Each returns an HRESULT and, but for DragLeave, sets *effect.
type DropHandler interface {
	DragEnter(data uintptr, keys uint32, pt Point, effect *uint32) uintptr
	DragOver(keys uint32, pt Point, effect *uint32) uintptr
	DragLeave() uintptr
	Drop(data uintptr, keys uint32, pt Point, effect *uint32) uintptr
}

type dropTarget struct{ h DropHandler }

// noEffect answers a call that failed or found no handler.
func noEffect(effect *uint32) {
	if effect != nil {
		*effect = dropEffectNone
	}
}

var dropTargetVtbl = []uintptr{
	cbQueryInterface, cbAddRef, cbRelease,
	// DragEnter
	windows.NewCallback(func(this, data, keys, pt uintptr, effect *uint32) (hr uintptr) {
		defer func() {
			if r := recover(); r != nil {
				noEffect(effect)
				hr = eUnexpected
			}
		}()
		t := implOf[*dropTarget](this)
		if t == nil || effect == nil {
			noEffect(effect)
			return eInvalidArg
		}
		return t.h.DragEnter(data, uint32(keys), pointOf(pt), effect) //nolint:gosec // G115: grfKeyState is a DWORD
	}),
	// DragOver
	windows.NewCallback(func(this, keys, pt uintptr, effect *uint32) (hr uintptr) {
		defer func() {
			if r := recover(); r != nil {
				noEffect(effect)
				hr = eUnexpected
			}
		}()
		t := implOf[*dropTarget](this)
		if t == nil || effect == nil {
			noEffect(effect)
			return eInvalidArg
		}
		return t.h.DragOver(uint32(keys), pointOf(pt), effect) //nolint:gosec // G115: grfKeyState is a DWORD
	}),
	// DragLeave
	windows.NewCallback(func(this uintptr) (hr uintptr) {
		defer guard(&hr)
		t := implOf[*dropTarget](this)
		if t == nil {
			return sOK
		}
		return t.h.DragLeave()
	}),
	// Drop
	windows.NewCallback(func(this, data, keys, pt uintptr, effect *uint32) (hr uintptr) {
		defer func() {
			if r := recover(); r != nil {
				noEffect(effect)
				hr = eUnexpected
			}
		}()
		t := implOf[*dropTarget](this)
		if t == nil || effect == nil {
			noEffect(effect)
			return eInvalidArg
		}
		return t.h.Drop(data, uint32(keys), pointOf(pt), effect) //nolint:gosec // G115: grfKeyState is a DWORD
	}),
}

func newDropTarget(h DropHandler) *object {
	return newObject(&dropTarget{h: h}, nil, ifaceDef{vtbl: dropTargetVtbl, iids: []windows.GUID{iidIDropTarget}})
}

// RegisterDropTarget makes h the drop target of hwnd, replacing whatever was registered there. It
// must run on the window's UI thread, which it initializes for OLE. revoke undoes it on that thread.
func RegisterDropTarget(hwnd uintptr, h DropHandler) (revoke func(), err error) {
	if hwnd == 0 || h == nil {
		return nil, errors.New("no window or handler")
	}
	if err := oleInit(); err != nil {
		return nil, err
	}
	_, _, _ = procRevokeDragDrop.Call(hwnd) //nolint:gosec // G104: DRAGDROP_E_NOTREGISTERED when nothing was there
	obj := newDropTarget(h)
	hr, _, _ := procRegisterDragDrop.Call(hwnd, obj.ptr(0)) //nolint:gosec // G104: the HRESULT is the result
	obj.release() // OLE holds its own reference while registered; on failure this ends the object
	if failed(hr) {
		return nil, syscall.Errno(hr)
	}
	done := false
	return func() {
		if !done {
			done = true
			_, _, _ = procRevokeDragDrop.Call(hwnd) //nolint:gosec // G104: the window may be gone already
		}
	}, nil
}

// webViewDrop is the part of ICoreWebView2CompositionController3 a WebView drop target calls;
// points are in the WebView's client pixels.
type webViewDrop interface {
	dragEnter(data uintptr, keys uint32, pt Point, effect *uint32) uintptr
	dragOver(keys uint32, pt Point, effect *uint32) uintptr
	dragLeave() uintptr
	drop(data uintptr, keys uint32, pt Point, effect *uint32) uintptr
}

// controller3 calls an ICoreWebView2CompositionController3 through its vtable.
type controller3 struct{ p *comPtr }

//nolint:gosec // G103: the out parameter of a COM method is a pointer to the caller's DWORD
func (c controller3) dragEnter(data uintptr, keys uint32, pt Point, effect *uint32) uintptr {
	return c.p.call(ccDragEnter, data, uintptr(keys), pt.arg(), uintptr(unsafe.Pointer(effect)))
}

//nolint:gosec // G103: the out parameter of a COM method is a pointer to the caller's DWORD
func (c controller3) dragOver(keys uint32, pt Point, effect *uint32) uintptr {
	return c.p.call(ccDragOver, uintptr(keys), pt.arg(), uintptr(unsafe.Pointer(effect)))
}

func (c controller3) dragLeave() uintptr { return c.p.call(ccDragLeave) }

//nolint:gosec // G103: the out parameter of a COM method is a pointer to the caller's DWORD
func (c controller3) drop(data uintptr, keys uint32, pt Point, effect *uint32) uintptr {
	return c.p.call(ccDrop, data, uintptr(keys), pt.arg(), uintptr(unsafe.Pointer(effect)))
}

// queryController3 is the ICoreWebView2CompositionController3 of a composition controller, with
// its own reference.
//
//nolint:gosec // G103: QueryInterface takes pointers to the IID and to the out pointer
func queryController3(cc unsafe.Pointer) (*comPtr, error) {
	if cc == nil {
		return nil, errors.New("no composition controller")
	}
	var out *comPtr
	hr := (*comPtr)(cc).call(0, uintptr(unsafe.Pointer(&iidCompositionController3)), uintptr(unsafe.Pointer(&out)))
	if failed(hr) {
		return nil, syscall.Errno(hr)
	}
	if out == nil {
		return nil, errors.New("no ICoreWebView2CompositionController3")
	}
	return out, nil
}

// WebView is the drop target of a window that hosts a WebView2 by composition: it hands each call
// to the WebView in its client coordinates. A drag of this package's own Drag (files going out to
// Explorer) is refused and never reaches the WebView, so a drag brought back over the window drops
// nothing. Use it only on the window's UI thread.
type WebView struct {
	hwnd     uintptr
	wv       webViewDrop
	release  func()
	toClient func(hwnd uintptr, p Point) Point
	busy     func() bool // a drag of Drag runs
	active   bool        // the current drag was handed to the WebView

	// OnLeave runs after a handed drag left the window or was cancelled over it, OnDrop after it
	// was dropped on it. Both run on the UI thread inside the drag loop and must not block.
	OnLeave func()
	OnDrop  func()
}

// NewWebView makes the drop target that hands drops on hwnd to the WebView2 whose
// ICoreWebView2CompositionController is cc. Close releases it.
func NewWebView(hwnd uintptr, cc unsafe.Pointer) (*WebView, error) {
	p, err := queryController3(cc)
	if err != nil {
		return nil, err
	}
	return newWebView(hwnd, controller3{p}, p.release), nil
}

func newWebView(hwnd uintptr, wv webViewDrop, release func()) *WebView {
	return &WebView{hwnd: hwnd, wv: wv, release: release, toClient: screenToClient, busy: running.Load}
}

// Close releases the controller; the target must be revoked first.
func (w *WebView) Close() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
}

// screenToClient is p in the client pixels of hwnd. The process is per-monitor DPI aware, so both
// are physical pixels, as WebView2 expects them in composition hosting.
func screenToClient(hwnd uintptr, p Point) Point {
	_, _, _ = procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p))) //nolint:gosec // G103: ScreenToClient converts the point in place
	return p
}

func (w *WebView) DragEnter(data uintptr, keys uint32, pt Point, effect *uint32) uintptr {
	w.active = !w.busy()
	if !w.active {
		*effect = dropEffectNone
		return sOK
	}
	return w.wv.dragEnter(data, keys, w.toClient(w.hwnd, pt), effect)
}

func (w *WebView) DragOver(keys uint32, pt Point, effect *uint32) uintptr {
	if !w.active {
		*effect = dropEffectNone
		return sOK
	}
	return w.wv.dragOver(keys, w.toClient(w.hwnd, pt), effect)
}

func (w *WebView) DragLeave() uintptr {
	if !w.active {
		return sOK
	}
	w.active = false
	hr := w.wv.dragLeave()
	if w.OnLeave != nil {
		w.OnLeave()
	}
	return hr
}

func (w *WebView) Drop(data uintptr, keys uint32, pt Point, effect *uint32) uintptr {
	if !w.active {
		*effect = dropEffectNone
		return sOK
	}
	w.active = false
	hr := w.wv.drop(data, keys, w.toClient(w.hwnd, pt), effect)
	if w.OnDrop != nil {
		w.OnDrop()
	}
	return hr
}
