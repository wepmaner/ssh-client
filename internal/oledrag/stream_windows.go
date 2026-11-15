package oledrag

import (
	"errors"
	"io"
	"path"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// statStg is STATSTG.
type statStg struct {
	name      uintptr
	typ       uint32
	size      uint64
	mtime     [2]uint32
	ctime     [2]uint32
	atime     [2]uint32
	mode      uint32
	locks     uint32
	clsid     windows.GUID
	stateBits uint32
	reserved  uint32
}

// stream is the IStream of one file of the drag: reads go to the remote file.
type stream struct {
	r    *Reader
	item Item
}

// newStream is the stream of item i, with one reference for the caller; it counts in h while alive.
func newStream(item Item, i int, h *holder) *object {
	s := &stream{item: item}
	s.r = NewReader(item.Size, func() (File, error) { return h.src.Open(i) })
	h.add()
	return newObject(s, func() {
		_ = s.r.Close() //nolint:gosec // G104: the stream is gone; a close error has nobody to go to
		h.done()
	}, ifaceDef{vtbl: streamVtbl, iids: []windows.GUID{iidIStream, iidISequentialStream}})
}

// read is ISequentialStream::Read: S_OK with fewer bytes only at the end of the file.
func (s *stream) read(buf []byte, pcb *uint32) uintptr {
	n, err := s.r.Read(buf)
	if pcb != nil {
		*pcb = uint32(n) //nolint:gosec // G115: n <= cb, a ULONG
	}
	if err == nil || errors.Is(err, io.EOF) || n > 0 {
		return sOK // an error after some bytes comes back on the next read
	}
	return uintptr(hresultOf(err))
}

func (s *stream) stat(st *statStg, flag uintptr) uintptr {
	*st = statStg{typ: stgtyStream, size: uint64(s.r.Size())} //nolint:gosec // G115: sizes are never negative
	st.mtime[0], st.mtime[1] = fileTime(s.item.ModTime)
	if flag&statflagNoName == 0 {
		name := s.item.Path
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:]
		}
		st.name = coTaskString(path.Base(name))
	}
	return sOK
}

// copyTo is IStream::CopyTo: up to cb bytes go to the Write of the target stream.
//
//nolint:gosec // G103: Write of the target stream reads the Go buffer and writes the count
func (s *stream) copyTo(dst *comPtr, cb uint64, pRead, pWritten *uint64) uintptr {
	buf := make([]byte, 256<<10)
	var read, written uint64
	defer func() {
		if pRead != nil {
			*pRead = read
		}
		if pWritten != nil {
			*pWritten = written
		}
	}()
	for read < cb {
		want := min(uint64(len(buf)), cb-read)
		n, err := s.r.Read(buf[:want])
		read += uint64(n) //nolint:gosec // G115: n is never negative
		if n > 0 {
			var w uint32
			if hr := dst.call(4, uintptr(unsafe.Pointer(&buf[0])), uintptr(n), uintptr(unsafe.Pointer(&w))); failed(hr) {
				return hr
			}
			written += uint64(w)
		}
		if errors.Is(err, io.EOF) || (err == nil && n == 0) {
			return sOK
		}
		if err != nil {
			return uintptr(hresultOf(err))
		}
	}
	return sOK
}

var streamVtbl = []uintptr{
	cbQueryInterface, cbAddRef, cbRelease,
	// Read
	windows.NewCallback(func(this uintptr, pv *byte, cb uintptr, pcb *uint32) (hr uintptr) {
		defer guard(&hr)
		if pcb != nil {
			*pcb = 0
		}
		s := implOf[*stream](this)
		if s == nil {
			return eUnexpected
		}
		if cb == 0 {
			return sOK
		}
		if pv == nil {
			return stgEInvalidPointer
		}
		return s.read(unsafe.Slice(pv, cb), pcb) //nolint:gosec // G103: the caller's buffer of cb bytes
	}),
	// Write
	windows.NewCallback(func(this, pv, cb uintptr, pcb *uint32) uintptr {
		if pcb != nil {
			*pcb = 0
		}
		return stgEAccessDenied
	}),
	// Seek
	windows.NewCallback(func(this, move, origin uintptr, pNew *uint64) (hr uintptr) {
		defer guard(&hr)
		s := implOf[*stream](this)
		if s == nil {
			return eUnexpected
		}
		p, err := s.r.Seek(int64(move), int(origin)) //nolint:gosec // G115: LARGE_INTEGER and DWORD passed as uintptr
		if err != nil {
			return stgEInvalidFunction
		}
		if pNew != nil {
			*pNew = uint64(p) //nolint:gosec // G115: p is never negative
		}
		return sOK
	}),
	// SetSize
	windows.NewCallback(func(this, size uintptr) uintptr { return stgEAccessDenied }),
	// CopyTo
	windows.NewCallback(func(this uintptr, dst *comPtr, cb uintptr, pRead, pWritten *uint64) (hr uintptr) {
		defer guard(&hr)
		s := implOf[*stream](this)
		if s == nil || dst == nil {
			return eInvalidArg
		}
		return s.copyTo(dst, uint64(cb), pRead, pWritten)
	}),
	// Commit, Revert
	windows.NewCallback(func(this, flags uintptr) uintptr { return sOK }),
	windows.NewCallback(func(this uintptr) uintptr { return sOK }),
	// LockRegion, UnlockRegion
	windows.NewCallback(func(this, off, cb, typ uintptr) uintptr { return stgEInvalidFunction }),
	windows.NewCallback(func(this, off, cb, typ uintptr) uintptr { return stgEInvalidFunction }),
	// Stat
	windows.NewCallback(func(this uintptr, st *statStg, flag uintptr) (hr uintptr) {
		defer guard(&hr)
		s := implOf[*stream](this)
		if s == nil || st == nil {
			return eInvalidArg
		}
		return s.stat(st, flag)
	}),
	// Clone
	windows.NewCallback(func(this uintptr, pp *uintptr) uintptr {
		if pp != nil {
			*pp = 0
		}
		return eNotImpl
	}),
}
