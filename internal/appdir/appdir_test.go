package appdir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshclient/internal/appdir"
)

// roots points APPDATA and LOCALAPPDATA at two temp folders and returns the old
// (%LOCALAPPDATA%\sshclient) and new (%APPDATA%\sshclient) data folders.
func roots(t *testing.T) (oldDir, newDir string) {
	t.Helper()
	roaming, local := t.TempDir(), t.TempDir()
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", local)
	t.Cleanup(appdir.ResetFallback)
	return filepath.Join(local, "sshclient"), filepath.Join(roaming, "sshclient")
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test temp folder
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestDirUnderAppData(t *testing.T) {
	_, newDir := roots(t)
	d, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if d != newDir {
		t.Fatalf("got %s want %s", d, newDir)
	}
	if !exists(d) {
		t.Fatal("dir not created")
	}
}

func TestDirEmptyAppData(t *testing.T) {
	t.Setenv("APPDATA", "")
	if _, err := appdir.Dir(); err == nil {
		t.Fatal("expected error when APPDATA is empty")
	}
}

func TestMigrateFreshStart(t *testing.T) {
	oldDir, newDir := roots(t)
	res, err := appdir.Migrate()
	if err != nil || res != appdir.NothingToMove {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(oldDir) {
		t.Fatal("the old folder must not be created")
	}
	if d := dir(t); d != newDir {
		t.Fatalf("Dir %s, want %s", d, newDir)
	}
}

func TestMigrateEmptyOldDir(t *testing.T) {
	oldDir, newDir := roots(t)
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if res, err := appdir.Migrate(); err != nil || res != appdir.NothingToMove {
		t.Fatalf("got %v, %v", res, err)
	}
	if d := dir(t); d != newDir {
		t.Fatalf("Dir %s, want %s", d, newDir)
	}
}

func TestMigrateByRename(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	write(t, filepath.Join(oldDir, "known_hosts"), "hosts")
	write(t, filepath.Join(oldDir, "WebView2", "EBWebView", "Local State"), "cache")
	// An earlier Dir() call may have left an empty new folder behind.
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := appdir.Migrate()
	if err != nil || res != appdir.Moved {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(oldDir) {
		t.Fatal("a renamed folder leaves nothing behind")
	}
	if read(t, filepath.Join(newDir, "vault.json")) != "vault" ||
		read(t, filepath.Join(newDir, "known_hosts")) != "hosts" ||
		read(t, filepath.Join(newDir, "WebView2", "EBWebView", "Local State")) != "cache" {
		t.Fatal("content not moved")
	}
	if !strings.Contains(read(t, filepath.Join(newDir, appdir.LogFile)), oldDir) {
		t.Fatal("the move is not logged in the data folder")
	}
	if d := dir(t); d != newDir {
		t.Fatalf("Dir %s, want %s", d, newDir)
	}
	// The second start finds nothing to move.
	if res, err := appdir.Migrate(); err != nil || res != appdir.NothingToMove {
		t.Fatalf("second run: %v, %v", res, err)
	}
}

func TestMigrateNewDirHasData(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "old")
	write(t, filepath.Join(newDir, "vault.json"), "new")

	res, err := appdir.Migrate()
	if err != nil || res != appdir.NewHasData {
		t.Fatalf("got %v, %v", res, err)
	}
	if read(t, filepath.Join(oldDir, "vault.json")) != "old" || read(t, filepath.Join(newDir, "vault.json")) != "new" {
		t.Fatal("both folders must stay as they were")
	}
	if !strings.Contains(read(t, filepath.Join(newDir, appdir.LogFile)), oldDir) {
		t.Fatal("the skip is not logged")
	}
}

func TestMigrateAfterCopyIsQuiet(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	write(t, filepath.Join(oldDir, appdir.MarkerFile), newDir)
	write(t, filepath.Join(newDir, "vault.json"), "vault")
	if res, err := appdir.Migrate(); err != nil || res != appdir.NothingToMove {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(filepath.Join(newDir, appdir.LogFile)) {
		t.Fatal("a finished copy is not logged again on every start")
	}
}

func dir(t *testing.T) string {
	t.Helper()
	d, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMigrateMarkerMeansDone(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "stale")
	write(t, filepath.Join(oldDir, appdir.MarkerFile), newDir)
	// The new folder is gone (deleted by hand): the stale copy is still not brought back.
	if res, err := appdir.Migrate(); err != nil || res != appdir.NothingToMove {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(filepath.Join(newDir, "vault.json")) {
		t.Fatal("stale data copied back")
	}
}

func TestMigrateWaitsForAnotherStart(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	lockFile := newDir + ".migrate.lock"
	write(t, lockFile, "1")
	appdir.SetLockWait(t, 200*time.Millisecond)

	res, err := appdir.Migrate()
	if err == nil || res != appdir.Failed || !strings.Contains(err.Error(), "another start") {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(newDir) || !exists(filepath.Join(oldDir, "vault.json")) {
		t.Fatal("moved while another start held the lock")
	}
	if !exists(lockFile) {
		t.Fatal("removed a lock it did not take")
	}
	if d := dir(t); d != oldDir {
		t.Fatalf("Dir %s, want the old folder %s", d, oldDir)
	}
}

func TestMigrateTakesOverAStaleLock(t *testing.T) {
	oldDir, newDir := roots(t)
	write(t, filepath.Join(oldDir, "vault.json"), "vault")
	lockFile := newDir + ".migrate.lock"
	write(t, lockFile, "1")
	hourAgo := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockFile, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	if res, err := appdir.Migrate(); err != nil || res != appdir.Moved {
		t.Fatalf("got %v, %v", res, err)
	}
	if exists(lockFile) {
		t.Fatal("lock left behind")
	}
}
