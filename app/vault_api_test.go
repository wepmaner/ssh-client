package app

import (
	"fmt"
	"testing"
	"time"

	"sshclient/internal/sshclient/sshtest"
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

// TestVaultLockIdle: the idle lock drops the vault key while the open session and the running
// forward keep their own clients; a redial during the lock fails and retries, the unlock resumes
// it and restarts nothing.
func TestVaultLockIdle(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	s.autostartOnce() // startup, as initVault does: the vault is open, nothing to start yet
	if err := s.VaultLock(); err == nil || err.Error() != "Хранилище не защищено паролем" {
		t.Fatalf("lock without a password: %v", err)
	}
	target := echoServer(t)
	auto := s.saveL(t, freePort(t), target, true)
	stopped := s.saveL(t, freePort(t), target, true)
	for _, f := range []ForwardDTO{auto, stopped} {
		if err := s.ForwardStart(f.ID); err != nil {
			t.Fatal(err)
		}
		s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	}
	if err := s.ForwardStop(stopped.ID); err != nil {
		t.Fatal(err)
	}
	run := s.currentRun(auto.ID)
	id := s.open(t)
	s.ev.waitState(t, id, "connected", 5*time.Second)
	if err := s.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}

	if err := s.VaultLock(); err != nil {
		t.Fatal(err)
	}
	if st := s.VaultState(); !st.Locked || !st.HasPassword {
		t.Fatalf("state %+v", st)
	}
	if len(s.KeysList()) != 0 || len(s.HostsList()) != 0 || len(s.ForwardsList()) != 0 || s.store.Keys() != nil {
		t.Fatal("locked vault still serves data")
	}
	if _, err := s.SessionOpen(s.hostID, 80, 24); err == nil || err.Error() != "Хранилище заблокировано" {
		t.Fatalf("open while locked: %v", err)
	}

	// The open session and the forward run on their own clients.
	s.write(t, id, "hi\r")
	s.ev.waitData(t, id, "ok\r\nhi\r")
	roundTrip(t, auto.Listen)

	// A redial needs the vault: it fails and keeps retrying.
	n := len(s.ev.fwdStates(auto.ID))
	s.srv.DropConnections()
	s.ev.waitFor(t, 10*time.Second, "redial refused while locked", func([]fakeEvent) bool {
		for _, st := range s.ev.fwdStates(auto.ID)[n:] {
			if st.Status == "starting" && st.Error == "Хранилище заблокировано" {
				return true
			}
		}
		return false
	})

	if err := s.VaultUnlock("correct horse"); err != nil {
		t.Fatal(err)
	}
	if len(s.KeysList()) != 1 || len(s.HostsList()) != 1 {
		t.Fatal("unlock did not restore the data")
	}
	s.ev.waitFwdState(t, auto.ID, "running", n+1, 15*time.Second)
	roundTrip(t, auto.Listen)
	if s.currentRun(auto.ID) != run {
		t.Fatal("unlock restarted a running forward")
	}
	if got := listed(t, s.App, stopped.ID); got.Status != "stopped" {
		t.Fatalf("unlock restarted a forward the user stopped: %+v", got)
	}
}
