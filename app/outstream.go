package app

import (
	"encoding/base64"
	"sync"
	"time"
)

// flushInterval is the longest time terminal output waits before it is sent to the frontend.
const flushInterval = 8 * time.Millisecond

// maxHeldBytes caps the output kept for a view that has not attached yet; the oldest is dropped.
var maxHeldBytes = 4 << 20

// droppedMarker opens the stream of a view whose oldest held output was dropped.
const droppedMarker = "[… начало вывода пропущено …]\r\n"

// heldEvent is an event waiting for the view: terminal bytes (data != nil) or a state payload.
type heldEvent struct {
	name    string
	payload any
	data    []byte
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

	mu        sync.Mutex
	buf       []byte
	timer     *time.Timer
	attached  bool
	held      []heldEvent
	heldBytes int // terminal bytes in held
	dropped   bool
	closed    bool
}

func newOutStream(e emitter, id string) *outStream {
	return newOutStreamFor(e, "term:data:"+id)
}

// newOutStreamFor is an outStream whose output goes out as dataEvent (a forward's mini-terminal).
func newOutStreamFor(e emitter, dataEvent string) *outStream {
	return &outStream{emit: e, dataEvent: dataEvent}
}

// Write implements io.Writer for the Prompter and the shell output. It never blocks on the UI.
func (o *outStream) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return len(b), nil
	}
	o.buf = append(o.buf, b...)
	if o.attached {
		if o.timer == nil {
			o.timer = time.AfterFunc(flushInterval, o.flush)
		}
	} else {
		o.trimLocked()
	}
	return len(b), nil
}

// trimLocked drops the oldest held output beyond maxHeldBytes; the caller holds mu.
func (o *outStream) trimLocked() {
	over := o.heldBytes + len(o.buf) - maxHeldBytes
	for i := 0; over > 0 && i < len(o.held); i++ {
		d := o.held[i].data
		if d == nil {
			continue
		}
		n := min(over, len(d))
		o.held[i].data = d[n:]
		o.heldBytes -= n
		over -= n
		o.dropped = true
	}
	if over > 0 {
		o.buf = o.buf[over:]
		o.dropped = true
	}
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
		o.held = append(o.held, heldEvent{name: o.dataEvent, data: b})
		o.heldBytes += len(b)
	}
	o.mu.Unlock()
	if len(b) > 0 && attached {
		o.emitData(b)
	}
}

func (o *outStream) emitData(b []byte) {
	o.emit.Emit(o.dataEvent, base64.StdEncoding.EncodeToString(b))
}

// Event flushes pending output, then emits name. last stops the stream after it.
func (o *outStream) Event(name string, payload any, last bool) {
	o.emitMu.Lock()
	defer o.emitMu.Unlock()
	o.flushLocked()
	o.mu.Lock()
	attached := o.attached
	if !attached {
		o.held = append(o.held, heldEvent{name: name, payload: payload})
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
	held, dropped := o.held, o.dropped
	o.held, o.heldBytes = nil, 0
	o.mu.Unlock()
	if dropped {
		o.emitData([]byte(droppedMarker))
	}
	for _, e := range held {
		switch {
		case e.data == nil:
			o.emit.Emit(e.name, e.payload)
		case len(e.data) > 0:
			o.emitData(e.data)
		}
	}
	o.flushLocked()
}
