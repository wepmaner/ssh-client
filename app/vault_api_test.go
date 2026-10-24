package app

import (
	"fmt"
	"testing"

	"sshclient/internal/store"
)

func TestVaultPasswordLifecycle(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a := &App{}
	a.openVault()

	if st := a.VaultState(); st.Locked || st.HasPassword || st.Corrupt {
		t.Fatalf("fresh state: %+v", st)
	}
	if err := a.VaultSetPassword("short"); err == nil || err.Error() != "Пароль короче 8 символов" {
		t.Fatalf("short password: %v", err)
	}
	if err := a.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}

	// Restart: password vault comes up locked.
	b := &App{}
	b.openVault()
	if st := b.VaultState(); !st.Locked || !st.HasPassword {
		t.Fatalf("locked state: %+v", st)
	}
	if err := b.SaveSettings(b.GetSettings()); err == nil {
		t.Fatal("SaveSettings must fail while locked")
	}
	if err := b.VaultUnlock("wrong"); err == nil || err.Error() != "Неверный пароль" {
		t.Fatalf("wrong password: %v", err)
	}
	if err := b.VaultUnlock("correct horse"); err != nil {
		t.Fatal(err)
	}
	if b.store == nil || b.VaultState().Locked {
		t.Fatal("not unlocked")
	}
	if err := b.SaveSettings(b.GetSettings()); err != nil {
		t.Fatal(err)
	}

	if err := b.VaultRemovePassword("wrong"); err == nil || err.Error() != "Неверный пароль" {
		t.Fatalf("remove with wrong password: %v", err)
	}
	if !b.vault.HasPassword() {
		t.Fatal("password removed despite wrong input")
	}
	if err := b.VaultRemovePassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	if b.VaultState().HasPassword {
		t.Fatal("password still set")
	}
}

func TestVaultStateCorrupt(t *testing.T) {
	a := &App{vaultErr: fmt.Errorf("open: %w", store.ErrCorrupt)}
	st := a.VaultState()
	if !st.Corrupt || st.Error == "" {
		t.Fatalf("state: %+v", st)
	}
}
