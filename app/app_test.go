package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"sshclient/internal/appdir"
	"sshclient/internal/store"
)

func TestVaultResetCorruptGuardedAndRecovers(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, vaultFile)
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}

	healthy := &App{}
	if err := healthy.VaultResetCorrupt(); err == nil {
		t.Fatal("expected error without a corrupt vault")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "garbage" { //nolint:gosec // G304: test temp path
		t.Fatalf("file touched: %q, %v", got, err)
	}

	a := corruptApp()
	if err := a.VaultResetCorrupt(); err != nil {
		t.Fatal(err)
	}
	if r := a.current(); r.err != nil || r.store == nil || r.vault.Locked() {
		t.Fatal("app not recovered")
	}
	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != "garbage" { //nolint:gosec // G304: test temp path
		t.Fatalf("corrupt copy: %q, %v", got, err)
	}
	if errors.Is(a.current().err, store.ErrCorrupt) {
		t.Fatal("error not cleared")
	}
}

// corruptApp is an App whose vault failed to open as corrupt.
func corruptApp() *App {
	a := &App{}
	a.setVault(nil, nil, fmt.Errorf("open: %w", store.ErrCorrupt))
	return a
}

// TestVaultResetCorruptConcurrentReaders: bound calls running while the vault is replaced see
// either the corrupt generation or the new one, never a vault without its store (go test -race).
func TestVaultResetCorruptConcurrentReaders(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	a := corruptApp()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if r := a.current(); (r.vault == nil) != (r.store == nil) {
				t.Error("torn vault/store pair")
				return
			}
			_ = a.VaultState()
			_ = a.storeUnlocked()
		}
	}()
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- a.VaultResetCorrupt() }()
	}
	ok := 0
	for range 2 {
		if err := <-errs; err == nil {
			ok++
		}
	}
	close(stop)
	<-done
	if ok != 1 {
		t.Fatalf("%d resets succeeded, want exactly 1", ok)
	}
	if !a.storeUnlocked() {
		t.Fatal("vault not open after the reset")
	}
}
