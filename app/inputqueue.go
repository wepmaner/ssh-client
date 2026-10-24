package app

import (
	"io"
	"sync"
)

// inputQueue carries keystrokes to the Prompter while a session is connecting.
// Write never blocks, so a UI call returns at once; Read blocks until input arrives.
// After Close, Read drains nothing more and returns io.EOF, which makes a pending prompt
// fail with sshclient.ErrCancelled.
type inputQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputQueue() *inputQueue {
	q := &inputQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Write queues b; after Close it is dropped.
func (q *inputQueue) Write(b []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.buf = append(q.buf, b...)
	q.cond.Signal()
}

// Read implements io.Reader for the Prompter.
func (q *inputQueue) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return 0, io.EOF
	}
	n := copy(p, q.buf)
	q.buf = q.buf[n:]
	return n, nil
}

// Close wakes a blocked Read with io.EOF and returns the input nobody has read yet.
func (q *inputQueue) Close() []byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	rest := q.buf
	q.buf, q.closed = nil, true
	q.cond.Broadcast()
	return rest
}
