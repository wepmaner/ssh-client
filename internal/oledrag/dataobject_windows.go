package oledrag

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tymedHGlobal    = 1
	tymedIStream    = 4
	dvaspectContent = 1
	datadirGet      = 1

	dvEFormatEtc           = 0x80040064
	dvELindex              = 0x80040068
	dvETymed               = 0x80040069
	dvEDVAspect            = 0x8004006B
	oleEAdviseNotSupported = 0x80040003
	dataSSameFormatEtc     = 0x00040130
	stgEAccessDenied       = 0x80030005
	stgEInvalidFunction    = 0x80030001
	stgEInvalidPointer     = 0x80030009
	stgEReadFault          = 0x8003001E
	maxStoredFormatBytes   = 1 << 20
)

// formatEtc is FORMATETC.
type formatEtc struct {
	cf     uint16
	ptd    uintptr
	aspect uint32
	lindex int32
	tymed  uint32
}

// stgMedium is STGMEDIUM.
type stgMedium struct {
	tymed   uint32
	data    uintptr // hGlobal or pstm
	release uintptr // pUnkForRelease
}

// Clipboard formats of the shell, registered once.
var formats struct {
	once                         sync.Once
	descriptor, contents, effect uint16
}

func initFormats() {
	formats.once.Do(func() {
		formats.descriptor = registerFormat("FileGroupDescriptorW")
		formats.contents = registerFormat("FileContents")
		formats.effect = registerFormat("Preferred DropEffect")
	})
}

// holder counts the data object and its live streams; the source is released when all are gone.
type holder struct {
	n   atomic.Int32
	src Source
}

func (h *holder) add() { h.n.Add(1) }

func (h *holder) done() {
	if h.n.Add(-1) == 0 {
		h.src.Release()
	}
}

// dataObject is the IDataObject of a drag, with IDataObjectAsyncCapability.
type dataObject struct {
	items []Item
	desc  []byte
	h     *holder

	mu     sync.Mutex
	stored map[uint16][]byte // formats the drop target or the shell set (drop effects, drag images)
	async  bool
	inOp   bool
}

func newDataObject(items []Item, desc []byte, h *holder) *object {
	d := &dataObject{items: items, desc: desc, h: h, async: true, stored: map[uint16][]byte{}}
	return newObject(d, h.done,
		ifaceDef{vtbl: dataObjectVtbl, iids: []windows.GUID{iidIDataObject}},
		ifaceDef{vtbl: asyncVtbl, iids: []windows.GUID{iidIAsyncCapability}},
	)
}

// offered reports the formats GetData serves, with the stored ones.
func (d *dataObject) offered() []formatEtc {
	out := []formatEtc{
		{cf: formats.descriptor, aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal},
		{cf: formats.contents, aspect: dvaspectContent, lindex: -1, tymed: tymedIStream},
		{cf: formats.effect, aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal},
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for cf := range d.stored {
		if cf != formats.descriptor && cf != formats.contents && cf != formats.effect {
			out = append(out, formatEtc{cf: cf, aspect: dvaspectContent, lindex: -1, tymed: tymedHGlobal})
		}
	}
	return out
}

// query checks fe against the offered formats: S_OK or the DV_E_ error.
func (d *dataObject) query(fe *formatEtc) uintptr {
	if fe.aspect != dvaspectContent {
		return dvEDVAspect
	}
	for _, o := range d.offered() {
		if o.cf != fe.cf {
			continue
		}
		if fe.tymed&o.tymed == 0 {
			return dvETymed
		}
		return sOK
	}
	return dvEFormatEtc
}

func (d *dataObject) storedFormat(cf uint16) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.stored[cf]
	return b, ok
}

func (d *dataObject) getData(fe *formatEtc, med *stgMedium) uintptr {
	if hr := d.query(fe); hr != sOK {
		return hr
	}
	hglobal := func(b []byte) uintptr {
		h, err := hglobalFrom(b)
		if err != nil {
			return eOutOfMemory
		}
		*med = stgMedium{tymed: tymedHGlobal, data: h}
		return sOK
	}
	switch fe.cf {
	case formats.descriptor:
		return hglobal(d.desc)
	case formats.effect:
		if b, ok := d.storedFormat(fe.cf); ok {
			return hglobal(b)
		}
		return hglobal([]byte{dropEffectCopy, 0, 0, 0})
	case formats.contents:
		i := int(fe.lindex)
		if i < 0 && len(d.items) == 1 {
			i = 0
		}
		if i < 0 || i >= len(d.items) || d.items[i].Dir {
			return dvELindex
		}
		s := newStream(d.items[i], i, d.h)
		*med = stgMedium{tymed: tymedIStream, data: s.ptr(0)}
		return sOK
	}
	if b, ok := d.storedFormat(fe.cf); ok {
		return hglobal(b)
	}
	return dvEFormatEtc
}

// setData keeps an HGLOBAL format a drop target or the shell sets, so it can read it back.
func (d *dataObject) setData(fe *formatEtc, med *stgMedium, release bool) uintptr {
	if med.tymed != tymedHGlobal || fe.cf == formats.descriptor || fe.cf == formats.contents {
		return eNotImpl
	}
	b := hglobalBytes(med.data)
	if len(b) > maxStoredFormatBytes {
		return eOutOfMemory
	}
	d.mu.Lock()
	d.stored[fe.cf] = b
	d.mu.Unlock()
	if release {
		releaseMedium(med)
	}
	return sOK
}

//nolint:gosec // G103: ReleaseStgMedium takes the medium the caller passed
func releaseMedium(med *stgMedium) {
	_, _, _ = procReleaseStgMedium.Call(uintptr(unsafe.Pointer(med)))
}

// enumFormats returns a system enumerator over the offered formats.
//
//nolint:gosec // G103: SHCreateStdEnumFmtEtc copies the Go array and writes the enumerator pointer
func (d *dataObject) enumFormats(pp *uintptr) uintptr {
	fs := d.offered()
	r, _, _ := procSHCreateStdEnumFmtEtc.Call(uintptr(len(fs)), uintptr(unsafe.Pointer(&fs[0])), uintptr(unsafe.Pointer(pp)))
	return r
}

var dataObjectVtbl = []uintptr{
	cbQueryInterface, cbAddRef, cbRelease,
	// GetData
	windows.NewCallback(func(this uintptr, fe *formatEtc, med *stgMedium) (hr uintptr) {
		defer guard(&hr)
		if fe == nil || med == nil {
			return eInvalidArg
		}
		*med = stgMedium{}
		d := implOf[*dataObject](this)
		if d == nil {
			return eUnexpected
		}
		return d.getData(fe, med)
	}),
	// GetDataHere
	windows.NewCallback(func(this uintptr, fe *formatEtc, med *stgMedium) uintptr { return eNotImpl }),
	// QueryGetData
	windows.NewCallback(func(this uintptr, fe *formatEtc) (hr uintptr) {
		defer guard(&hr)
		d := implOf[*dataObject](this)
		if d == nil || fe == nil {
			return eInvalidArg
		}
		return d.query(fe)
	}),
	// GetCanonicalFormatEtc
	windows.NewCallback(func(this uintptr, in *formatEtc, out *formatEtc) (hr uintptr) {
		defer guard(&hr)
		if out == nil {
			return eInvalidArg
		}
		out.ptd = 0
		return dataSSameFormatEtc
	}),
	// SetData
	windows.NewCallback(func(this uintptr, fe *formatEtc, med *stgMedium, release uintptr) (hr uintptr) {
		defer guard(&hr)
		d := implOf[*dataObject](this)
		if d == nil || fe == nil || med == nil {
			return eInvalidArg
		}
		return d.setData(fe, med, release != 0)
	}),
	// EnumFormatEtc
	windows.NewCallback(func(this uintptr, dir uintptr, pp *uintptr) (hr uintptr) {
		defer guard(&hr)
		if pp == nil {
			return eInvalidArg
		}
		*pp = 0
		d := implOf[*dataObject](this)
		if d == nil {
			return eUnexpected
		}
		if dir != datadirGet {
			return eNotImpl
		}
		return d.enumFormats(pp)
	}),
	// DAdvise, DUnadvise, EnumDAdvise
	windows.NewCallback(func(this, fe, advf, sink, conn uintptr) uintptr { return oleEAdviseNotSupported }),
	windows.NewCallback(func(this, conn uintptr) uintptr { return oleEAdviseNotSupported }),
	windows.NewCallback(func(this, pp uintptr) uintptr { return oleEAdviseNotSupported }),
}

func boolOf(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

var asyncVtbl = []uintptr{
	cbQueryInterface, cbAddRef, cbRelease,
	// SetAsyncMode
	windows.NewCallback(func(this, async uintptr) (hr uintptr) {
		defer guard(&hr)
		if d := implOf[*dataObject](this); d != nil {
			d.mu.Lock()
			d.async = async != 0
			d.mu.Unlock()
		}
		return sOK
	}),
	// GetAsyncMode
	windows.NewCallback(func(this uintptr, p *int32) (hr uintptr) {
		defer guard(&hr)
		d := implOf[*dataObject](this)
		if d == nil || p == nil {
			return eInvalidArg
		}
		d.mu.Lock()
		*p = boolOf(d.async)
		d.mu.Unlock()
		return sOK
	}),
	// StartOperation
	windows.NewCallback(func(this, bindCtx uintptr) (hr uintptr) {
		defer guard(&hr)
		if d := implOf[*dataObject](this); d != nil {
			d.mu.Lock()
			d.inOp = true
			d.mu.Unlock()
		}
		return sOK
	}),
	// InOperation
	windows.NewCallback(func(this uintptr, p *int32) (hr uintptr) {
		defer guard(&hr)
		d := implOf[*dataObject](this)
		if d == nil || p == nil {
			return eInvalidArg
		}
		d.mu.Lock()
		*p = boolOf(d.inOp)
		d.mu.Unlock()
		return sOK
	}),
	// EndOperation
	windows.NewCallback(func(this, result, bindCtx, effects uintptr) (hr uintptr) {
		defer guard(&hr)
		if d := implOf[*dataObject](this); d != nil {
			d.mu.Lock()
			d.inOp = false
			d.mu.Unlock()
		}
		return sOK
	}),
}
