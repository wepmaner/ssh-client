// Package oledrag drags remote files out of the window into Explorer as OLE virtual files: a data
// object offers FileGroupDescriptorW (every file and folder with its relative path, size and time)
// and FileContents (one stream per file that reads the remote file as Explorer copies it).
//
// The parts that need no COM live here and build everywhere: the descriptor, the drop source's
// decision and the read-ahead reader of a stream. drag_windows.go runs the drag itself, the other
// _windows files hold the COM objects (data object, streams) in pure Go vtables.
package oledrag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// MaxPath is the most UTF-16 units of a relative path in a descriptor: cFileName is MAX_PATH
// with the terminating NUL.
const MaxPath = 259

// Item is one entry of the descriptor. Path is relative, with backslashes, every component a name
// Windows accepts; folders come before what lies in them.
type Item struct {
	Path    string
	Dir     bool
	Size    int64
	ModTime time.Time
}

// File is an open remote file: Seek places WriteTo, which streams the rest of the file.
type File interface {
	io.Seeker
	io.WriterTo
	io.Closer
}

// Source opens the files of a drag. Release is called once, when Explorer and the drag no longer
// hold the data object or any of its streams.
type Source interface {
	Open(index int) (File, error)
	Release()
}

// ErrLost marks a read that failed because the connection is gone: Explorer shows «сетевое имя
// больше недоступно».
var ErrLost = errors.New("connection lost")

// errBadPath is a descriptor path that could leave the target folder or does not fit.
var errBadPath = errors.New("bad descriptor path")

// FILEDESCRIPTORW flags and attributes.
const (
	fdAttributes  = 0x00000004
	fdWritesTime  = 0x00000020
	fdFileSize    = 0x00000040
	fdProgressUI  = 0x00004000
	fdUnicode     = 0x80000000
	attrDirectory = 0x00000010
	attrNormal    = 0x00000080

	descriptorSize = 592 // sizeof(FILEDESCRIPTORW)
	nameOffset     = 72  // offsetof(FILEDESCRIPTORW, cFileName)
)

// CheckPath refuses a descriptor path that is empty, absolute, has an empty, "." or ".." component,
// a slash or a colon, or does not fit MaxPath.
func CheckPath(p string) error {
	if p == "" || len(utf16.Encode([]rune(p))) > MaxPath || strings.ContainsAny(p, "/:") {
		return fmt.Errorf("%w: %q", errBadPath, p)
	}
	for _, c := range strings.Split(p, `\`) {
		if c == "" || c == "." || c == ".." {
			return fmt.Errorf("%w: %q", errBadPath, p)
		}
	}
	return nil
}

// fileTime is t as a FILETIME: 100 ns intervals since 1601-01-01 UTC.
func fileTime(t time.Time) (lo, hi uint32) {
	if t.IsZero() {
		return 0, 0
	}
	const epochDiff = 116444736000000000 // 1601 to 1970 in 100 ns
	v := t.UnixNano()/100 + epochDiff
	if v < 0 {
		return 0, 0
	}
	u := uint64(v)
	return uint32(u), uint32(u >> 32) //nolint:gosec // G115: the two halves of a 64-bit FILETIME
}

// Descriptor is the FILEGROUPDESCRIPTORW of items: the count, then one FILEDESCRIPTORW each with
// attributes, size, write time and Explorer's own progress dialog.
func Descriptor(items []Item) ([]byte, error) {
	b := make([]byte, 4+descriptorSize*len(items))
	binary.LittleEndian.PutUint32(b, uint32(len(items))) //nolint:gosec // G115: the caller caps the count
	for i, it := range items {
		if err := CheckPath(it.Path); err != nil {
			return nil, err
		}
		if it.Size < 0 {
			return nil, fmt.Errorf("%w: negative size of %q", errBadPath, it.Path)
		}
		d := b[4+i*descriptorSize : 4+(i+1)*descriptorSize]
		binary.LittleEndian.PutUint32(d[0:], fdAttributes|fdWritesTime|fdFileSize|fdProgressUI|fdUnicode)
		attr := uint32(attrNormal)
		size := uint64(it.Size)
		if it.Dir {
			attr, size = attrDirectory, 0
		}
		binary.LittleEndian.PutUint32(d[36:], attr)
		lo, hi := fileTime(it.ModTime)
		binary.LittleEndian.PutUint32(d[56:], lo) // ftLastWriteTime
		binary.LittleEndian.PutUint32(d[60:], hi)
		binary.LittleEndian.PutUint32(d[64:], uint32(size>>32)) //nolint:gosec // G115: nFileSizeHigh
		binary.LittleEndian.PutUint32(d[68:], uint32(size))     //nolint:gosec // G115: nFileSizeLow
		for j, u := range utf16.Encode([]rune(it.Path)) {
			binary.LittleEndian.PutUint16(d[nameOffset+2*j:], u)
		}
	}
	return b, nil
}

// Drop source answers (HRESULTs of IDropSource::QueryContinueDrag) and the key state bits.
const (
	sOK                 = 0
	dragdropSDrop       = 0x00040100
	dragdropSCancel     = 0x00040101
	dragdropSUseDefault = 0x00040102
	mkLButton           = 0x0001
	mkRButton           = 0x0002
)

// QueryContinue is the drop source's decision: Esc or a right click while dragging cancels, the
// released left button drops, otherwise the drag goes on.
func QueryContinue(escape bool, keys uint32) uint32 {
	switch {
	case escape, keys&mkRButton != 0:
		return dragdropSCancel
	case keys&mkLButton == 0:
		return dragdropSDrop
	}
	return sOK
}

// Decider is the drop source's state over a drag: a drag whose first look finds the button already
// released is cancelled, not dropped, so a late start never drops files where the cursor happens to
// be.
type Decider struct{ sawDown bool }

// Next is the answer to one QueryContinueDrag.
func (d *Decider) Next(escape bool, keys uint32) uint32 {
	if keys&mkLButton != 0 {
		d.sawDown = true
	}
	r := QueryContinue(escape, keys)
	if r == dragdropSDrop && !d.sawDown {
		return dragdropSCancel
	}
	return r
}

// HRESULTs a failed read reports, so Explorer names the cause.
const (
	eFail          = 0x80004005
	hrAccessDenied = 0x80070005 // HRESULT_FROM_WIN32(ERROR_ACCESS_DENIED)
	hrNotFound     = 0x80070002 // HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND)
	hrNetGone      = 0x80070040 // HRESULT_FROM_WIN32(ERROR_NETNAME_DELETED)
)

// hresultOf is the HRESULT a stream returns for err.
func hresultOf(err error) uint32 {
	switch {
	case errors.Is(err, ErrLost):
		return hrNetGone
	case errors.Is(err, fs.ErrPermission):
		return hrAccessDenied
	case errors.Is(err, fs.ErrNotExist):
		return hrNotFound
	}
	return eFail
}

// Reader is the content of one stream: it opens the file on the first read and streams it with
// WriteTo from the position, so the remote reads stay pipelined while Explorer reads in chunks.
// A seek elsewhere restarts the streaming there. It never reads past size, the size Explorer was
// told in the descriptor.
type Reader struct {
	mu     sync.Mutex
	open   func() (File, error)
	f      File
	size   int64
	pos    int64
	pr     *io.PipeReader
	at     int64         // the position the pipe delivers next
	done   chan struct{} // closed when the WriteTo of pr has returned
	closed bool
}

// NewReader is the reader of a file of size bytes that open opens.
func NewReader(size int64, open func() (File, error)) *Reader {
	return &Reader{size: size, open: open}
}

// Size is the size the reader ends at.
func (r *Reader) Size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Read fills p up to the end of the file; it returns fewer bytes only at the end or with an error.
func (r *Reader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.pos >= r.size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if left := r.size - r.pos; want > left {
		want = left
	}
	if want == 0 {
		return 0, nil
	}
	if r.f == nil {
		f, err := r.open()
		if err != nil {
			return 0, err
		}
		r.f = f
	}
	if r.pr == nil || r.at != r.pos {
		r.stopLocked()
		if _, err := r.f.Seek(r.pos, io.SeekStart); err != nil {
			return 0, err
		}
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func(f File) {
			defer close(done)
			_, err := f.WriteTo(pw)
			if err == nil {
				err = io.EOF
			}
			pw.CloseWithError(err)
		}(r.f)
		r.pr, r.at, r.done = pr, r.pos, done
	}
	n, err := io.ReadFull(r.pr, p[:want])
	r.pos += int64(n)
	r.at += int64(n)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		// The file is shorter now than when the drag started: it ends here.
		r.size = r.pos
		r.stopLocked()
		if n > 0 {
			return n, nil
		}
		return 0, io.EOF
	}
	r.stopLocked() // the next read starts the streaming anew at pos
	return n, err
}

// stopLocked ends the streaming and waits for its WriteTo, so the file can seek again.
func (r *Reader) stopLocked() {
	if r.pr == nil {
		return
	}
	_ = r.pr.Close() //nolint:gosec // G104: closing a pipe reader never fails
	<-r.done
	r.pr, r.done = nil, nil
}

// Seek moves the position; it reads nothing. whence is io.SeekStart, io.SeekCurrent or io.SeekEnd,
// the same values as STREAM_SEEK_SET, _CUR and _END.
func (r *Reader) Seek(off int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.pos
	case io.SeekEnd:
		base = r.size
	default:
		return r.pos, errors.New("bad whence")
	}
	p := base + off
	if p < 0 {
		return r.pos, errors.New("negative position")
	}
	r.pos = p
	return p, nil
}

// Close ends the streaming and closes the file.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.stopLocked()
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}
