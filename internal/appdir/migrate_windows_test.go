package appdir_test

import (
	"os"
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
	// The tests rely on the lock stopping other readers. Some machines (the GitHub Windows
	// runner) still let os.Open read such a file; there the premise does not hold.
	if share&windows.FILE_SHARE_READ == 0 {
		if f, err := os.Open(path); err == nil { //nolint:gosec // G304: a test file in t.TempDir
			_ = f.Close() //nolint:gosec // G104: only probing the lock
			t.Skip("this machine does not enforce the share-mode lock, so a locked file is still readable")
		}
	}
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
	marker := read(t, filepath.Join(oldDir, appdir.MarkerFile))
	if !strings.Contains(marker, newDir) {
		t.Fatal("no marker pointing at the new folder")
	}
	if !strings.Contains(marker, "не переносятся") {
		t.Fatal("the marker does not warn about an older build still writing")
	}
	if !strings.Contains(read(t, filepath.Join(newDir, appdir.LogFile)), "not carried over") {
		t.Fatal("the log does not warn about an older build still writing")
	}
	if left := staging(t, newDir); len(left) > 0 {
		t.Fatalf("staging folders left behind: %v", left)
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
	if exists(filepath.Join(newDir, "WebView2", "EBWebView", "lockfile")) {
		t.Fatal("the locked cache file was copied")
	}
	if !strings.Contains(read(t, filepath.Join(newDir, appdir.LogFile)), "1 locked WebView2 cache files skipped") {
		t.Fatal("the skip is not counted in the log")
	}
}

func TestMigrateLeavesOtherStagingFolders(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	openShared(t, filepath.Join(oldDir, "vault.json"), windows.FILE_SHARE_READ)
	others := []string{newDir + ".migrating", newDir + ".migrating-1-123"}
	for _, o := range others {
		write(t, filepath.Join(o, "vault.json"), "other start")
	}

	res, err := appdir.Migrate()
	if err != nil || res != appdir.Copied {
		t.Fatalf("got %v, %v", res, err)
	}
	if read(t, filepath.Join(newDir, "vault.json")) != "vault" {
		t.Fatal("vault not copied")
	}
	for _, o := range others {
		if read(t, filepath.Join(o, "vault.json")) != "other start" {
			t.Fatalf("%s touched", o)
		}
	}
	if left := staging(t, newDir); len(left) != len(others)-1 {
		t.Fatalf("staging folders: %v", left) // only the pre-made .migrating-1-123 matches the pattern
	}
}

// staging lists the per-process staging folders beside newDir.
func staging(t *testing.T, newDir string) []string {
	t.Helper()
	m, err := filepath.Glob(newDir + ".migrating-*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMigrateFailureKeepsUsingTheOldFolder(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	openShared(t, filepath.Join(oldDir, "vault.json"), 0)

	res, err := appdir.Migrate()
	if err == nil || res != appdir.Failed {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(newDir) || len(staging(t, newDir)) > 0 {
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
