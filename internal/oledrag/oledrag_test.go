package oledrag

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

func TestCheckPath(t *testing.T) {
	for _, ok := range []string{"a", `a\b.txt`, "файл", strings.Repeat("x", MaxPath)} {
		if err := CheckPath(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", `\a`, `a\`, `a\\b`, `.`, `a\..\b`, "a/b", "c:x", strings.Repeat("x", MaxPath+1)} {
		if err := CheckPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDescriptorLayout(t *testing.T) {
	mt := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{Path: "dir", Dir: true, ModTime: mt},
		{Path: `dir\файл.txt`, Size: 5<<32 + 7, ModTime: mt},
	}
	b, err := Descriptor(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 4+2*descriptorSize || binary.LittleEndian.Uint32(b) != 2 {
		t.Fatalf("size %d count %d", len(b), binary.LittleEndian.Uint32(b))
	}
	d0, d1 := b[4:4+descriptorSize], b[4+descriptorSize:]
	if f := binary.LittleEndian.Uint32(d1); f != fdAttributes|fdWritesTime|fdFileSize|fdProgressUI|fdUnicode {
		t.Errorf("flags %#x", f)
	}
	if a := binary.LittleEndian.Uint32(d0[36:]); a != attrDirectory {
		t.Errorf("dir attributes %#x", a)
	}
	if a := binary.LittleEndian.Uint32(d1[36:]); a != attrNormal {
		t.Errorf("file attributes %#x", a)
	}
	if hi, lo := binary.LittleEndian.Uint32(d1[64:]), binary.LittleEndian.Uint32(d1[68:]); hi != 5 || lo != 7 {
		t.Errorf("size %d %d", hi, lo)
	}
	ft := uint64(binary.LittleEndian.Uint32(d1[60:]))<<32 | uint64(binary.LittleEndian.Uint32(d1[56:]))
	if want := uint64(mt.UnixNano()/100 + 116444736000000000); ft != want {
		t.Errorf("write time %d, want %d", ft, want)
	}
	name := make([]uint16, 0, MaxPath)
	for i := nameOffset; i+1 < descriptorSize; i += 2 {
		u := binary.LittleEndian.Uint16(d1[i:])
		if u == 0 {
			break
		}
		name = append(name, u)
	}
	if got := string(utf16.Decode(name)); got != `dir\файл.txt` {
		t.Errorf("name %q", got)
	}
	if _, err := Descriptor([]Item{{Path: "../x"}}); err == nil {
		t.Error("a path out of the folder accepted")
	}
	if _, err := Descriptor([]Item{{Path: "x", Size: -1}}); err == nil {
		t.Error("a negative size accepted")
	}
}

func TestDecider(t *testing.T) {
	var d Decider
	if r := d.Next(false, 0); r != dragdropSCancel {
		t.Errorf("released before the first look: %#x, want cancel", r)
	}
	d = Decider{}
	if r := d.Next(false, mkLButton); r != sOK {
		t.Errorf("held: %#x", r)
	}
	if r := d.Next(false, 0); r != dragdropSDrop {
		t.Errorf("released: %#x, want drop", r)
	}
	d = Decider{}
	if r := d.Next(true, mkLButton); r != dragdropSCancel {
		t.Errorf("Esc: %#x", r)
	}
	if r := d.Next(false, mkLButton|mkRButton); r != dragdropSCancel {
		t.Errorf("right button: %#x", r)
	}
}

func TestHresultOf(t *testing.T) {
	for err, want := range map[error]uint32{
		ErrLost:                      hrNetGone,
		fs.ErrPermission:             hrAccessDenied,
		fs.ErrNotExist:               hrNotFound,
		errors.New("other"):          eFail,
		io.ErrUnexpectedEOF:          eFail,
		errors.Join(ErrLost, io.EOF): hrNetGone,
	} {
		if got := hresultOf(err); got != want {
			t.Errorf("%v: %#x, want %#x", err, got, want)
		}
	}
}

// memFile is a File over bytes; it counts opens and seeks through its owner.
type memFile struct {
	r      *bytes.Reader
	failAt int64 // WriteTo fails with ErrLost after this many bytes, if > 0
	closed *int
}

func (m *memFile) Seek(off int64, whence int) (int64, error) { return m.r.Seek(off, whence) }

func (m *memFile) WriteTo(w io.Writer) (int64, error) {
	if m.failAt > 0 {
		pos, err := m.r.Seek(0, io.SeekCurrent)
		if err != nil {
			return 0, err
		}
		n, err := io.CopyN(w, m.r, max(m.failAt-pos, 0))
		if err != nil {
			return n, err
		}
		return n, ErrLost
	}
	return m.r.WriteTo(w)
}

func (m *memFile) Close() error { *m.closed++; return nil }

func data(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestReaderReadsAndSeeks(t *testing.T) {
	src := data(300_000)
	opens, closed := 0, 0
	r := NewReader(int64(len(src)), func() (File, error) {
		opens++
		return &memFile{r: bytes.NewReader(src), closed: &closed}, nil
	})
	if r.Size() != int64(len(src)) || opens != 0 {
		t.Fatalf("size %d opens %d", r.Size(), opens)
	}
	var got bytes.Buffer
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if n != len(buf) && got.Len() != len(src) {
			t.Fatalf("short read %d at %d", n, got.Len())
		}
	}
	if !bytes.Equal(got.Bytes(), src) || opens != 1 {
		t.Fatalf("content differs or %d opens", opens)
	}
	if p, err := r.Seek(-10, io.SeekEnd); err != nil || p != int64(len(src))-10 {
		t.Fatalf("seek %d %v", p, err)
	}
	n, err := r.Read(buf)
	if err != nil || n != 10 || !bytes.Equal(buf[:n], src[len(src)-10:]) {
		t.Fatalf("tail %d %v", n, err)
	}
	if _, err := r.Seek(1000, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Read(buf[:5]); err != nil || n != 5 || !bytes.Equal(buf[:5], src[1000:1005]) {
		t.Fatalf("after seek %d %v", n, err)
	}
	if _, err := r.Seek(-1, io.SeekStart); err == nil {
		t.Error("negative position accepted")
	}
	if err := r.Close(); err != nil || closed != 1 {
		t.Fatalf("close %v, closed %d", err, closed)
	}
	if _, err := r.Read(buf); err == nil {
		t.Error("read after close")
	}
	if err := r.Close(); err != nil || closed != 1 {
		t.Fatalf("second close %v, closed %d", err, closed)
	}
}

func TestReaderStopsAtSizeAndShrink(t *testing.T) {
	src := data(1000)
	closed := 0
	open := func() (File, error) { return &memFile{r: bytes.NewReader(src), closed: &closed}, nil }

	// The file grew after the descriptor was made: the stream ends at the told size.
	r := NewReader(600, open)
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, src[:600]) {
		t.Fatalf("grown: %d bytes, %v", len(got), err)
	}
	mustClose(t, r)

	// The file shrank: the stream ends where the file does.
	r = NewReader(5000, open)
	got, err = io.ReadAll(r)
	if err != nil || !bytes.Equal(got, src) || r.Size() != 1000 {
		t.Fatalf("shrunk: %d bytes, size %d, %v", len(got), r.Size(), err)
	}
	mustClose(t, r)
}

func TestReaderLostAndOpenError(t *testing.T) {
	src := data(1000)
	closed := 0
	r := NewReader(1000, func() (File, error) {
		return &memFile{r: bytes.NewReader(src), failAt: 400, closed: &closed}, nil
	})
	buf := make([]byte, 1000)
	n, err := r.Read(buf)
	if n != 400 || !errors.Is(err, ErrLost) {
		t.Fatalf("lost: %d %v", n, err)
	}
	mustClose(t, r)

	r = NewReader(10, func() (File, error) { return nil, fs.ErrPermission })
	if _, err := r.Read(buf); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("open error: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReaderConcurrentUse(t *testing.T) {
	src := data(200_000)
	closed := 0
	r := NewReader(int64(len(src)), func() (File, error) {
		return &memFile{r: bytes.NewReader(src), closed: &closed}, nil
	})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				if _, err := r.Read(buf); err != nil {
					return
				}
				_ = r.Size()
			}
		}()
	}
	wg.Wait()
	mustClose(t, r)
}

func mustClose(t *testing.T, r *Reader) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}
