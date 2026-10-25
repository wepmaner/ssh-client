package app

// emitter sends events to the frontend. Tests replace the Wails implementation with a recorder.
type emitter interface {
	Emit(name string, payload any)
}

// eventSink is the part of the Wails v3 event manager (application.App.Event) the emitter uses.
type eventSink interface {
	Emit(name string, data ...any) bool
}

// wailsEmitter emits through the Wails event manager. The payload goes as the
// only argument, so the frontend receives it unchanged as WailsEvent.data.
type wailsEmitter struct{ sink eventSink }

func (w wailsEmitter) Emit(name string, payload any) { w.sink.Emit(name, payload) }

// nopEmitter drops events emitted before ServiceStartup.
type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

func (a *App) events() emitter {
	if a.emit == nil {
		return nopEmitter{}
	}
	return a.emit
}
