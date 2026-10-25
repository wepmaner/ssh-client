package app

import "github.com/wailsapp/wails/v3/pkg/application"

// raisable is the part of the Wails window a second launch brings forward.
type raisable interface {
	IsMinimised() bool
	Restore()
	Show() application.Window
	Focus()
}

// SecondLaunchHandler returns the OnSecondInstanceLaunch callback: a second start of the
// program exits at once and the running one raises its main window instead, so two copies
// never save over each other's vault. A function, not a method: methods of App are bound
// to the frontend.
func SecondLaunchHandler(a *App) func(application.SecondInstanceData) {
	return func(application.SecondInstanceData) {
		// The notice comes through the window message loop, which starts after ServiceStartup
		// has created the window.
		if a.mainWin != nil {
			raiseWindow(a.mainWin)
		}
	}
}

// raiseWindow restores a minimised window and brings it to the front.
func raiseWindow(w raisable) {
	if w.IsMinimised() {
		w.Restore()
	}
	w.Show()
	w.Focus()
}
