package oledrag

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPointPacking(t *testing.T) {
	for _, p := range []Point{{0, 0}, {10, 20}, {-5, 7}, {-32000, -32000}, {1 << 30, -(1 << 30)}} {
		if got := pointOf(p.arg()); got != p {
			t.Fatalf("round trip %v -> %v", p, got)
		}
	}
	// POINTL {x, y} in memory is x then y: the low half of the register is x.
	if got := pointOf(0xFFFFFFFE_00000003); got != (Point{3, -2}) {
		t.Fatalf("pointOf %v", got)
	}
}

// fakeCC records the calls of the CompositionController3 test object.
var fakeCC struct {
	calls  []string
	data   uintptr
	keys   uintptr
	pt     Point
	result uint32 // the effect it answers
}

//nolint:gosec // G103: the fake writes the effect through the pointer COM passes
var fakeCC3Vtbl = func() []uintptr {
	v := make([]uintptr, 16)
	v[0], v[1], v[2] = cbQueryInterface, cbAddRef, cbRelease
	v[ccDragEnter] = windows.NewCallback(func(this, data, keys, pt uintptr, effect *uint32) uintptr {
		fakeCC.calls = append(fakeCC.calls, "enter")
		fakeCC.data, fakeCC.keys, fakeCC.pt = data, keys, pointOf(pt)
		*effect = fakeCC.result
		return sOK
	})
	v[ccDragLeave] = windows.NewCallback(func(this uintptr) uintptr {
		fakeCC.calls = append(fakeCC.calls, "leave")
		return sOK
	})
	v[ccDragOver] = windows.NewCallback(func(this, keys, pt uintptr, effect *uint32) uintptr {
		fakeCC.calls = append(fakeCC.calls, "over")
		fakeCC.keys, fakeCC.pt = keys, pointOf(pt)
		*effect = fakeCC.result
		return sOK
	})
	v[ccDrop] = windows.NewCallback(func(this, data, keys, pt uintptr, effect *uint32) uintptr {
		fakeCC.calls = append(fakeCC.calls, "drop")
		fakeCC.data, fakeCC.keys, fakeCC.pt = data, keys, pointOf(pt)
		*effect = fakeCC.result
		return sOK
	})
	return v
}()

// asPointer is the interface pointer p as unsafe.Pointer, as the app gets it from Wails.
func asPointer(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) } //nolint:gosec // G103: a COM pointer kept alive by reg

//nolint:gosec // G103: the test passes pointers to its effect to the vtable methods
func TestWebViewForwardsToController3(t *testing.T) {
	fakeCC.calls = nil
	fakeCC.result = 2 // DROPEFFECT_MOVE
	// The controller answers QueryInterface for Controller3 only; its base interface is the same
	// object here, which is all NewWebView needs.
	cc := newObject(struct{}{}, nil, ifaceDef{vtbl: fakeCC3Vtbl, iids: []windows.GUID{iidCompositionController3}})
	defer cc.release()

	w, err := NewWebView(42, asPointer(cc.ptr(0)))
	if err != nil {
		t.Fatal(err)
	}
	if n := cc.refs.Load(); n != 2 {
		t.Fatalf("refs after QI %d, want 2", n)
	}
	var gotHwnd uintptr
	w.toClient = func(hwnd uintptr, p Point) Point { gotHwnd = hwnd; return Point{p.X - 100, p.Y - 50} }
	busy := false
	w.busy = func() bool { return busy }
	leaves, drops := 0, 0
	w.OnLeave = func() { leaves++ }
	w.OnDrop = func() { drops++ }

	target := newDropTarget(w)
	defer target.release()
	pt := target.ptr(0)

	// The effect lives on the heap, as OLE's does: a callback may move the test's stack.
	effBox = new(uint32)
	eff := effBox
	*eff = 99
	if hr := vcall(dropTargetVtbl, pt, 3, 0x1234, mkLButton, Point{150, 80}.arg(), uintptr(unsafe.Pointer(eff))); hr != sOK {
		t.Fatalf("DragEnter %#x", hr)
	}
	if *eff != 2 || fakeCC.data != 0x1234 || fakeCC.keys != mkLButton || fakeCC.pt != (Point{50, 30}) || gotHwnd != 42 {
		t.Fatalf("enter eff %d data %#x keys %d pt %v hwnd %d", *eff, fakeCC.data, fakeCC.keys, fakeCC.pt, gotHwnd)
	}
	*eff = 99
	if hr := vcall(dropTargetVtbl, pt, 4, mkLButton|8, Point{90, 40}.arg(), uintptr(unsafe.Pointer(eff))); hr != sOK || *eff != 2 || fakeCC.pt != (Point{-10, -10}) || fakeCC.keys != mkLButton|8 {
		t.Fatalf("DragOver %#x eff %d pt %v", hr, *eff, fakeCC.pt)
	}
	vcall(dropTargetVtbl, pt, 5)
	if leaves != 1 {
		t.Fatalf("leaves %d", leaves)
	}
	vcall(dropTargetVtbl, pt, 5) // a second leave with no drag is not handed on
	vcall(dropTargetVtbl, pt, 3, 0x99, mkLButton, Point{100, 50}.arg(), uintptr(unsafe.Pointer(eff)))
	*eff = 99
	vcall(dropTargetVtbl, pt, 6, 0x99, 0, Point{110, 60}.arg(), uintptr(unsafe.Pointer(eff)))
	if *eff != 2 || drops != 1 || leaves != 1 || fakeCC.data != 0x99 || fakeCC.pt != (Point{10, 10}) {
		t.Fatalf("Drop eff %d drops %d leaves %d data %#x pt %v", *eff, drops, leaves, fakeCC.data, fakeCC.pt)
	}
	want := []string{"enter", "over", "leave", "enter", "drop"}
	if len(fakeCC.calls) != len(want) {
		t.Fatalf("calls %v, want %v", fakeCC.calls, want)
	}
	for i := range want {
		if fakeCC.calls[i] != want[i] {
			t.Fatalf("calls %v, want %v", fakeCC.calls, want)
		}
	}

	// A drag of our own Drag is refused and never reaches the WebView.
	busy = true
	fakeCC.calls = nil
	*eff = 99
	vcall(dropTargetVtbl, pt, 3, 0x5, mkLButton, Point{150, 80}.arg(), uintptr(unsafe.Pointer(eff)))
	if *eff != dropEffectNone {
		t.Fatalf("own drag effect %d", *eff)
	}
	*eff = 99
	vcall(dropTargetVtbl, pt, 4, mkLButton, Point{150, 80}.arg(), uintptr(unsafe.Pointer(eff)))
	vcall(dropTargetVtbl, pt, 5)
	vcall(dropTargetVtbl, pt, 6, 0x5, 0, Point{150, 80}.arg(), uintptr(unsafe.Pointer(eff)))
	if len(fakeCC.calls) != 0 || *eff != dropEffectNone || leaves != 1 || drops != 1 {
		t.Fatalf("own drag reached the WebView: %v eff %d", fakeCC.calls, *eff)
	}

	// A null effect pointer is refused before the handler.
	if hr := vcall(dropTargetVtbl, pt, 3, 0x5, 0, 0, 0); hr != eInvalidArg {
		t.Fatalf("null effect %#x", hr)
	}

	w.Close()
	if n := cc.refs.Load(); n != 1 {
		t.Fatalf("refs after Close %d, want 1", n)
	}
}

func TestNewWebViewWithoutController3(t *testing.T) {
	if _, err := NewWebView(1, nil); err == nil {
		t.Fatal("nil controller accepted")
	}
	other := newObject(struct{}{}, nil, ifaceDef{vtbl: dropSourceVtbl, iids: []windows.GUID{iidIDropSource}})
	defer other.release()
	if _, err := NewWebView(1, asPointer(other.ptr(0))); err == nil {
		t.Fatal("an object without Controller3 accepted")
	}
}

var (
	procCreateWindowExW = user32.NewProc("CreateWindowExW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
)

// effBox keeps the test's effect on the heap.
var effBox *uint32

type nopDrop struct{}

func (nopDrop) DragEnter(uintptr, uint32, Point, *uint32) uintptr { return sOK }
func (nopDrop) DragOver(uint32, Point, *uint32) uintptr           { return sOK }
func (nopDrop) DragLeave() uintptr                                { return sOK }
func (nopDrop) Drop(uintptr, uint32, Point, *uint32) uintptr      { return sOK }

// TestRegisterDropTarget registers on a hidden window of an OLE thread and revokes it.
//
//nolint:gosec // G103: CreateWindowExW reads the class name
func TestRegisterDropTarget(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procOleInitialize.Call(0); failed(hr) {
		t.Skipf("OleInitialize %#x", hr)
	}
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 10, 10, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowEx: %v", err)
	}
	defer func() { _, _, _ = procDestroyWindow.Call(hwnd) }()
	before := len(reg)
	revoke, err := RegisterDropTarget(hwnd, nopDrop{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(reg) != before+1 {
		t.Fatalf("the target is not alive while registered")
	}
	// Registering again replaces the first target, which OLE then releases.
	revoke2, err := RegisterDropTarget(hwnd, nopDrop{})
	if err != nil {
		t.Fatalf("register again: %v", err)
	}
	if len(reg) != before+1 {
		t.Fatalf("the replaced target was not released: %d", len(reg)-before)
	}
	revoke2()
	revoke2()
	revoke()
	if len(reg) != before {
		t.Fatalf("the target outlived the revoke")
	}
	if _, err := RegisterDropTarget(0, nopDrop{}); err == nil {
		t.Fatal("no window accepted")
	}
}
