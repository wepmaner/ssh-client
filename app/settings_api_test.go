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
	for _, tc := range []struct {
		field, text string
		bad         func(s *model.Settings)
	}{
		{"font", "Выберите шрифт из списка", func(s *model.Settings) { s.TerminalFont = "Comic Sans" }},
		{"size", "Размер от 10 до 20", func(s *model.Settings) { s.TerminalSize = 9 }},
		{"size", "Размер от 10 до 20", func(s *model.Settings) { s.TerminalSize = 21 }},
		{"lock", "Блокировка через 5, 15 или 30 минут или никогда", func(s *model.Settings) { s.LockAfterMin = 7 }},
	} {
		s := ok
		tc.bad(&s)
		if err := a.SaveSettings(s); err == nil || err.Error() != "field:"+tc.field+": "+tc.text {
			t.Fatalf("%+v: %v", s, err)
		}
	}
	if got := a.GetSettings(); got != ok {
		t.Fatalf("a rejected save changed the settings: %+v", got)
	}
}
