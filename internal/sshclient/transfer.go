package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var (
	// ErrExists is returned when the target already exists and overwrite was not asked for.
	ErrExists = errors.New("файл уже существует; чтобы заменить его, нужно разрешить перезапись")
	// ErrNoSFTP is returned when the server does not offer the sftp subsystem.
	ErrNoSFTP = errors.New("на сервере недоступен SFTP, передача файлов невозможна")
)

// transferHook, when set by a test, is called with the running byte count of each transfer
// on the local side of the copy.
var transferHook func(written int64)

// tempName returns the name of the temporary file next to target.
func tempName(target string) string {
	return target + ".sshclient-" + uuid.NewString() + ".tmp"
}

// openSFTP starts an sftp session on its own channel of c and gives up when ctx ends, even if
// the server never answers. The returned function closes the session and the sftp client,
// never c.
func openSFTP(ctx context.Context, c *ssh.Client) (*sftp.Client, func(), error) {
	type opened struct {
		sc    *sftp.Client
		close func()
		err   error
	}
	ch := make(chan opened, 1)
	go func() {
		sc, cl, err := openSFTPNow(ctx, c)
		ch <- opened{sc, cl, err}
	}()
	select {
	case r := <-ch:
		return r.sc, r.close, r.err
	case <-ctx.Done():
		go func() { // whatever the handshake yields later is closed at once
			if r := <-ch; r.close != nil {
				r.close()
			}
		}()
		return nil, nil, ctx.Err()
	}
}

func openSFTPNow(ctx context.Context, c *ssh.Client) (*sftp.Client, func(), error) {
	sess, err := c.NewSession()
	if err != nil {
		return nil, nil, err
	}
	// ending ctx closes the session, which unblocks a handshake the server leaves unanswered
	stop := context.AfterFunc(ctx, func() { _ = sess.Close() }) //nolint:gosec // G104: the session is being discarded
	defer stop()
	fail := func(err error) (*sftp.Client, func(), error) {
		_ = sess.Close() //nolint:gosec // G104: the session is being discarded
		return nil, nil, fmt.Errorf("%w (%v)", ErrNoSFTP, err)
	}
	w, err := sess.StdinPipe()
	if err != nil {
		return fail(err)
	}
	r, err := sess.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	if err := sess.RequestSubsystem("sftp"); err != nil {
		return fail(err)
	}
	sc, err := sftp.NewClientPipe(r, w)
	if err != nil {
		return fail(err)
	}
	return sc, func() {
		_ = sess.Close() //nolint:gosec // G104: the session is being discarded
		_ = sc.Close()   //nolint:gosec // G104: the client is being discarded
	}, nil
}

// ctxReader and ctxWriter stop a copy when ctx ends and count the bytes on the local side.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
	n   int64
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if transferHook != nil {
		transferHook(r.n)
	}
	return n, err
}

type ctxWriter struct {
	ctx context.Context
	w   io.Writer
	n   int64
}

func (w *ctxWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	if transferHook != nil {
		transferHook(w.n)
	}
	return n, err
}

// Upload copies the local file to remote through a temporary file next to remote and renames
// it into place, so a broken transfer never leaves a half-written target. It returns the number
// of bytes sent. The client c is shared and is never closed here.
func Upload(ctx context.Context, c *ssh.Client, local, remote string, overwrite bool) (int64, error) {
	src, err := os.Open(local) //nolint:gosec // G304: path checked by checkLocalPath
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }() //nolint:gosec // G104: read-only file, close errors are irrelevant
	if st, err := src.Stat(); err != nil {
		return 0, err
	} else if st.IsDir() {
		return 0, fmt.Errorf("%s: это папка, а не файл", local)
	}

	sc, closeSFTP, err := openSFTP(ctx, c)
	if err != nil {
		return 0, err
	}
	var once sync.Once
	closeOnce := func() { once.Do(closeSFTP) }
	defer closeOnce()
	stop := context.AfterFunc(ctx, closeOnce)
	defer stop()

	if !overwrite {
		if _, err := sc.Lstat(remote); err == nil {
			return 0, ErrExists
		}
	}
	tmp := tempName(remote)
	n, err := uploadTo(ctx, sc, src, tmp)
	if err == nil {
		if overwrite {
			err = sc.PosixRename(tmp, remote)
		} else {
			err = sc.Rename(tmp, remote) // fails if remote appeared meanwhile
			if err != nil {
				if _, serr := sc.Lstat(remote); serr == nil {
					err = ErrExists
				}
			}
		}
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		removeRemote(c, tmp)
		return 0, err
	}
	return n, nil
}

func uploadTo(ctx context.Context, sc *sftp.Client, src io.Reader, tmp string) (int64, error) {
	dst, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, err
	}
	cr := &ctxReader{ctx: ctx, r: src}
	_, err = io.Copy(dst, cr)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return cr.n, err
}

// removeGrace bounds the cleanup of a leftover remote temporary file.
const removeGrace = 2 * time.Second

// removeRemote deletes a leftover temporary file over a fresh sftp session (the one that wrote
// it may be closed). Best effort and bounded by removeGrace: if the connection is gone or
// stalled, there is nothing to do.
func removeRemote(c *ssh.Client, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), removeGrace)
	defer cancel()
	sc, closeSFTP, err := openSFTP(ctx, c)
	if err != nil {
		return
	}
	defer closeSFTP()
	stop := context.AfterFunc(ctx, closeSFTP) // unblocks Remove on a stalled connection
	defer stop()
	_ = sc.Remove(name) //nolint:gosec // G104: best-effort cleanup of a temporary file
}

// Download copies the remote file to local through a temporary file next to local and renames
// it into place, so a broken transfer leaves the previous target untouched and no temporary
// file behind. It returns the number of bytes received. The client c is shared and is never
// closed here.
func Download(ctx context.Context, c *ssh.Client, remote, local string, overwrite bool) (int64, error) {
	if !overwrite {
		if _, err := os.Lstat(local); err == nil {
			return 0, ErrExists
		}
	}
	sc, closeSFTP, err := openSFTP(ctx, c)
	if err != nil {
		return 0, err
	}
	var once sync.Once
	closeOnce := func() { once.Do(closeSFTP) }
	defer closeOnce()
	stop := context.AfterFunc(ctx, closeOnce)
	defer stop()

	src, err := sc.Open(remote)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }() //nolint:gosec // G104: read-only file, close errors are irrelevant

	tmp := tempName(local)
	n, err := downloadTo(ctx, src, tmp)
	if err == nil {
		err = placeLocal(tmp, local, overwrite)
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		_ = os.Remove(tmp) //nolint:gosec // G104: best-effort cleanup after the primary error
		return 0, err
	}
	return n, nil
}

// placeLocal moves tmp to target. Without overwrite it links first, which fails if target
// exists even when it appeared a moment ago; where hard links are unsupported (FAT) it falls
// back to a check and a rename.
func placeLocal(tmp, target string, overwrite bool) error {
	if overwrite {
		return os.Rename(tmp, target)
	}
	err := os.Link(tmp, target)
	switch {
	case err == nil:
		_ = os.Remove(tmp) //nolint:gosec // G104: the file is linked, the temporary name is only clutter
		return nil
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	}
	if _, serr := os.Lstat(target); serr == nil {
		return ErrExists
	}
	return os.Rename(tmp, target)
}

func downloadTo(ctx context.Context, src io.Reader, tmp string) (int64, error) {
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path checked by checkLocalPath
	if err != nil {
		return 0, err
	}
	cw := &ctxWriter{ctx: ctx, w: dst}
	_, err = io.Copy(cw, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return cw.n, err
}
