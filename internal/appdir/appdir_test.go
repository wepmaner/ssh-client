package appdir_test

import (
	"os"
	"path/filepath"
	"testing"

	"sshclient/internal/appdir"
)

func TestDirUnderLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	d, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(os.Getenv("LOCALAPPDATA"), "sshclient")
	if d != want {
		t.Fatalf("got %s want %s", d, want)
	}
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
}

func TestDirEmptyLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	if _, err := appdir.Dir(); err == nil {
		t.Fatal("expected error when LOCALAPPDATA is empty")
	}
}
