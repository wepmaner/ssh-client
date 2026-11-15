package sshclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// FSError is a file operation error with a Russian message; the system error stays as its cause
// for the details.
type FSError struct {
	Msg string
	Err error
}

func (e *FSError) Error() string { return e.Msg }
func (e *FSError) Unwrap() error { return e.Err }

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	// ErrNameTaken is the cause of an FSError when the target name is already taken.
	ErrNameTaken = errors.New("Имя уже занято")
	// ErrBadName is the cause of an FSError for an empty name or one with a slash.
	ErrBadName = errors.New("Недопустимое имя")
)

// FileError turns err of an operation on name into an FSError with a Russian message: no access,
// not found, name taken, connection lost; anything else keeps the system text in the cause.
func FileError(name string, err error) error {
	if err == nil {
		return nil
	}
	var fe *FSError
	if errors.As(err, &fe) {
		return err
	}
	var se *sftp.StatusError
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, fs.ErrPermission):
		return &FSError{Msg: fmt.Sprintf("Нет доступа: %s", name), Err: err}
	case errors.Is(err, fs.ErrNotExist):
		return &FSError{Msg: fmt.Sprintf("Не найдено: %s", name), Err: err}
	case errors.Is(err, fs.ErrExist), errors.Is(err, ErrExists), errors.Is(err, ErrNameTaken):
		return &FSError{Msg: fmt.Sprintf("Уже есть: %s", name), Err: err}
	case errors.Is(err, sftp.ErrSSHFxConnectionLost), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return &FSError{Msg: "Соединение с сервером прервано", Err: err}
	case errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxOpUnsupported:
		return &FSError{Msg: fmt.Sprintf("Сервер не поддерживает операцию: %s", name), Err: err}
	}
	return &FSError{Msg: fmt.Sprintf("Ошибка: %s", name), Err: err}
}

// RemoteEntry is one entry of a remote folder.
type RemoteEntry struct {
	Name    string
	Dir     bool // a folder, or a link to one
	Link    bool
	Broken  bool // a link whose target is missing or unreadable
	Size    int64
	ModTime time.Time
	Mode    string // like ls: drwxr-xr-x, lrwxrwxrwx, -rw-r--r--
}

// ModeString is the ls-style mode of m: a type letter and the nine permission letters.
func ModeString(m fs.FileMode) string {
	t := byte('-')
	switch {
	case m&fs.ModeSymlink != 0:
		t = 'l'
	case m.IsDir():
		t = 'd'
	case m&fs.ModeNamedPipe != 0:
		t = 'p'
	case m&fs.ModeSocket != 0:
		t = 's'
	case m&fs.ModeCharDevice != 0:
		t = 'c'
	case m&fs.ModeDevice != 0:
		t = 'b'
	}
	return string(t) + m.Perm().String()[1:]
}

// List reads the folder dir. Links are followed to tell a link to a folder from a broken one;
// the entry keeps the link's own mode.
func List(sc *sftp.Client, dir string) ([]RemoteEntry, error) {
	infos, err := sc.ReadDir(dir)
	if err != nil {
		return nil, FileError(dir, err)
	}
	out := make([]RemoteEntry, 0, len(infos))
	for _, fi := range infos {
		if fi.Name() == "." || fi.Name() == ".." {
			continue
		}
		e := RemoteEntry{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime(), Mode: ModeString(fi.Mode())}
		if fi.Mode()&fs.ModeSymlink != 0 {
			e.Link = true
			if st, err := sc.Stat(path.Join(dir, fi.Name())); err != nil {
				e.Broken = true
			} else {
				e.Dir = st.IsDir()
				if !e.Dir {
					e.Size = st.Size()
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// CheckName refuses a name that cannot be one folder entry: empty, ".", "..", or with a slash or NUL.
func CheckName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return &FSError{Msg: "Недопустимое имя: " + name, Err: ErrBadName}
	}
	return nil
}

func exists(sc *sftp.Client, p string) bool {
	_, err := sc.Lstat(p)
	return err == nil
}

// Mkdir creates the folder name in dir and returns its path. A taken name is an error.
func Mkdir(sc *sftp.Client, dir, name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	p := path.Join(dir, name)
	if exists(sc, p) {
		return "", FileError(name, ErrNameTaken)
	}
	if err := sc.Mkdir(p); err != nil {
		if exists(sc, p) {
			return "", FileError(name, ErrNameTaken)
		}
		return "", FileError(name, err)
	}
	return p, nil
}

// RenameNoReplace renames from to to and never replaces: a taken target is an error.
func RenameNoReplace(sc *sftp.Client, from, to string) error {
	if path.Clean(from) == path.Clean(to) {
		return nil
	}
	if exists(sc, to) {
		return FileError(path.Base(to), ErrNameTaken)
	}
	if err := sc.Rename(from, to); err != nil {
		if exists(sc, to) {
			return FileError(path.Base(to), ErrNameTaken)
		}
		return FileError(path.Base(from), err)
	}
	return nil
}

// CountTree counts p and everything under it, links not followed. Unreadable folders count as one.
func CountTree(ctx context.Context, sc *sftp.Client, p string) int {
	fi, err := sc.Lstat(p)
	if err != nil || !fi.IsDir() {
		return 1
	}
	n := 1
	infos, err := sc.ReadDir(p)
	if err != nil {
		return n
	}
	for _, c := range infos {
		if ctx.Err() != nil {
			return n
		}
		if c.Name() == "." || c.Name() == ".." {
			continue
		}
		n += CountTree(ctx, sc, path.Join(p, c.Name()))
	}
	return n
}

// RemoveTree deletes p with everything under it; links are deleted, never followed. done is
// called for each entry with its error (nil when deleted); an error does not stop the rest. It
// returns ctx.Err() when ctx ends, or a lost connection, which stops it.
func RemoveTree(ctx context.Context, sc *sftp.Client, p string, done func(p string, err error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := sc.Lstat(p)
	if err != nil {
		if lost(err) {
			return err
		}
		done(p, FileError(p, err))
		return nil
	}
	if fi.IsDir() {
		infos, rerr := sc.ReadDir(p)
		if rerr != nil {
			if lost(rerr) {
				return rerr
			}
			done(p, FileError(p, rerr))
			return nil
		}
		for _, c := range infos {
			if c.Name() == "." || c.Name() == ".." {
				continue
			}
			if err := RemoveTree(ctx, sc, path.Join(p, c.Name()), done); err != nil {
				return err
			}
		}
		err = sc.RemoveDirectory(p)
	} else {
		err = sc.Remove(p)
	}
	if err != nil && lost(err) {
		return err
	}
	done(p, FileError(p, err))
	return nil
}

// lost reports an error of a closed sftp session or connection.
func lost(err error) bool {
	return errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed)
}

// IsLost reports an error after which the sftp session must be opened anew.
func IsLost(err error) bool { return lost(err) }

// PlanItem is one entry of a transfer: a folder to create or a file to copy. Rel is the path under
// the target folder, slash-separated, starting with the name of the chosen item.
type PlanItem struct {
	Src     string
	Rel     string
	Dir     bool
	Size    int64
	ModTime time.Time
}

// PlanRemote walks the chosen remote items into a plan. A chosen link follows its target; inside
// a folder, links to folders are skipped (no cycles) and links to files are copied as files.
// fail is told about an entry that could not be read; the walk goes on. It stops when ctx ends
// or the connection is lost and returns that error.
func PlanRemote(ctx context.Context, sc *sftp.Client, roots []string, fail func(p string, err error)) ([]PlanItem, error) {
	var plan []PlanItem
	var walk func(src, rel string, top bool) error
	walk = func(src, rel string, top bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := sc.Lstat(src)
		if err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			fi, err = sc.Stat(src)
			if err == nil && fi.IsDir() && !top {
				return nil
			}
			if err != nil && !top {
				return nil // a broken link inside a folder
			}
		}
		if err != nil {
			if lost(err) {
				return err
			}
			fail(src, FileError(src, err))
			return nil
		}
		if !fi.IsDir() {
			if !fi.Mode().IsRegular() {
				fail(src, &FSError{Msg: "Не обычный файл, пропущен: " + src, Err: ErrBadName})
				return nil
			}
			plan = append(plan, PlanItem{Src: src, Rel: rel, Size: fi.Size(), ModTime: fi.ModTime()})
			return nil
		}
		plan = append(plan, PlanItem{Src: src, Rel: rel, Dir: true, ModTime: fi.ModTime()})
		infos, err := sc.ReadDir(src)
		if err != nil {
			if lost(err) {
				return err
			}
			fail(src, FileError(src, err))
			return nil
		}
		for _, c := range infos {
			if c.Name() == "." || c.Name() == ".." {
				continue
			}
			if err := walk(path.Join(src, c.Name()), rel+"/"+c.Name(), false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := walk(r, path.Base(r), true); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// PlanLocal is PlanRemote for local items; links and junctions follow the same rules.
func PlanLocal(ctx context.Context, roots []string, fail func(p string, err error)) ([]PlanItem, error) {
	var plan []PlanItem
	var walk func(src, rel string, top bool) error
	walk = func(src, rel string, top bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := os.Lstat(src)
		if err == nil && fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			if !top && fi.Mode()&fs.ModeIrregular != 0 {
				return nil
			}
			fi, err = os.Stat(src)
			if err == nil && fi.IsDir() && !top {
				return nil
			}
			if err != nil && !top {
				return nil
			}
		}
		if err != nil {
			fail(src, FileError(src, err))
			return nil
		}
		if !fi.IsDir() {
			if !fi.Mode().IsRegular() {
				fail(src, &FSError{Msg: "Не обычный файл, пропущен: " + src, Err: ErrBadName})
				return nil
			}
			plan = append(plan, PlanItem{Src: src, Rel: rel, Size: fi.Size(), ModTime: fi.ModTime()})
			return nil
		}
		plan = append(plan, PlanItem{Src: src, Rel: rel, Dir: true, ModTime: fi.ModTime()})
		ents, err := os.ReadDir(src)
		if err != nil {
			fail(src, FileError(src, err))
			return nil
		}
		for _, c := range ents {
			if err := walk(filepath.Join(src, c.Name()), rel+"/"+c.Name(), false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := walk(filepath.Clean(r), filepath.Base(filepath.Clean(r)), true); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// PartName is the temporary name next to target that a job writes before the file is complete.
func PartName(target, jobID string) string {
	return target + ".sshclient-" + jobID + ".part"
}

// Copy describes one file copy of a job.
type Copy struct {
	JobID   string
	Replace bool // an existing target is replaced; otherwise the file is skipped
	// Progress is told the position reached in the file (bytes from its start), from any goroutine
	// of the copy, never concurrently.
	Progress func(pos int64)
	// Acked holds, by remote part name, how much of an upload the server has confirmed; the job
	// keeps it across attempts so a resumed upload never trusts a tail written out of order.
	Acked map[string]int64
	// OnPart is called with a part name before it is first written, for the cleanup of the job.
	OnPart func(part string)
}

// Copy outcomes.
const (
	Copied  = "copied"
	Skipped = "skipped"
)

// uploadChunk and uploadWindow size the pipelined writes of an upload: a write per sftp packet,
// that many in flight.
const (
	uploadChunk  = 32 << 10
	uploadWindow = 64
)

// UploadFile copies the local file to remote through its part file, resuming from what the server
// has confirmed, sets the modification time and moves it into place. An existing target is
// replaced or the file skipped, by c.Replace.
func UploadFile(ctx context.Context, sc *sftp.Client, local, remote string, c Copy) (string, error) {
	if st, err := sc.Lstat(remote); err == nil {
		if !c.Replace {
			return Skipped, nil
		}
		if st.IsDir() {
			return "", &FSError{Msg: "На сервере на этом месте папка: " + remote, Err: ErrNameTaken}
		}
	} else if lost(err) {
		return "", err
	}
	src, err := os.Open(local) //nolint:gosec // G304: a file the user chose
	if err != nil {
		return "", FileError(local, err)
	}
	defer func() { _ = src.Close() }() //nolint:gosec // G104: read-only file, close errors are irrelevant
	lst, err := src.Stat()
	if err != nil {
		return "", FileError(local, err)
	}
	part := PartName(remote, c.JobID)
	var off int64
	if pst, err := sc.Lstat(part); err == nil && !pst.IsDir() {
		off = min(pst.Size(), c.Acked[part], lst.Size())
	} else if err != nil && lost(err) {
		return "", err
	}
	if c.OnPart != nil {
		c.OnPart(part)
	}
	flags := os.O_WRONLY | os.O_CREATE
	if off == 0 {
		flags |= os.O_TRUNC
	}
	dst, err := sc.OpenFile(part, flags)
	if err != nil {
		return "", FileError(remote, err)
	}
	if off > 0 {
		if err := dst.Truncate(off); err != nil {
			_ = dst.Close() //nolint:gosec // G104: the primary error is returned
			return "", FileError(remote, err)
		}
		if _, err := src.Seek(off, io.SeekStart); err != nil {
			_ = dst.Close() //nolint:gosec // G104: the primary error is returned
			return "", FileError(local, err)
		}
	}
	if c.Progress != nil {
		c.Progress(off)
	}
	acked, err := pipeUpload(ctx, dst, src, off, c.Progress)
	if c.Acked != nil {
		c.Acked[part] = acked
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if lost(err) {
			return "", err
		}
		return "", FileError(remote, err)
	}
	_ = sc.Chtimes(part, time.Now(), lst.ModTime()) //nolint:gosec // G104: a server that refuses times still gets the file
	if err := placeRemote(sc, part, remote, c.Replace); err != nil {
		return "", err
	}
	delete(c.Acked, part)
	return Copied, nil
}

// pipeUpload writes src to f from off with up to uploadWindow writes in flight and returns how far
// the server has confirmed every byte.
func pipeUpload(ctx context.Context, f *sftp.File, src io.Reader, off int64, progress func(int64)) (int64, error) {
	type ack struct {
		off  int64
		n    int
		want int
		err  error
	}
	acks := make(chan ack, uploadWindow)
	done := map[int64]int{} // confirmed chunks past the prefix
	prefix, next := off, off
	inflight := 0
	var firstErr error
	eof := false
	for {
		for inflight < uploadWindow && !eof && firstErr == nil {
			if err := ctx.Err(); err != nil {
				firstErr = err
				break
			}
			buf := make([]byte, uploadChunk)
			n, err := io.ReadFull(src, buf)
			if n > 0 {
				inflight++
				go func(o int64, b []byte) {
					m, err := f.WriteAt(b, o)
					acks <- ack{o, m, len(b), err}
				}(next, buf[:n])
				next += int64(n)
			}
			switch {
			case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
				eof = true
			case err != nil:
				firstErr = err
			}
		}
		if inflight == 0 {
			return prefix, firstErr
		}
		select {
		case a := <-acks:
			inflight--
			if a.err == nil && a.n < a.want {
				a.err = io.ErrShortWrite
			}
			if a.err != nil {
				if firstErr == nil {
					firstErr = a.err
				}
				continue
			}
			done[a.off] = a.n
			moved := false
			for n, ok := done[prefix]; ok; n, ok = done[prefix] {
				delete(done, prefix)
				prefix += int64(n)
				moved = true
			}
			if moved && progress != nil {
				progress(prefix)
			}
		case <-ctx.Done():
			// The writes in flight end with the sftp session, which the caller closes.
			return prefix, ctx.Err()
		}
	}
}

// placeRemote moves the finished part to target: over it when replace, otherwise only if the
// name is still free.
func placeRemote(sc *sftp.Client, part, target string, replace bool) error {
	if replace {
		err := sc.PosixRename(part, target)
		if err == nil {
			return nil
		}
		var se *sftp.StatusError
		if !errors.As(err, &se) || se.FxCode() != sftp.ErrSSHFxOpUnsupported {
			return FileError(target, err)
		}
		if st, serr := sc.Lstat(target); serr == nil && !st.IsDir() {
			if err := sc.Remove(target); err != nil {
				return FileError(target, err)
			}
		}
	} else if exists(sc, target) {
		return FileError(target, ErrNameTaken)
	}
	if err := sc.Rename(part, target); err != nil {
		if exists(sc, target) && !replace {
			return FileError(target, ErrNameTaken)
		}
		return FileError(target, err)
	}
	return nil
}

// DownloadFile copies remote to the local file through its part file, resuming from the part's
// size, sets the modification time and moves it into place, replaced or skipped by c.Replace.
func DownloadFile(ctx context.Context, sc *sftp.Client, remote, local string, c Copy) (string, error) {
	if st, err := os.Lstat(local); err == nil {
		if !c.Replace {
			return Skipped, nil
		}
		if st.IsDir() {
			return "", &FSError{Msg: "На компьютере на этом месте папка: " + local, Err: ErrNameTaken}
		}
	}
	src, err := sc.Open(remote)
	if err != nil {
		if lost(err) {
			return "", err
		}
		return "", FileError(remote, err)
	}
	defer func() { _ = src.Close() }() //nolint:gosec // G104: read-only file, close errors are irrelevant
	rst, err := src.Stat()
	if err != nil {
		if lost(err) {
			return "", err
		}
		return "", FileError(remote, err)
	}
	part := PartName(local, c.JobID)
	var off int64
	if pst, err := os.Lstat(part); err == nil && pst.Mode().IsRegular() && pst.Size() <= rst.Size() {
		off = pst.Size()
	}
	if c.OnPart != nil {
		c.OnPart(part)
	}
	flags := os.O_WRONLY | os.O_CREATE
	if off == 0 {
		flags |= os.O_TRUNC
	}
	dst, err := os.OpenFile(part, flags, 0o600) //nolint:gosec // G304: checked by CheckInside
	if err != nil {
		return "", FileError(local, err)
	}
	if off > 0 {
		if _, err = dst.Seek(off, io.SeekStart); err == nil {
			_, err = src.Seek(off, io.SeekStart)
		}
		if err != nil {
			_ = dst.Close() //nolint:gosec // G104: the primary error is returned
			return "", FileError(local, err)
		}
	}
	if c.Progress != nil {
		c.Progress(off)
	}
	w := &progressWriter{ctx: ctx, w: dst, pos: off, progress: c.Progress}
	_, err = io.Copy(w, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if lost(err) {
			return "", err
		}
		return "", FileError(remote, err)
	}
	_ = os.Chtimes(part, time.Now(), rst.ModTime()) //nolint:gosec // G104: the file is complete without its time
	if err := placeLocal(part, local, c.Replace); err != nil {
		return "", FileError(local, err)
	}
	return Copied, nil
}

// progressWriter stops a copy when ctx ends and reports the position.
type progressWriter struct {
	ctx      context.Context
	w        io.Writer
	pos      int64
	progress func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	w.pos += int64(n)
	if w.progress != nil {
		w.progress(w.pos)
	}
	return n, err
}

// EnsureRemoteDir creates the folder p unless it is one already; a file in its place is an error.
func EnsureRemoteDir(sc *sftp.Client, p string) error {
	st, err := sc.Stat(p)
	if err == nil {
		if st.IsDir() {
			return nil
		}
		return &FSError{Msg: "На сервере на месте папки файл: " + p, Err: ErrNameTaken}
	}
	if lost(err) {
		return err
	}
	if err := sc.Mkdir(p); err != nil {
		if st, serr := sc.Stat(p); serr == nil && st.IsDir() {
			return nil
		}
		if lost(err) {
			return err
		}
		return FileError(p, err)
	}
	return nil
}

// EnsureLocalDir is EnsureRemoteDir on the computer.
func EnsureLocalDir(p string) error {
	st, err := os.Stat(p)
	if err == nil {
		if st.IsDir() {
			return nil
		}
		return &FSError{Msg: "На компьютере на месте папки файл: " + p, Err: ErrNameTaken}
	}
	if err := os.Mkdir(p, 0o750); err != nil {
		if st, serr := os.Stat(p); serr == nil && st.IsDir() {
			return nil
		}
		return FileError(p, err)
	}
	return nil
}

// OpenSFTP starts an sftp session on its own channel of c; see openSFTP.
func OpenSFTP(ctx context.Context, c *ssh.Client) (*sftp.Client, func(), error) {
	return openSFTP(ctx, c)
}

// RemoveRemoteFiles deletes leftover part files over a fresh sftp session, best effort and bounded
// by removeGrace in all.
func RemoveRemoteFiles(c *ssh.Client, names []string) {
	if c == nil || len(names) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), removeGrace)
	defer cancel()
	sc, closeSFTP, err := openSFTP(ctx, c)
	if err != nil {
		return
	}
	var once sync.Once
	closeOnce := func() { once.Do(closeSFTP) }
	defer closeOnce()
	stop := context.AfterFunc(ctx, closeOnce) // unblocks Remove on a stalled connection
	defer stop()
	for _, n := range names {
		if ctx.Err() != nil {
			return
		}
		_ = sc.Remove(n) //nolint:gosec // G104: best-effort cleanup of a temporary file
	}
}

// copyReadBuffer is the read size of a copy inside the server: a read that large goes out as
// several sftp requests in flight instead of one round trip per packet.
const copyReadBuffer = 1 << 20

// CopyRemoteFile copies the remote file src to dst on the same server through dst's part file,
// streaming through the client, then sets the modification time and moves it into place. An
// existing dst is replaced or the file skipped, by c.Replace. It does not resume: a part left by an
// earlier run is written anew.
func CopyRemoteFile(ctx context.Context, sc *sftp.Client, src, dst string, c Copy) (string, error) {
	if st, err := sc.Lstat(dst); err == nil {
		if !c.Replace {
			return Skipped, nil
		}
		if st.IsDir() {
			return "", &FSError{Msg: "На сервере на этом месте папка: " + dst, Err: ErrNameTaken}
		}
	} else if lost(err) {
		return "", err
	}
	in, err := sc.Open(src)
	if err != nil {
		if lost(err) {
			return "", err
		}
		return "", FileError(src, err)
	}
	defer func() { _ = in.Close() }() //nolint:gosec // G104: read-only file, close errors are irrelevant
	sst, err := in.Stat()
	if err != nil {
		if lost(err) {
			return "", err
		}
		return "", FileError(src, err)
	}
	part := PartName(dst, c.JobID)
	if c.OnPart != nil {
		c.OnPart(part)
	}
	out, err := sc.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		if lost(err) {
			return "", err
		}
		return "", FileError(dst, err)
	}
	if c.Progress != nil {
		c.Progress(0)
	}
	_, err = pipeUpload(ctx, out, bufioReader(in), 0, c.Progress)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return "", cerr
		}
		if lost(err) {
			return "", err
		}
		return "", FileError(dst, err)
	}
	_ = sc.Chmod(part, sst.Mode().Perm())           //nolint:gosec // G104: a server that refuses modes still gets the file
	_ = sc.Chtimes(part, time.Now(), sst.ModTime()) //nolint:gosec // G104: a server that refuses times still gets the file
	if err := placeRemote(sc, part, dst, c.Replace); err != nil {
		return "", err
	}
	return Copied, nil
}

// CopyName is a free name for a copy of name in dir: «a (копия).txt», «a (копия 2).txt»; a
// folder or a name without an extension gets the suffix at the end.
func CopyName(sc *sftp.Client, dir, name string, isDir bool) (string, error) {
	stem, ext := name, ""
	if i := strings.LastIndex(name, "."); !isDir && i > 0 {
		stem, ext = name[:i], name[i:]
	}
	for n := 1; n < 1000; n++ {
		suffix := " (копия)"
		if n > 1 {
			suffix = fmt.Sprintf(" (копия %d)", n)
		}
		cand := stem + suffix + ext
		if _, err := sc.Lstat(path.Join(dir, cand)); err != nil {
			if lost(err) {
				return "", err
			}
			if errors.Is(err, fs.ErrNotExist) {
				return cand, nil
			}
			return "", FileError(cand, err)
		}
	}
	return "", FileError(name, ErrNameTaken)
}

func bufioReader(r io.Reader) io.Reader { return bufio.NewReaderSize(r, copyReadBuffer) }
