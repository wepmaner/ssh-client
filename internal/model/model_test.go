package model_test

import (
	"testing"

	"sshclient/internal/model"
)

func TestDefaultSettings(t *testing.T) {
	s := model.DefaultSettings()
	if s.TerminalFont != "Cascadia Mono" {
		t.Errorf("font = %q", s.TerminalFont)
	}
	if s.TerminalSize != 13 {
		t.Errorf("size = %d", s.TerminalSize)
	}
	if !s.WebGL {
		t.Error("WebGL should default to true")
	}
	if s.Window.W != 1280 || s.Window.H != 800 {
		t.Errorf("window = %+v", s.Window)
	}
}
