package app

import "testing"

// sinkRecorder stands in for the Wails event manager.
type sinkRecorder struct {
	name string
	data []any
}

func (s *sinkRecorder) Emit(name string, data ...any) bool {
	s.name, s.data = name, data
	return false
}

// Wails v3 wraps several arguments into a slice; the payload must go as one
// argument so the frontend receives it as event.data unchanged.
func TestWailsEmitterSendsPayloadAsSingleArgument(t *testing.T) {
	s := &sinkRecorder{}
	wailsEmitter{s}.Emit("term:data:1", "aGk=")
	if s.name != "term:data:1" || len(s.data) != 1 || s.data[0] != "aGk=" {
		t.Fatalf("got %q %#v", s.name, s.data)
	}

	payload := []string{"a", "b"}
	wailsEmitter{s}.Emit("fwd:state:1", payload)
	if len(s.data) != 1 {
		t.Fatalf("slice payload split into %d arguments", len(s.data))
	}
}

func TestEventsBeforeStartupAreDropped(t *testing.T) {
	a := &App{}
	a.events().Emit("term:data:1", "x") // must not panic
}
