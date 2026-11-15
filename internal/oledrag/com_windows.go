package oledrag

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The vtables pass 64-bit values (ULARGE_INTEGER, LARGE_INTEGER) as one uintptr argument: that
// holds on amd64 and arm64 only. This fails to compile on a 32-bit build.
var _ [unsafe.Sizeof(uintptr(0)) - 8]byte

const (
	eNotImpl          = 0x80004001
	eNoInterface      = 0x80004002
	ePointer          = 0x80004003
	eUnexpected       = 0x8000FFFF
	eInvalidArg       = 0x80070057
	eOutOfMemory      = 0x8007000E
	rpcEChangedMode   = 0x80010106
	sFalse            = 1
	coinitMulti       = 0x0
	gmemMoveable      = 0x0002
	statflagNoName    = 1
	stgtyStream       = 2
	smSwapButton      = 23
	vkLButton         = 0x01
	vkRButton         = 0x02
	dropEffectCopy    = 1
	dropEffectNone    = 0
	errDragAlreadyRun = "drag already running"
)

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procCoInitializeEx                        = ole32.NewProc("CoInitializeEx")
	procOleInitialize                         = ole32.NewProc("OleInitialize")
	procDoDragDrop                            = ole32.NewProc("DoDragDrop")
	procCoMarshalInterThreadInterfaceInStream = ole32.NewProc("CoMarshalInterThreadInterfaceInStream")
	procCoGetInterfaceAndReleaseStream        = ole32.NewProc("CoGetInterfaceAndReleaseStream")
	procReleaseStgMedium                      = ole32.NewProc("ReleaseStgMedium")
	procCoTaskMemAlloc                        = ole32.NewProc("CoTaskMemAlloc")

	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalFree    = kernel32.NewProc("GlobalFree")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procGlobalSize    = kernel32.NewProc("GlobalSize")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")

	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")
	procGetAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")

	procSHCreateStdEnumFmtEtc = shell32.NewProc("SHCreateStdEnumFmtEtc")
)

var (
	iidIUnknown          = windows.GUID{Data1: 0x00000000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDataObject       = windows.GUID{Data1: 0x0000010E, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIStream           = windows.GUID{Data1: 0x0000000C, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropSource       = windows.GUID{Data1: 0x00000121, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidISequentialStream = windows.GUID{Data1: 0x0C733A30, Data2: 0x2A1C, Data3: 0x11CE, Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D}}
	// IDataObjectAsyncCapability (IAsyncOperation): Explorer copies on its own thread.
	iidIAsyncCapability = windows.GUID{Data1: 0x3D8B0590, Data2: 0xF691, Data3: 0x11D2, Data4: [8]byte{0x8E, 0xA9, 0x00, 0x60, 0x97, 0xDF, 0x5B, 0xD4}}
)

// iface is one interface pointer of an exported object: COM reads vtbl through the pointer. The
// value lives in reg while the object is alive, so the collector keeps it (Go never moves heap
// values).
type iface struct {
	vtbl uintptr // must stay first
	obj  *object
	iids []windows.GUID
}

// object is a Go value exported to COM with one or more interfaces; the first one is its IUnknown.
type object struct {
	refs   atomic.Int32
	ifaces []*iface
	impl   any
	final  func() // after the last Release
}

type ifaceDef struct {
	vtbl []uintptr
	iids []windows.GUID
}

var (
	regMu sync.RWMutex
	reg   = map[uintptr]*iface{}
)

// newObject exports impl with the interfaces defs, with one reference for the caller.
func newObject(impl any, final func(), defs ...ifaceDef) *object {
	o := &object{impl: impl, final: final}
	o.refs.Store(1)
	regMu.Lock()
	defer regMu.Unlock()
	for _, d := range defs {
		f := &iface{vtbl: vtblAddr(d.vtbl), obj: o, iids: d.iids}
		o.ifaces = append(o.ifaces, f)
		reg[ifacePtr(f)] = f
	}
	return o
}

// ifacePtr is the interface pointer COM gets for f.
//
//nolint:gosec // G103: an exported COM interface pointer is the address of its Go value, kept alive in reg
func ifacePtr(f *iface) uintptr { return uintptr(unsafe.Pointer(f)) }

//nolint:gosec // G103: the vtable is a package-level slice that never goes away
func vtblAddr(v []uintptr) uintptr { return uintptr(unsafe.Pointer(&v[0])) }

// ptr is the object's i-th interface pointer, with no new reference.
func (o *object) ptr(i int) uintptr { return ifacePtr(o.ifaces[i]) }

func (o *object) addRef() uint32 {
	return uint32(o.refs.Add(1)) //nolint:gosec // G115: a reference count is never negative here
}

func (o *object) release() uint32 {
	n := o.refs.Add(-1)
	if n > 0 {
		return uint32(n) //nolint:gosec // G115: positive
	}
	if n < 0 { // a client released once too often: keep the count at zero
		o.refs.Store(0)
		return 0
	}
	regMu.Lock()
	for _, f := range o.ifaces {
		delete(reg, ifacePtr(f))
	}
	regMu.Unlock()
	if o.final != nil {
		o.final()
	}
	return 0
}

// lookup is the object behind an interface pointer, nil after its last Release.
func lookup(this uintptr) *iface {
	regMu.RLock()
	defer regMu.RUnlock()
	return reg[this]
}

// guard turns a panic in a COM callback into E_UNEXPECTED: a panic must never cross into the caller.
func guard(hr *uintptr) {
	if r := recover(); r != nil {
		*hr = eUnexpected
	}
}

// The IUnknown part of every vtable.
var (
	cbQueryInterface = windows.NewCallback(func(this uintptr, riid *windows.GUID, ppv *uintptr) (hr uintptr) {
		defer guard(&hr)
		if ppv == nil {
			return ePointer
		}
		*ppv = 0
		f := lookup(this)
		if f == nil || riid == nil {
			return eInvalidArg
		}
		if *riid == iidIUnknown {
			f.obj.addRef()
			*ppv = f.obj.ptr(0)
			return sOK
		}
		for _, g := range f.obj.ifaces {
			for _, id := range g.iids {
				if id == *riid {
					f.obj.addRef()
					*ppv = ifacePtr(g)
					return sOK
				}
			}
		}
		return eNoInterface
	})
	cbAddRef = windows.NewCallback(func(this uintptr) (n uintptr) {
		defer guard(&n)
		if f := lookup(this); f != nil {
			return uintptr(f.obj.addRef())
		}
		return 0
	})
	cbRelease = windows.NewCallback(func(this uintptr) (n uintptr) {
		defer guard(&n)
		if f := lookup(this); f != nil {
			return uintptr(f.obj.release())
		}
		return 0
	})
)

// implOf is the Go value behind an interface pointer, nil after its last Release.
func implOf[T any](this uintptr) T {
	var zero T
	f := lookup(this)
	if f == nil {
		return zero
	}
	v, _ := f.obj.impl.(T)
	return v
}

// comPtr is an interface pointer of a COM object that is not ours: a proxy, a system stream or
// enumerator. Only the methods it has are called.
type comPtr struct {
	vtbl *[16]uintptr
}

// call invokes method i of p.
//
//nolint:gosec // G103: calling a COM method passes the interface pointer itself
// call invokes vtable method i. Like syscall's Proc.Call it must be uintptrescapes: callers pass
// uintptr(unsafe.Pointer(&local)) for out parameters, and without the directive the local may sit on
// a stack that moves during the call, so the callee writes to the old address (seen under -race).
//
//go:uintptrescapes
func (p *comPtr) call(i int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(p))}, args...)
	r, _, _ := syscall.SyscallN(p.vtbl[i], all...)
	return r
}

func (p *comPtr) release() {
	if p != nil {
		p.call(2)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 } //nolint:gosec // G115: an HRESULT is a signed 32-bit value

// hglobalFrom copies b into a new movable HGLOBAL; the receiver of the medium frees it.
//
//nolint:gosec // G103: RtlMoveMemory copies from the Go slice into the locked block
func hglobalFrom(b []byte) (uintptr, error) {
	n := max(len(b), 1)
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(n))
	if h == 0 {
		return 0, err
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return 0, err
	}
	if len(b) > 0 {
		_, _, _ = procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	}
	_, _, _ = procGlobalUnlock.Call(h)
	return h, nil
}

// hglobalBytes copies the content of an HGLOBAL.
//
//nolint:gosec // G103: RtlMoveMemory copies from the locked block into a Go slice
func hglobalBytes(h uintptr) []byte {
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 || size > 64<<20 {
		return nil
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil
	}
	defer func() { _, _, _ = procGlobalUnlock.Call(h) }()
	b := make([]byte, size)
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&b[0])), p, size)
	return b
}

// coTaskString is s as a NUL-terminated UTF-16 string in CoTaskMem memory, which the caller frees.
//
//nolint:gosec // G103: RtlMoveMemory copies the UTF-16 Go slice into the task memory block
func coTaskString(s string) uintptr {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0
	}
	n := uintptr(len(u)) * 2
	p, _, _ := procCoTaskMemAlloc.Call(n)
	if p == 0 {
		return 0
	}
	_, _, _ = procRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&u[0])), n)
	return p
}

// registerFormat is the clipboard format id of name.
func registerFormat(name string) uint16 {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p))) //nolint:gosec // G103: a UTF-16 string for the call
	return uint16(r)                                                         //nolint:gosec // G115: a clipboard format is 0xC000–0xFFFF
}

// mta runs functions on one thread of the process's multithreaded apartment. The data object is
// marshaled there, so Explorer's calls (GetData, the stream reads) are served by COM's worker
// threads and never wait for the window's thread, which stays free while files are copied.
var mta struct {
	once sync.Once
	ch   chan func()
	err  error
}

func onMTA(fn func()) error {
	mta.once.Do(func() {
		mta.ch = make(chan func())
		ready := make(chan error)
		go func() {
			runtime.LockOSThread()                              // the thread stays in the MTA for the life of the process
			hr, _, _ := procCoInitializeEx.Call(0, coinitMulti) //nolint:gosec // G104: the HRESULT is the result
			if failed(hr) {
				ready <- syscall.Errno(hr)
				return
			}
			ready <- nil
			for f := range mta.ch {
				f()
			}
		}()
		mta.err = <-ready
	})
	if mta.err != nil {
		return mta.err
	}
	done := make(chan struct{})
	mta.ch <- func() {
		defer close(done)
		defer func() { _ = recover() }()
		fn()
	}
	<-done
	return nil
}

// leftButtonDown reports whether the primary mouse button is held now.
func leftButtonDown() bool {
	vk := uintptr(vkLButton)
	if swapped, _, _ := procGetSystemMetrics.Call(smSwapButton); swapped != 0 { //nolint:gosec // G104: the metric is the result
		vk = vkRButton // GetAsyncKeyState reads the physical buttons
	}
	s, _, _ := procGetAsyncKeyState.Call(vk) //nolint:gosec // G104: the key state is the result
	return s&0x8000 != 0
}
