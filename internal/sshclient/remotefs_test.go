package sshclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/sshclient/sshtest"
)

// sftpClient is an sftp session to a test server whose sftp root is root; paths are relative to it.
func sftpClient(t *testing.T, root string) *sftp.Client {
	t.Helper()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: root})
	return sftpClient2(t, c)
}

func mustWrite(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListEntries(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "d", "a.txt"), "hello")
	mustWrite(t, filepath.Join(root, ".hidden"), "x")
	sc := sftpClient(t, root)
	got, err := List(sc, ".")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]RemoteEntry{}
	for _, e := range got {
		byName[e.Name] = e
	}
	if d := byName["d"]; !d.Dir || d.Link || !strings.HasPrefix(d.Mode, "d") || len(d.Mode) != 10 {
		t.Fatalf("folder entry %+v", d)
	}
	if h := byName[".hidden"]; h.Dir || h.Size != 1 || h.Mode[0] != '-' || h.ModTime.IsZero() {
		t.Fatalf("file entry %+v", h)
	}
	if _, err := List(sc, "missing"); err == nil || !strings.Contains(err.Error(), "Не найдено") {
		t.Fatalf("missing folder: %v", err)
	}
}

// memSFTP is an sftp client of an in-memory server, which has symlinks on every OS.
func memSFTP(t *testing.T) *sftp.Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv := sftp.NewRequestServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw}, sftp.InMemHandler())
	go func() { _ = srv.Serve() }() //nolint:gosec // G104: ends when the pipes close
	sc, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sw.Close()  //nolint:gosec // G104: test cleanup; ends the client's reader
		_ = sc.Close()  //nolint:gosec // G104: test cleanup
		_ = srv.Close() //nolint:gosec // G104: test cleanup
	})
	return sc
}

func memWrite(t *testing.T, sc *sftp.Client, p, text string) {
	t.Helper()
	f, err := sc.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func memLinks(t *testing.T) *sftp.Client {
	t.Helper()
	sc := memSFTP(t)
	if err := sc.MkdirAll("/d"); err != nil {
		t.Fatal(err)
	}
	memWrite(t, sc, "/d/a.txt", "aaa")
	memWrite(t, sc, "/f.txt", "ff")
	for _, l := range [][2]string{{"/d", "/d/loop"}, {"/f.txt", "/d/flink"}, {"/d", "/toDir"}, {"/gone", "/broken"}} {
		if err := sc.Symlink(l[0], l[1]); err != nil {
			t.Fatal(err)
		}
	}
	return sc
}

func TestListLinks(t *testing.T) {
	sc := memLinks(t)
	got, err := List(sc, "/")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range got {
		switch e.Name {
		case "toDir":
			seen++
			if !e.Link || !e.Dir || e.Broken || e.Mode[0] != 'l' {
				t.Fatalf("link to folder %+v", e)
			}
		case "broken":
			seen++
			if !e.Link || !e.Broken {
				t.Fatalf("broken link %+v", e)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("entries %+v", got)
	}
}

func TestPlanRemoteSkipsFolderLinks(t *testing.T) {
	sc := memLinks(t)
	plan, err := PlanRemote(context.Background(), sc, []string{"/d"}, func(p string, err error) { t.Errorf("%s: %v", p, err) })
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, it := range plan {
		rels = append(rels, it.Rel)
		if it.Rel == "d/flink" && (it.Dir || it.Size != 2) {
			t.Fatalf("file link %+v", it)
		}
	}
	slices.Sort(rels)
	if !slices.Equal(rels, []string{"d", "d/a.txt", "d/flink"}) {
		t.Fatalf("plan %v", rels)
	}
	// A chosen link follows its target.
	plan, err = PlanRemote(context.Background(), sc, []string{"/toDir"}, func(string, error) {})
	if err != nil || len(plan) == 0 || !plan[0].Dir || plan[0].Rel != "toDir" {
		t.Fatalf("chosen link %+v %v", plan, err)
	}
}

func TestRemoveTreeDoesNotFollowLinks(t *testing.T) {
	sc := memLinks(t)
	if err := RemoveTree(context.Background(), sc, "/toDir", func(p string, err error) {
		if err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Stat("/d/a.txt"); err != nil {
		t.Fatalf("the link's target lost: %v", err)
	}
}

func TestPlanLocalSkipsJunctions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows thing")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "d", "a.txt"), "aaa")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "d", "loop"), filepath.Join(root, "d")).CombinedOutput() //nolint:gosec // G204: test temp paths
	if err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	plan, err := PlanLocal(context.Background(), []string{filepath.Join(root, "d")}, func(p string, err error) { t.Errorf("%s: %v", p, err) })
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan %+v", plan)
	}
}

func TestModeString(t *testing.T) {
	if got := ModeString(os.ModeDir | 0o755); got != "drwxr-xr-x" {
		t.Fatal(got)
	}
	if got := ModeString(0o640); got != "-rw-r-----" {
		t.Fatal(got)
	}
	if got := ModeString(os.ModeSymlink | 0o777); got != "lrwxrwxrwx" {
		t.Fatal(got)
	}
}

func TestMkdirAndRenameNoReplace(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "b.txt"), "b")
	sc := sftpClient(t, root)

	p, err := Mkdir(sc, ".", "new")
	if err != nil || p != "new" {
		t.Fatalf("Mkdir: %q %v", p, err)
	}
	if st, err := os.Stat(filepath.Join(root, "new")); err != nil || !st.IsDir() {
		t.Fatal("folder not created")
	}
	if _, err := Mkdir(sc, ".", "new"); !errors.Is(err, ErrNameTaken) || !strings.Contains(err.Error(), "Уже есть") {
		t.Fatalf("second Mkdir: %v", err)
	}
	if _, err := Mkdir(sc, ".", "x/y"); !errors.Is(err, ErrBadName) {
		t.Fatalf("bad name: %v", err)
	}
	if err := RenameNoReplace(sc, "a.txt", "b.txt"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename over a file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(b) != "b" { //nolint:gosec // G304: test temp path
		t.Fatalf("target replaced: %q", b)
	}
	if err := RenameNoReplace(sc, "a.txt", "new/c.txt"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new", "c.txt")); string(b) != "a" { //nolint:gosec // G304: test temp path
		t.Fatalf("moved file %q", b)
	}
}

func TestRemoveTree(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "d", "a.txt"), "a")
	mustWrite(t, filepath.Join(root, "d", "e", "b.txt"), "b")
	mustWrite(t, filepath.Join(root, "keep.txt"), "k")
	sc := sftpClient(t, root)
	if n := CountTree(context.Background(), sc, "d"); n != 4 {
		t.Fatalf("CountTree %d", n)
	}
	var removed []string
	err := RemoveTree(context.Background(), sc, "d", func(p string, err error) {
		if err != nil {
			t.Errorf("%s: %v", p, err)
		}
		removed = append(removed, p)
	})
	if err != nil || len(removed) != 4 || removed[len(removed)-1] != "d" {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if names := readDirNames(t, root); !slices.Equal(names, []string{"keep.txt"}) {
		t.Fatalf("left %v", names)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RemoveTree(ctx, sc, "keep.txt", func(string, error) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestPlanLocal(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "d", "a.txt"), "aaa")
	mustWrite(t, filepath.Join(root, "d", "s", "b.txt"), "b")
	plan, err := PlanLocal(context.Background(), []string{filepath.Join(root, "d")}, func(p string, err error) { t.Errorf("%s: %v", p, err) })
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var rels []string
	for _, it := range plan {
		rels = append(rels, it.Rel)
		total += it.Size
	}
	slices.Sort(rels)
	if !slices.Equal(rels, []string{"d", "d/a.txt", "d/s", "d/s/b.txt"}) || total != 4 {
		t.Fatalf("plan %v total %d", rels, total)
	}
}

// partStable waits until the size of p stops changing: writes still in flight after a cancel land.
func partStable(t *testing.T, p string) {
	t.Helper()
	var last int64 = -1
	for range 50 {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("part file missing: %v", err)
		}
		if st.Size() == last {
			return
		}
		last = st.Size()
		time.Sleep(20 * time.Millisecond)
	}
}

func TestUploadFileResumes(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	src := filepath.Join(localDir, "big.bin")
	data := writeRandom(t, src, 3_000_000)
	mtime := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(src, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	acked := map[string]int64{}
	var parts []string

	sc, closeSFTP, err := OpenSFTP(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cp := Copy{JobID: "j1", Acked: acked, OnPart: func(p string) { parts = append(parts, p) },
		Progress: func(pos int64) {
			if pos > 1_000_000 {
				cancel()
			}
		}}
	if _, err := UploadFile(ctx, sc, src, "big.bin", cp); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted upload: %v", err)
	}
	closeSFTP()
	if _, err := os.Stat(filepath.Join(remoteDir, "big.bin")); err == nil {
		t.Fatal("a half-written file under the real name")
	}
	part := filepath.Join(remoteDir, "big.bin.sshclient-j1.part")
	partStable(t, part)
	if acked["big.bin.sshclient-j1.part"] < 1_000_000 {
		t.Fatalf("acked %v", acked)
	}

	sc = sftpClient2(t, c)
	var first int64 = -1
	cp.Progress = func(pos int64) {
		if first < 0 {
			first = pos
		}
	}
	res, err := UploadFile(context.Background(), sc, src, "big.bin", cp)
	if err != nil || res != Copied {
		t.Fatalf("resumed upload: %s %v", res, err)
	}
	if first < 1_000_000 {
		t.Fatalf("resumed from %d, not from the part", first)
	}
	got, err := os.ReadFile(filepath.Join(remoteDir, "big.bin")) //nolint:gosec // G304: test temp path
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("content differs: %v", err)
	}
	if st, err := os.Stat(filepath.Join(remoteDir, "big.bin")); err != nil || !st.ModTime().Equal(mtime) {
		t.Fatalf("mtime %v", st.ModTime())
	}
	if names := readDirNames(t, remoteDir); len(names) != 1 {
		t.Fatalf("left on the server: %v", names)
	}
	if len(parts) == 0 || parts[0] != "big.bin.sshclient-j1.part" {
		t.Fatalf("parts %v", parts)
	}
}

func sftpClient2(t *testing.T, c *ssh.Client) *sftp.Client {
	t.Helper()
	sc, closeSFTP, err := OpenSFTP(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeSFTP)
	return sc
}

func TestDownloadFileResumes(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	data := writeRandom(t, filepath.Join(remoteDir, "big.bin"), 3_000_000)
	mtime := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(remoteDir, "big.bin"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(localDir, "big.bin")
	sc := sftpClient2(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cp := Copy{JobID: "j2", Progress: func(pos int64) {
		if pos > 1_000_000 {
			cancel()
		}
	}}
	if _, err := DownloadFile(ctx, sc, "big.bin", dst, cp); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted download: %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a half-written file under the real name")
	}
	var first int64 = -1
	cp.Progress = func(pos int64) {
		if first < 0 {
			first = pos
		}
	}
	if res, err := DownloadFile(context.Background(), sc, "big.bin", dst, cp); err != nil || res != Copied {
		t.Fatalf("resumed download: %s %v", res, err)
	}
	if first < 1_000_000 {
		t.Fatalf("resumed from %d", first)
	}
	got, err := os.ReadFile(dst) //nolint:gosec // G304: test temp path
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("content differs: %v", err)
	}
	if st, err := os.Stat(dst); err != nil || !st.ModTime().Equal(mtime) {
		t.Fatalf("mtime %v", st.ModTime())
	}
	if names := readDirNames(t, localDir); len(names) != 1 {
		t.Fatalf("left locally: %v", names)
	}
}

func TestCopySkipAndReplace(t *testing.T) {
	remoteDir, localDir := t.TempDir(), t.TempDir()
	_, c := execClient(t, sshtest.Opts{SFTPRoot: remoteDir})
	sc := sftpClient2(t, c)
	mustWrite(t, filepath.Join(localDir, "a.txt"), "new")
	mustWrite(t, filepath.Join(remoteDir, "a.txt"), "old")

	res, err := UploadFile(context.Background(), sc, filepath.Join(localDir, "a.txt"), "a.txt", Copy{JobID: "s"})
	if err != nil || res != Skipped {
		t.Fatalf("skip: %s %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(remoteDir, "a.txt")); string(b) != "old" { //nolint:gosec // G304: test temp path
		t.Fatalf("skipped file changed: %q", b)
	}
	res, err = UploadFile(context.Background(), sc, filepath.Join(localDir, "a.txt"), "a.txt", Copy{JobID: "r", Replace: true})
	if err != nil || res != Copied {
		t.Fatalf("replace: %s %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(remoteDir, "a.txt")); string(b) != "new" { //nolint:gosec // G304: test temp path
		t.Fatalf("not replaced: %q", b)
	}

	mustWrite(t, filepath.Join(remoteDir, "b.txt"), "server")
	mustWrite(t, filepath.Join(localDir, "b.txt"), "local")
	if res, err := DownloadFile(context.Background(), sc, "b.txt", filepath.Join(localDir, "b.txt"), Copy{JobID: "s"}); err != nil || res != Skipped {
		t.Fatalf("download skip: %s", res)
	}
	if res, err := DownloadFile(context.Background(), sc, "b.txt", filepath.Join(localDir, "b.txt"), Copy{JobID: "r", Replace: true}); err != nil || res != Copied {
		t.Fatalf("download replace: %s %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(localDir, "b.txt")); string(b) != "server" { //nolint:gosec // G304: test temp path
		t.Fatalf("not replaced: %q", b)
	}
	if _, err := DownloadFile(context.Background(), sc, "nope.txt", filepath.Join(localDir, "nope.txt"), Copy{JobID: "x"}); err == nil || !strings.Contains(err.Error(), "Не найдено") {
		t.Fatalf("missing remote file: %v", err)
	}
}

func TestSafeLocalName(t *testing.T) {
	cases := map[string]string{
		"ok.txt":     "ok.txt",
		"a:b?.txt":   "a_b_.txt",
		`x\y`:        "x_y",
		"con":        "_con",
		"NUL.tar.gz": "_NUL.tar.gz",
		"COM1":       "_COM1",
		"name. ":     "name__",
		".":          "_",
		"..":         "__",
		"":           "_",
		"tab\tname":  "tab_name",
		"Файл.txt":   "Файл.txt",
		"console":    "console",
	}
	for in, want := range cases {
		got, changed := SafeLocalName(in)
		if got != want || changed != (in != want) {
			t.Errorf("SafeLocalName(%q) = %q, %v; want %q", in, got, changed, want)
		}
	}
}

func TestLocalTargetStaysInside(t *testing.T) {
	root := t.TempDir()
	got, renamed, err := LocalTarget(root, "d/a:b.txt")
	if err != nil || got != filepath.Join(root, "d", "a_b.txt") || renamed != 1 {
		t.Fatalf("%q %d %v", got, renamed, err)
	}
	for _, rel := range []string{"..", "../x", "d/../../x", `..\x`, "C:/Windows", "/"} {
		got, _, err := LocalTarget(root, rel)
		if err == nil {
			if err := CheckInside(root, got); err != nil || !strings.HasPrefix(got, root+string(filepath.Separator)) {
				t.Errorf("%q escaped to %q", rel, got)
			}
		}
	}
	if err := CheckInside(root, filepath.Join(root, "..", "x")); !errors.Is(err, ErrOutside) {
		t.Fatalf("outside: %v", err)
	}
}
