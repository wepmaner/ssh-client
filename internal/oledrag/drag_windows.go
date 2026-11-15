package oledrag

import (
	"errors"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procSendInput                = user32.NewProc("SendInput")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procScreenToClient           = user32.NewProc("ScreenToClient")
)

const (
	vkEscape       = 0x1B
	inputKeyboard  = 1
	keyeventfKeyUp = 0x0002
	wmLButtonUp    = 0x0202
)

// ErrBusy is a drag started while another one runs.
var ErrBusy = errors.New(errDragAlreadyRun)

var running atomic.Bool

// dropSource is the IDropSource of a drag.
type dropSource struct{ d Decider }

var dropSourceVtbl = []uintptr{
	cbQueryInterface, cbAddRef, cbRelease,
	// QueryContinueDrag
	windows.NewCallback(func(this, escape, keys uintptr) (hr uintptr) {
		defer guard(&hr)
		s := implOf[*dropSource](this)
		if s == nil {
			return dragdropSCancel
		}
		return uintptr(s.d.Next(escape != 0, uint32(keys))) //nolint:gosec // G115: grfKeyState is a DWORD
	}),
	// GiveFeedback
	windows.NewCallback(func(this, effect uintptr) uintptr { return dragdropSUseDefault }),
}

type point struct{ x, y int32 }

type rect struct{ left, top, right, bottom int32 }

func cursorPos() (point, bool) {
	var p point
	ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))) //nolint:gosec // G103: GetCursorPos writes the point
	return p, ok != 0
}

// LeftButtonDown reports whether the primary mouse button is held now.
func LeftButtonDown() bool { return leftButtonDown() }

// CursorOutside reports whether the cursor is outside the window hwnd.
func CursorOutside(hwnd uintptr) bool {
	p, ok := cursorPos()
	if !ok {
		return false
	}
	var r rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 { //nolint:gosec // G103: GetWindowRect writes the rect
		return false
	}
	return p.x < r.left || p.x >= r.right || p.y < r.top || p.y >= r.bottom
}

// guiThreadInfo is GUITHREADINFO.
type guiThreadInfo struct {
	size                                               uint32
	flags                                              uint32
	active, focus, capture, menuOwner, moveSize, caret uintptr
	caretRect                                          rect
}

// captureOf is the window that holds the mouse capture in the thread of hwnd, 0 for none.
func captureOf(hwnd uintptr) uintptr {
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0) //nolint:gosec // G104: the thread id is the result, 0 on failure
	if tid == 0 {
		return 0
	}
	gi := guiThreadInfo{size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gi))); ok == 0 { //nolint:gosec // G103: GetGUIThreadInfo fills the struct
		return 0
	}
	return gi.capture
}

// keyInput is INPUT with a KEYBDINPUT (40 bytes on 64-bit Windows).
type keyInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte
}

// EndBrowserDrag ends the HTML drag the WebView runs on the thread of hwnd: it presses Esc, which
// the drag loop takes as cancel, and waits until no other window of that thread holds the mouse
// capture (OLE's drag loop holds it on its own window). It fails if the loop is still running
// after a second, so a native drag never starts inside it.
func EndBrowserDrag(hwnd uintptr) error {
	in := [2]keyInput{
		{typ: inputKeyboard, vk: vkEscape},
		{typ: inputKeyboard, vk: vkEscape, flags: keyeventfKeyUp},
	}
	n, _, err := procSendInput.Call(2, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0])) //nolint:gosec // G103: SendInput reads the array
	if n != 2 {
		return err
	}
	const step = 10 * time.Millisecond
	free := 0
	for waited := time.Duration(0); waited < time.Second; waited += step {
		time.Sleep(step)
		c := captureOf(hwnd)
		if c == 0 || c == hwnd {
			free++
		} else {
			free = 0
		}
		if free >= 3 && waited >= 40*time.Millisecond {
			return nil
		}
	}
	return errors.New("the HTML drag did not end")
}

// ReleaseButton tells the window that the left button went up at the cursor, when the cursor is
// outside it: the drag loop took the real button-up, and the WebView would think it still held.
func ReleaseButton(hwnd uintptr) {
	if !CursorOutside(hwnd) {
		return
	}
	p, ok := cursorPos()
	if !ok {
		return
	}
	_, _, _ = procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p))) //nolint:gosec // G103: ScreenToClient converts the point in place
	lp := uintptr(uint16(p.x)) | uintptr(uint16(p.y))<<16                //nolint:gosec // G115: MAKELPARAM of signed client coordinates
	_, _, _ = procPostMessageW.Call(hwnd, wmLButtonUp, 0, lp)            //nolint:gosec // G104: a lost reset leaves the old state
}

var ole struct {
	done bool
	err  error
}

// oleInit initializes OLE on the calling thread once (the window's UI thread); it stays initialized.
func oleInit() error {
	if ole.done {
		return ole.err
	}
	hr, _, _ := procOleInitialize.Call(0) //nolint:gosec // G104: the HRESULT is the result
	ole.done = true
	if failed(hr) && hr != rpcEChangedMode {
		ole.err = syscall.Errno(hr)
	}
	return ole.err
}

// Drag runs the drag of items out of the window and returns when the drop or cancel happened; a
// drop target that copies asynchronously (Explorer) goes on reading the streams afterwards. It
// must run on the window's UI thread. src is released when nothing holds the data any more, also
// when Drag fails.
//
// The data object lives in the process's multithreaded apartment: the drop target's calls are
// served on COM's threads, not on the UI thread, so the window stays responsive while files copy.
//
//nolint:gosec // G103: COM calls pass pointers to the GUIDs, the out pointers and the stream
func Drag(items []Item, src Source) (effect uint32, err error) {
	if !running.CompareAndSwap(false, true) {
		src.Release()
		return 0, ErrBusy
	}
	defer running.Store(false)
	desc, err := Descriptor(items)
	if err != nil {
		src.Release()
		return 0, err
	}
	if err := oleInit(); err != nil {
		src.Release()
		return 0, err
	}
	if !leftButtonDown() {
		src.Release()
		return dropEffectNone, nil
	}
	initFormats()
	h := &holder{src: src}
	h.add() // the data object
	var stm uintptr
	var mhr uintptr
	if err := onMTA(func() {
		obj := newDataObject(items, desc, h)
		mhr, _, _ = procCoMarshalInterThreadInterfaceInStream.Call(
			uintptr(unsafe.Pointer(&iidIDataObject)), obj.ptr(0), uintptr(unsafe.Pointer(&stm)))
		obj.release() // the marshaled stream holds its own reference; on failure this ends the object
	}); err != nil {
		src.Release()
		return 0, err
	}
	if failed(mhr) {
		return 0, syscall.Errno(mhr)
	}
	var pdo *comPtr
	if hr, _, _ := procCoGetInterfaceAndReleaseStream.Call(stm, uintptr(unsafe.Pointer(&iidIDataObject)), uintptr(unsafe.Pointer(&pdo))); failed(hr) {
		return 0, syscall.Errno(hr)
	}
	defer pdo.release()
	ds := newObject(&dropSource{}, nil, ifaceDef{vtbl: dropSourceVtbl, iids: []windows.GUID{iidIDropSource}})
	defer ds.release()
	var eff uint32
	hr, _, _ := procDoDragDrop.Call(uintptr(unsafe.Pointer(pdo)), ds.ptr(0), dropEffectCopy, uintptr(unsafe.Pointer(&eff)))
	if failed(hr) {
		return 0, syscall.Errno(hr)
	}
	if hr != dragdropSDrop {
		return dropEffectNone, nil
	}
	return eff, nil
}
