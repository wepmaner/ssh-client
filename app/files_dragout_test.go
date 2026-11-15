package app

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"sshclient/internal/oledrag"
	"sshclient/internal/sshclient"
)

func TestDragItemsNames(t *testing.T) {
	long := strings.Repeat("x", 258) // d\<long> is one unit over MaxPath: the folder goes with all under it
	plan := []sshclient.PlanItem{
		{Src: "/h/d", Rel: "d", Dir: true},
		{Src: "/h/d/a:b?.txt", Rel: "d/a:b?.txt", Size: 3},
		{Src: "/h/d/con", Rel: "d/con", Size: 1},
		{Src: "/h/d/" + long, Rel: "d/" + long, Dir: true},
		{Src: "/h/d/" + long + "/f", Rel: "d/" + long + "/f", Size: 1},
		{Src: "/h/d/" + long + "/g/h", Rel: "d/" + long + "/g/h", Size: 1},
		{Src: "/h/d/end. ", Rel: "d/end. ", Size: 2},
	}
	items, srcs, skipped := dragItems(plan)
	want := []string{"d", `d\a_b_.txt`, `d\_con`, `d\end__`}
	if len(items) != len(want) || skipped != 3 {
		t.Fatalf("items %+v, skipped %d", items, skipped)
	}
	for i, w := range want {
		if items[i].Path != w {
			t.Errorf("item %d: %q, want %q", i, items[i].Path, w)
		}
	}
	if srcs[1] != "/h/d/a:b?.txt" || srcs[3] != "/h/d/end. " || !items[0].Dir || items[1].Size != 3 {
		t.Fatalf("srcs %v items %+v", srcs, items)
	}
	if _, err := oledrag.Descriptor(items); err != nil {
		t.Fatal(err)
	}
}

func TestDragErrorKeepsNoEOF(t *testing.T) {
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, errFilesClosed} {
		got := dragError(err)
		if !errors.Is(got, oledrag.ErrLost) || errors.Is(got, io.EOF) || errors.Is(got, io.ErrUnexpectedEOF) {
			t.Errorf("%v became %v", err, got)
		}
	}
	if got := dragError(fs.ErrNotExist); !errors.Is(got, fs.ErrNotExist) {
		t.Errorf("not found became %v", got)
	}
	if got := dragError(fs.ErrPermission); !errors.Is(got, fs.ErrPermission) {
		t.Errorf("permission became %v", got)
	}
	if dragError(nil) != nil {
		t.Error("nil became an error")
	}
}

func TestFilesDragOutStreams(t *testing.T) {
	s := newFilesApp(t)
	a := writeData(t, filepath.Join(s.root, "d", "a.bin"), 300_000)
	b := writeData(t, filepath.Join(s.root, "d", "sub", "b.bin"), 10)
	c := writeData(t, filepath.Join(s.root, "c.txt"), 5)
	tab := s.openTab(t)
	home := s.home(t, tab)

	if err := s.FilesDragOut(tab, nil); !errors.Is(err, errFilesNothing) {
		t.Fatalf("nothing: %v", err)
	}
	if err := s.FilesDragOut(tab, []string{"rel"}); !errors.Is(err, errBadInput) {
		t.Fatalf("relative: %v", err)
	}
	if err := s.FilesDragOut("nope", []string{"/x"}); !errors.Is(err, errFilesTabNotFound) {
		t.Fatalf("no tab: %v", err)
	}

	ft, err := s.filesTab(tab)
	if err != nil {
		t.Fatal(err)
	}
	items, srcs, err := ft.dragPlan([]string{path.Join(home, "d"), path.Join(home, "c.txt")})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{`d\a.bin`: a, `d\sub\b.bin`: b, "c.txt": c}
	src := &dragSource{t: ft, srcs: srcs}
	files := 0
	for i, it := range items {
		if it.Dir {
			continue
		}
		files++
		w, ok := want[it.Path]
		if !ok || it.Size != int64(len(w)) {
			t.Fatalf("item %+v", it)
		}
		r := oledrag.NewReader(it.Size, func() (oledrag.File, error) { return src.Open(i) })
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, w) {
			t.Fatalf("%s: %d bytes, %v", it.Path, len(got), err)
		}
		if it.Path == `d\a.bin` {
			if _, err := r.Seek(123_456, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 1000)
			if _, err := io.ReadFull(r, buf); err != nil || !bytes.Equal(buf, a[123_456:124_456]) {
				t.Fatalf("after seek: %v", err)
			}
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if files != 3 || len(items) != 5 {
		t.Fatalf("%d files of %d items", files, len(items))
	}
	if _, err := src.Open(99); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("bad index: %v", err)
	}

	// A file removed after the plan reports not found; a missing root plans nothing.
	if err := os.Remove(filepath.Join(s.root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		if it.Path == "c.txt" {
			if _, err := src.Open(i); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("removed file: %v", err)
			}
		}
	}
	if _, _, err := ft.dragPlan([]string{path.Join(home, "missing")}); !errors.Is(err, errDragOutEmpty) {
		t.Fatalf("missing root: %v", err)
	}

	// After the tab closes, a stream reports a lost connection.
	if err := s.FilesClose(tab); err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		if it.Path == `d\a.bin` {
			if _, err := src.Open(i); !errors.Is(err, oledrag.ErrLost) {
				t.Fatalf("closed tab: %v", err)
			}
		}
	}
}

func TestDragOutStep(t *testing.T) {
	for _, c := range []struct {
		ev               dragEvent
		down, outside    bool
		wantOut, wantEnd bool
	}{
		{dragTick, true, false, false, false},   // dragging inside
		{dragTick, true, true, true, false},     // outside before the watch heard the leave
		{dragTick, false, false, false, true},   // dropped or cancelled somewhere
		{dragTick, false, true, false, true},    // released outside
		{dragLeft, true, true, true, false},     // left the window with the button held
		{dragLeft, true, false, false, true},    // Esc over the window
		{dragLeft, false, true, false, true},    // released outside the window
		{dragDropped, true, false, false, true}, // dropped on the list
		{dragDropped, false, true, false, true},
	} {
		out, end := dragOutStep(c.ev, c.down, c.outside)
		if out != c.wantOut || end != c.wantEnd {
			t.Errorf("%+v: out %v end %v", c, out, end)
		}
	}
}
