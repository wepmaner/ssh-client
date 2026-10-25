package app

import (
	"testing"

	"sshclient/internal/model"
)

func TestSaveSettingsValidates(t *testing.T) {
	a := newKeysApp(t)
	ok := model.DefaultSettings()
	ok.TerminalFont, ok.TerminalSize, ok.LockAfterMin = "JetBrains Mono", 20, 15
	if err := a.SaveSettings(ok); err != nil {
		t.Fatal(err)
	}
	if got := a.GetSettings(); got != ok {
		t.Fatalf("saved %+v", got)
	}
	for _, bad := range []func(s *model.Settings){
		func(s *model.Settings) { s.TerminalFont = "Comic Sans" },
		func(s *model.Settings) { s.TerminalSize = 9 },
		func(s *model.Settings) { s.TerminalSize = 21 },
		func(s *model.Settings) { s.LockAfterMin = 7 },
	} {
		s := ok
		bad(&s)
		if err := a.SaveSettings(s); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	if got := a.GetSettings(); got != ok {
		t.Fatalf("a rejected save changed the settings: %+v", got)
	}
}
