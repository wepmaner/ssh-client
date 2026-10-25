package appdir_test

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"sshclient/internal/appdir"
)

// openShared opens path with the given share mode and keeps it open for the test:
// without FILE_SHARE_DELETE Windows refuses to rename the folder that holds it.
func openShared(t *testing.T, path string, share uint32) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
}

func TestMigrateCopiesWhenRenameFails(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	write(t, filepath.Join(oldDir, "window.json"), "{}")
	openShared(t, filepath.Join(oldDir, "window.json"), windows.FILE_SHARE_READ)

	res, err := appdir.Migrate()
	if err != nil || res != appdir.Copied {
		t.Fatalf("got %v, %v", res, err)
	}
	if read(t, filepath.Join(newDir, "vault.json")) != "vault" || read(t, filepath.Join(newDir, "window.json")) != "{}" {
		t.Fatal("content not copied")
	}
	if read(t, filepath.Join(oldDir, "vault.json")) != "vault" {
		t.Fatal("the old folder must stay intact")
	}
	if !strings.Contains(read(t, filepath.Join(oldDir, appdir.MarkerFile)), newDir) {
		t.Fatal("no marker pointing at the new folder")
	}
	if exists(newDir + ".migrating") {
		t.Fatal("the staging folder is left behind")
	}
	if d := dir(t); d != newDir {
		t.Fatalf("Dir %s, want %s", d, newDir)
	}
}

func TestMigrateSkipsLockedWebViewCache(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	cache := filepath.Join(oldDir, "WebView2", "EBWebView", "lockfile")
	write(t, cache, "x")
	openShared(t, cache, 0)

	res, err := appdir.Migrate()
	if err != nil || res != appdir.Copied {
		t.Fatalf("got %v, %v", res, err)
	}
	if read(t, filepath.Join(newDir, "vault.json")) != "vault" {
		t.Fatal("vault not copied")
	}
}

func TestMigrateFailureKeepsUsingTheOldFolder(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	openShared(t, filepath.Join(oldDir, "vault.json"), 0)

	res, err := appdir.Migrate()
	if err == nil || res != appdir.Failed {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(newDir) || exists(newDir+".migrating") {
		t.Fatal("a failed copy must leave no new folder behind")
	}
	if exists(filepath.Join(oldDir, appdir.MarkerFile)) {
		t.Fatal("no marker after a failed copy")
	}
	// This run goes on with the old folder, so nothing new is written beside the old data.
	if d, err := appdir.Dir(); err != nil || d != oldDir {
		t.Fatalf("Dir %s, %v; want the old folder %s", d, err, oldDir)
	}
	if !strings.Contains(read(t, filepath.Join(oldDir, appdir.LogFile)), "vault.json") {
		t.Fatal("the failure is not logged")
	}
}
