package sshclient

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// execKillGrace is how long a command gets after SIGTERM before its session is closed.
const execKillGrace = 2 * time.Second

// ExecResult is the outcome of a finished (or interrupted) command. Stdout and Stderr hold
// at most the last limit bytes of each stream, as valid UTF-8.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
	Truncated      bool // either stream lost its beginning
	TimedOut       bool // the context deadline ended the command
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu        sync.Mutex
	limit     int
	buf       []byte
	truncated bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) >= b.limit {
		if len(p) > b.limit || len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return n, nil
	}
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.truncated = true
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return n, nil
}

// text returns the kept tail as valid UTF-8 and whether anything was dropped.
func (b *tailBuffer) text() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.ToValidUTF8(string(b.buf), "�"), b.truncated
}

// Exec runs command on a new session of c and waits for it. A non-zero exit code is a result,
// not an error. When ctx ends, the command gets SIGTERM, then its session is closed after
// execKillGrace; a deadline sets TimedOut, a cancellation returns context.Canceled together
// with the output collected so far. The client c is shared and is never closed here.
func Exec(ctx context.Context, c *ssh.Client, command string, limit int) (ExecResult, error) {
	if limit < 1 {
		limit = 1
	}
	sess, err := c.NewSession()
	if err != nil {
		return ExecResult{}, err
	}
	defer func() { _ = sess.Close() }() //nolint:gosec // G104: the session is being discarded
	stdout, stderr := &tailBuffer{limit: limit}, &tailBuffer{limit: limit}
	sess.Stdout, sess.Stderr = stdout, stderr
	if err := sess.Start(command); err != nil {
		return ExecResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	var waitErr error
	interrupted := false
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		interrupted = true
		_ = sess.Signal(ssh.SIGTERM) //nolint:gosec // G104: the session is closed after the grace
		select {
		case waitErr = <-done:
		case <-time.After(execKillGrace):
			_ = sess.Close() //nolint:gosec // G104: the session is being discarded
			waitErr = <-done
		}
	}

	res := ExecResult{ExitCode: -1}
	res.Stdout, res.Truncated = stdout.text()
	var errTrunc bool
	res.Stderr, errTrunc = stderr.text()
	res.Truncated = res.Truncated || errTrunc

	var exitErr *ssh.ExitError
	switch {
	case waitErr == nil:
		res.ExitCode = 0
	case errors.As(waitErr, &exitErr):
		res.ExitCode = exitErr.ExitStatus()
	case !interrupted:
		// includes *ssh.ExitMissingError: the channel closed without a status
		return res, waitErr
	}
	if interrupted {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.TimedOut = true
			return res, nil
		}
		return res, ctx.Err()
	}
	return res, nil
}
