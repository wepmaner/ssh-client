package app

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// fakeRaise records how a second launch raises the window.
type fakeRaise struct {
	minimised bool
	calls     []string
}

func (f *fakeRaise) IsMinimised() bool { return f.minimised }
func (f *fakeRaise) Restore()          { f.calls = append(f.calls, "restore"); f.minimised = false }
func (f *fakeRaise) Show() application.Window {
	f.calls = append(f.calls, "show")
	return nil
}
func (f *fakeRaise) Focus() { f.calls = append(f.calls, "focus") }

func TestRaiseWindowRestoresAMinimisedWindow(t *testing.T) {
	w := &fakeRaise{minimised: true}
	raiseWindow(w)
	if got := w.calls; len(got) != 3 || got[0] != "restore" || got[1] != "show" || got[2] != "focus" {
		t.Fatalf("calls %v", got)
	}
	w = &fakeRaise{}
	raiseWindow(w)
	if got := w.calls; len(got) != 2 || got[0] != "show" || got[1] != "focus" {
		t.Fatalf("calls %v", got)
	}
}

func TestSecondLaunchBeforeTheWindowIsHarmless(t *testing.T) {
	SecondLaunchHandler(&App{})(application.SecondInstanceData{Args: []string{"sshclient.exe"}})
}
