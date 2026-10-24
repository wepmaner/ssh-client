package app

import (
	"encoding/base64"
	"sync"
	"time"
)

// flushInterval is the longest time terminal output waits before it is sent to the frontend.
const flushInterval = 8 * time.Millisecond

type heldEvent struct {
	name    string
	payload any
}

// outStream batches a session's terminal output into term:data events (base64) at most every
// flushInterval and orders them with the session's state events.
//
// Until the view attaches (its first SessionResize) nothing is emitted: the frontend learns the
// session id only when SessionOpen returns and subscribes after that, so earlier events are held.
type outStream struct {
	emit      emitter
	dataEvent string

	emitMu sync.Mutex // serialises emissions so data and state events keep their order

	mu       sync.Mutex
	buf      []byte
	timer    *time.Timer
	attached bool
	held     []heldEvent
	closed   bool
}

func newOutStream(e emitter, id string) *outStream {
	return &outStream{emit: e, dataEvent: "term:data:" + id}
}

// Write implements io.Writer for the Prompter and the shell output. It never blocks on the UI.
func (o *outStream) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return len(b), nil
	}
	o.buf = append(o.buf, b...)
	if o.attached && o.timer == nil {
		o.timer = time.AfterFunc(flushInterval, o.flush)
	}
	return len(b), nil
}

func (o *outStream) flush() {
	o.emitMu.Lock()
	defer o.emitMu.Unlock()
	o.flushLocked()
}

// flushLocked sends (or holds) the buffered output; the caller holds emitMu.
func (o *outStream) flushLocked() {
	o.mu.Lock()
	b := o.buf
	o.buf = nil
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	attached := o.attached
	if len(b) > 0 && !attached {
		o.held = append(o.held, heldEvent{o.dataEvent, base64.StdEncoding.EncodeToString(b)})
	}
	o.mu.Unlock()
	if len(b) > 0 && attached {
		o.emit.Emit(o.dataEvent, base64.StdEncoding.EncodeToString(b))
	}
}

// Event flushes pending output, then emits name. last stops the stream after it.
func (o *outStream) Event(name string, payload any, last bool) {
	o.emitMu.Lock()
	defer o.emitMu.Unlock()
	o.flushLocked()
	o.mu.Lock()
	attached := o.attached
	if !attached {
		o.held = append(o.held, heldEvent{name, payload})
	}
	if last {
		o.closed = true
	}
	o.mu.Unlock()
	if attached {
		o.emit.Emit(name, payload)
	}
}

// Attach releases held events in order and switches to live emission. Repeated calls do nothing.
func (o *outStream) Attach() {
	o.emitMu.Lock()
	defer o.emitMu.Unlock()
	o.mu.Lock()
	if o.attached {
		o.mu.Unlock()
		return
	}
	o.attached = true
	held := o.held
	o.held = nil
	o.mu.Unlock()
	for _, e := range held {
		o.emit.Emit(e.name, e.payload)
	}
	o.flushLocked()
}
