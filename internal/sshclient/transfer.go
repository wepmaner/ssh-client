package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

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

// openSFTP starts an sftp session on its own channel of c. The returned function closes the
// session and the sftp client, never c.
func openSFTP(c *ssh.Client) (*sftp.Client, func(), error) {
	sess, err := c.NewSession()
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*sftp.Client, func(), error) {
		_ = sess.Close()
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
		_ = sess.Close()
		_ = sc.Close()
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
	src, err := os.Open(local)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	if st, err := src.Stat(); err != nil {
		return 0, err
	} else if st.IsDir() {
		return 0, fmt.Errorf("%s: это папка, а не файл", local)
	}

	sc, closeSFTP, err := openSFTP(c)
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

// removeRemote deletes a leftover temporary file over a fresh sftp session (the one that wrote
// it may be closed). Best effort: if the connection is gone, there is nothing to do.
func removeRemote(c *ssh.Client, name string) {
	sc, closeSFTP, err := openSFTP(c)
	if err != nil {
		return
	}
	defer closeSFTP()
	_ = sc.Remove(name)
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
	sc, closeSFTP, err := openSFTP(c)
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
	defer func() { _ = src.Close() }()

	tmp := tempName(local)
	n, err := downloadTo(ctx, src, tmp)
	if err == nil {
		if !overwrite {
			if _, serr := os.Lstat(local); serr == nil {
				err = ErrExists
			}
		}
		if err == nil {
			err = os.Rename(tmp, local)
		}
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			err = cerr
		}
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

func downloadTo(ctx context.Context, src io.Reader, tmp string) (int64, error) {
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
