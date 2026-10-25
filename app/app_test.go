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

	a := &App{vaultErr: fmt.Errorf("open: %w", store.ErrCorrupt)}
	if err := a.VaultResetCorrupt(); err != nil {
		t.Fatal(err)
	}
	if a.vaultErr != nil || a.store == nil || a.vault.Locked() {
		t.Fatal("app not recovered")
	}
	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != "garbage" { //nolint:gosec // G304: test temp path
		t.Fatalf("corrupt copy: %q, %v", got, err)
	}
	if errors.Is(a.vaultErr, store.ErrCorrupt) {
		t.Fatal("error not cleared")
	}
}
