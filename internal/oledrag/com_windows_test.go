package oledrag

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fakeSource serves memFiles and counts its releases.
type fakeSource struct {
	files    [][]byte
	released atomic.Int32
	closed   int
}

func (s *fakeSource) Open(i int) (File, error) {
	if s.files[i] == nil {
		return nil, fs.ErrPermission
	}
	return &memFile{r: bytes.NewReader(s.files[i]), closed: &s.closed}, nil
}

func (s *fakeSource) Release() { s.released.Add(1) }

// vcall calls method i of the vtable vt on the interface pointer p, as COM would.
func vcall(vt []uintptr, p uintptr, i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(vt[i], append([]uintptr{p}, args...)...)
	return r
}

func newTestDataObject(t *testing.T, items []Item, src Source) (*object, *holder) {
	t.Helper()
	initFormats()
	desc, err := Descriptor(items)
	if err != nil {
		t.Fatal(err)
	}
	h := &holder{src: src}
	h.add()
	return newDataObject(items, desc, h), h
}

//nolint:gosec // G103: the test passes pointers to its own structs to the vtable methods
func TestDataObjectComBasics(t *testing.T) {
	src := &fakeSource{files: [][]byte{nil, data(100_000), nil}}
	items := []Item{
		{Path: "d", Dir: true},
		{Path: `d\a.bin`, Size: 100_000, ModTime: time.Now()},
		{Path: `d\locked`, Size: 3},
	}
	obj, _ := newTestDataObject(t, items, src)
	pdo := obj.ptr(0)

	// QueryInterface: the async capability and IUnknown, each with a reference.
	var pa, pu uintptr
	if hr := vcall(dataObjectVtbl, pdo, 0, uintptr(unsafe.Pointer(&iidIAsyncCapability)), uintptr(unsafe.Pointer(&pa))); hr != sOK || pa == 0 {
		t.Fatalf("QI async %#x", hr)
	}
	if hr := vcall(asyncVtbl, pa, 0, uintptr(unsafe.Pointer(&iidIUnknown)), uintptr(unsafe.Pointer(&pu))); hr != sOK || pu != pdo {
		t.Fatalf("QI IUnknown %#x, %#x want %#x", hr, pu, pdo)
	}
	var none uintptr
	if hr := vcall(dataObjectVtbl, pdo, 0, uintptr(unsafe.Pointer(&iidIStream)), uintptr(unsafe.Pointer(&none))); hr != eNoInterface || none != 0 {
		t.Fatalf("QI IStream on the data object %#x", hr)
	}
	if n := obj.refs.Load(); n != 3 {
		t.Fatalf("refs %d, want 3", n)
	}
	var async int32
	if hr := vcall(asyncVtbl, pa, 4, uintptr(unsafe.Pointer(&async))); hr != sOK || async != 1 {
		t.Fatalf("GetAsyncMode %#x %d", hr, async)
	}
	vcall(asyncVtbl, pa, 2)
	vcall(dataObjectVtbl, pu, 2)

	// The descriptor comes as an HGLOBAL with the bytes of Descriptor.
	fe := formatEtc{cf: formats.descriptor, aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := vcall(dataObjectVtbl, pdo, 5, uintptr(unsafe.Pointer(&fe))); hr != sOK {
		t.Fatalf("QueryGetData %#x", hr)
	}
	var med stgMedium
	if hr := vcall(dataObjectVtbl, pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&med))); hr != sOK || med.tymed != tymedHGlobal {
		t.Fatalf("GetData descriptor %#x", hr)
	}
	got := hglobalBytes(med.data)
	if len(got) < 4 || binary.LittleEndian.Uint32(got) != 3 {
		t.Fatalf("descriptor of %d bytes", len(got))
	}
	releaseMedium(&med)

	// The preferred effect is copy.
	fe = formatEtc{cf: formats.effect, aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := vcall(dataObjectVtbl, pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&med))); hr != sOK {
		t.Fatalf("GetData effect %#x", hr)
	}
	if b := hglobalBytes(med.data); len(b) < 4 || binary.LittleEndian.Uint32(b) != dropEffectCopy {
		t.Fatalf("effect %v", b)
	}
	releaseMedium(&med)

	// A folder has no contents; a file comes as a stream.
	fe = formatEtc{cf: formats.contents, aspect: dvaspectContent, lindex: 0, tymed: tymedIStream}
	if hr := vcall(dataObjectVtbl, pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&med))); hr != dvELindex {
		t.Fatalf("contents of a folder %#x", hr)
	}
	fe.lindex = 1
	if hr := vcall(dataObjectVtbl, pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&med))); hr != sOK || med.tymed != tymedIStream || med.data == 0 {
		t.Fatalf("contents %#x", hr)
	}
	ps := med.data

	var st statStg
	if hr := vcall(streamVtbl, ps, 12, uintptr(unsafe.Pointer(&st)), statflagNoName); hr != sOK || st.size != 100_000 || st.typ != stgtyStream {
		t.Fatalf("Stat %#x %+v", hr, st)
	}
	var all bytes.Buffer
	buf := make([]byte, 30_000)
	for {
		var n uint32
		hr := vcall(streamVtbl, ps, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)))
		if hr != sOK {
			t.Fatalf("Read %#x", hr)
		}
		all.Write(buf[:n])
		if n < uint32(len(buf)) {
			break
		}
	}
	if !bytes.Equal(all.Bytes(), src.files[1]) {
		t.Fatalf("stream read %d bytes", all.Len())
	}
	var pos uint64
	if hr := vcall(streamVtbl, ps, 5, 10, 0, uintptr(unsafe.Pointer(&pos))); hr != sOK || pos != 10 {
		t.Fatalf("Seek %#x %d", hr, pos)
	}
	var n uint32
	if hr := vcall(streamVtbl, ps, 3, uintptr(unsafe.Pointer(&buf[0])), 4, uintptr(unsafe.Pointer(&n))); hr != sOK || n != 4 || !bytes.Equal(buf[:4], src.files[1][10:14]) {
		t.Fatalf("Read after seek %#x %d", hr, n)
	}
	if hr := vcall(streamVtbl, ps, 4, uintptr(unsafe.Pointer(&buf[0])), 4, uintptr(unsafe.Pointer(&n))); hr != stgEAccessDenied {
		t.Fatalf("Write %#x", hr)
	}

	// A file the source cannot open reports access denied.
	fe.lindex = 2
	if hr := vcall(dataObjectVtbl, pdo, 3, uintptr(unsafe.Pointer(&fe)), uintptr(unsafe.Pointer(&med))); hr != sOK {
		t.Fatalf("contents 2 %#x", hr)
	}
	ps2 := med.data
	if hr := vcall(streamVtbl, ps2, 3, uintptr(unsafe.Pointer(&buf[0])), 3, uintptr(unsafe.Pointer(&n))); hr != hrAccessDenied || n != 0 {
		t.Fatalf("Read of a locked file %#x %d", hr, n)
	}

	// The source is released after the data object and both streams are gone, once.
	if r := vcall(dataObjectVtbl, pdo, 2); r != 0 {
		t.Fatalf("data object refs after release %d", r)
	}
	if lookup(pdo) != nil {
		t.Fatal("the data object is still registered")
	}
	if src.released.Load() != 0 {
		t.Fatal("released while streams live")
	}
	vcall(streamVtbl, ps, 2)
	vcall(streamVtbl, ps2, 2)
	if src.released.Load() != 1 || src.closed != 1 {
		t.Fatalf("released %d, closed %d", src.released.Load(), src.closed)
	}
	// A call on a released pointer is refused, not a crash.
	if hr := vcall(streamVtbl, ps, 3, uintptr(unsafe.Pointer(&buf[0])), 3, uintptr(unsafe.Pointer(&n))); hr != eUnexpected {
		t.Fatalf("Read after release %#x", hr)
	}
	if r := vcall(dataObjectVtbl, pdo, 2); r != 0 {
		t.Fatalf("release after the end %d", r)
	}
	if src.released.Load() != 1 {
		t.Fatal("released twice")
	}
}

func TestDropSourceVtable(t *testing.T) {
	ds := newObject(&dropSource{}, nil, ifaceDef{vtbl: dropSourceVtbl, iids: []windows.GUID{iidIDropSource}})
	p := ds.ptr(0)
	if hr := vcall(dropSourceVtbl, p, 3, 0, mkLButton); hr != sOK {
		t.Fatalf("held %#x", hr)
	}
	if hr := vcall(dropSourceVtbl, p, 3, 0, 0); hr != dragdropSDrop {
		t.Fatalf("released %#x", hr)
	}
	if hr := vcall(dropSourceVtbl, p, 4, dropEffectCopy); hr != dragdropSUseDefault {
		t.Fatalf("GiveFeedback %#x", hr)
	}
	if r := vcall(dropSourceVtbl, p, 2); r != 0 {
		t.Fatalf("refs %d", r)
	}
}

func TestMTARunsOnOneThread(t *testing.T) {
	var a, b uint32
	if err := onMTA(func() { a = windows.GetCurrentThreadId() }); err != nil {
		t.Fatal(err)
	}
	if err := onMTA(func() { b = windows.GetCurrentThreadId() }); err != nil {
		t.Fatal(err)
	}
	if a == 0 || a != b {
		t.Fatalf("threads %d %d", a, b)
	}
	if err := onMTA(func() { panic("boom") }); err != nil {
		t.Fatal(err)
	}
}
