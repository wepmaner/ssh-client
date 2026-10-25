package app

import "testing"

func TestContextMenusCoverEveryScreen(t *testing.T) {
	want := map[string][]string{
		"host":    {"edit", "duplicate", "delete"},
		"group":   {"rename", "delete"},
		"key":     {"rename", "delete"},
		"forward": {"edit", "delete"},
	}
	if len(contextMenus) != len(want) {
		t.Fatalf("got %d menus", len(contextMenus))
	}
	for _, m := range contextMenus {
		w, ok := want[m.name]
		if !ok || len(m.items) != len(w) {
			t.Fatalf("menu %q: %+v", m.name, m.items)
		}
		for i, it := range m.items {
			if it.action != w[i] || it.label == "" {
				t.Fatalf("menu %q item %d: %+v", m.name, i, it)
			}
		}
	}
}

func TestMenuClickEmitsAction(t *testing.T) {
	ev := newFakeEmitter()
	a := &App{emit: ev}
	// The runtime reads the CSS custom property verbatim, with the leading space.
	a.menuClicked("host", "delete", " 5f0c-id ")
	got := ev.snapshot()
	want := MenuActionDTO{Menu: "host", Action: "delete", ID: "5f0c-id"}
	if len(got) != 1 || got[0].name != menuActionEvent || got[0].payload != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
