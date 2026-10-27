package app

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshclient/internal/appdir"
	"sshclient/internal/model"
	"sshclient/internal/sshclient/sshtest"
	"sshclient/internal/store"
)

func vaultBytes(t *testing.T) []byte {
	t.Helper()
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, vaultFile)) //nolint:gosec // G304: test temp path
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVaultBackupNeedsAPasswordOnlyWithoutOne(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	a := &App{}
	a.openVault()
	if _, err := a.VaultBackup(""); err == nil || err.Error() != "Пароль короче 8 символов" {
		t.Fatalf("DPAPI vault, empty password: %v", err)
	}
	if err := a.VaultSetPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	// Past the password check it needs the window for the save dialog.
	if _, err := a.VaultBackup(""); err != errNoWindow {
		t.Fatalf("password vault: %v", err)
	}
	if _, err := a.VaultBackup("another pass"); err != errCopyHasPw {
		t.Fatalf("password vault with a password: %v", err)
	}
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	for _, inside := range []string{filepath.Join(dir, "b.vault"), filepath.Join(strings.ToUpper(dir), "sub", "b.vault")} {
		if err := a.writeBackup(inside, ""); err != errCopyInDir {
			t.Fatalf("backup in the data folder %s: %v", inside, err)
		}
		if _, err := os.Stat(inside); err == nil {
			t.Fatal("a refused backup was written")
		}
	}
	out := filepath.Join(t.TempDir(), "b.vault")
	if err := a.writeBackup(out, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenBackup(out, []byte("correct horse")); err != nil {
		t.Fatalf("backup of a password vault opens with its password: %v", err)
	}
}

// TestVaultRestore: a wrong password changes nothing and keeps the session and the forward;
// the right one closes them, keeps the old file aside and swaps in the backup, after which
// connections start again.
func TestVaultRestore(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	backup := filepath.Join(t.TempDir(), "copy.vault")
	if err := s.writeBackup(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	// Changes after the backup: the restore drops them.
	if _, err := s.storeRef().SaveHost(model.Host{Name: "later", Address: "later.test", Port: 22, User: "u"}); err != nil {
		t.Fatal(err)
	}
	if c := s.VaultCounts(); c.Hosts != 2 || c.Keys != 1 || c.Forwards != 0 {
		t.Fatalf("counts %+v", c)
	}
	f := s.saveL(t, freePort(t), echoServer(t), false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	id := s.open(t)
	s.ev.waitState(t, id, "connected", 5*time.Second)
	before := vaultBytes(t)
	oldStore := s.storeRef()
	if c := s.VaultCounts(); !c.VaultFile || c.Forwards != 1 {
		t.Fatalf("counts %+v", c)
	}

	notBackup := filepath.Join(t.TempDir(), "x.vault")
	if err := os.WriteFile(notBackup, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultRestore(notBackup, "backup pass"); err == nil || err.Error() != "Это не резервная копия sshclient" {
		t.Fatalf("not a backup: %v", err)
	}
	err := s.VaultRestore(backup, "wrong pass")
	wantField(t, err, "password")
	if !strings.HasSuffix(err.Error(), "Неверный пароль копии") {
		t.Fatalf("wrong password: %v", err)
	}
	if !bytes.Equal(vaultBytes(t), before) || s.storeRef() != oldStore {
		t.Fatal("a refused restore touched the vault")
	}
	if len(s.SessionList()) != 1 || listed(t, s.App, f.ID).Status != "running" {
		t.Fatal("a refused restore closed the session or the forward")
	}
	roundTrip(t, f.Listen)

	if err := s.VaultRestore(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if len(s.SessionList()) != 0 {
		t.Fatal("sessions still open")
	}
	// The old forward's listener is closed: its port is free.
	ln, err := net.Listen("tcp", f.Listen)
	if err != nil {
		t.Fatalf("old forward still listens on %s: %v", f.Listen, err)
	}
	ignore(ln.Close())
	s.ev.waitState(t, id, "closed", 5*time.Second)
	if st := s.VaultState(); st.Locked || !st.HasPassword {
		t.Fatalf("state after restore %+v", st)
	}
	if hosts := s.HostsList(); len(hosts) != 1 || hosts[0].Name != "srv" {
		t.Fatalf("hosts after restore %+v", hosts)
	}
	if len(s.ForwardsList()) != 0 || len(s.KeysList()) != 1 {
		t.Fatal("forwards or keys not from the backup")
	}
	if _, err := oldStore.SaveHost(model.Host{Address: "late.test"}); err == nil {
		t.Fatal("the old store still writes")
	}
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	asides, err := filepath.Glob(filepath.Join(dir, "vault.before-restore-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(asides) != 1 {
		t.Fatalf("aside files %v", asides)
	}
	if got, _ := os.ReadFile(asides[0]); !bytes.Equal(got, before) { //nolint:gosec // G304: test temp path
		t.Fatal("aside file is not the old vault")
	}

	// The restored vault opens with the backup password after a restart.
	b := &App{}
	b.openVault()
	if err := b.VaultUnlock("backup pass"); err != nil {
		t.Fatal(err)
	}

	// Connections start again on the restored vault.
	id2 := s.open(t)
	s.ev.waitState(t, id2, "connected", 5*time.Second)
}

func TestVaultRestoreOnCorruptVault(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	src := &App{}
	src.openVault()
	if _, err := src.storeRef().SaveHost(model.Host{Name: "h", Address: "h.test", Port: 22, User: "u"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "copy.vault")
	if err := src.writeBackup(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, vaultFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.openVault()
	if !a.VaultState().Corrupt {
		t.Fatal("vault should be corrupt")
	}
	if err := a.VaultRestore(backup, "nope nope"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if got := vaultBytes(t); string(got) != "garbage" {
		t.Fatal("refused restore touched the corrupt file")
	}
	if err := a.VaultRestore(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if st := a.VaultState(); st.Corrupt || st.Locked || len(a.HostsList()) != 1 {
		t.Fatalf("state %+v", st)
	}
}

func TestRestoringRefusesNewSessionsAndForwards(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	s.restoring.Store(true)
	if _, err := s.SessionOpen(s.hostID, 80, 24); err != errRestoring {
		t.Fatalf("SessionOpen: %v", err)
	}
	if err := s.ForwardStart(f.ID); err != errRestoring {
		t.Fatalf("ForwardStart: %v", err)
	}
	if err := s.ForwardStartAll(s.hostID); err != errRestoring {
		t.Fatalf("ForwardStartAll: %v", err)
	}
	s.restoring.Store(false)
}

func TestReopenHostConnsRespectsShutdown(t *testing.T) {
	a := &App{}
	a.closeAllHostConns()
	a.exiting.Store(true)
	a.reopenHostConns()
	if !a.hcDown {
		t.Fatal("a restore reopened connections after the shutdown began")
	}
	a.exiting.Store(false)
	a.reopenHostConns()
	if a.hcDown {
		t.Fatal("connections not reopened")
	}
}

func TestVaultCountsSeesCorruptFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if c := corruptApp().VaultCounts(); c.VaultFile {
		t.Fatal("no vault.json yet")
	}
	if err := os.WriteFile(filepath.Join(dir, vaultFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := corruptApp().VaultCounts(); !c.VaultFile || c.Keys+c.Hosts+c.Forwards != 0 {
		t.Fatalf("counts %+v", c)
	}
}

// TestFailedRestoreKeepsForwardsGroups: a restore that fails at the install leaves the old vault
// live with its attached forwards groups, and forwards start on it as before.
func TestFailedRestoreKeepsForwardsGroups(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	backup := filepath.Join(t.TempDir(), "copy.vault")
	if err := s.writeBackup(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	f := s.saveL(t, freePort(t), echoServer(t), false) // attaches the host's group
	g := s.fwdHostGroup(s.hostID)
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	// A directory in the way of the temporary file makes the install fail.
	if err := os.Mkdir(filepath.Join(dir, vaultFile+".restore.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := vaultBytes(t)
	if err := s.VaultRestore(backup, "backup pass"); err == nil {
		t.Fatal("expected the install to fail")
	}
	if !bytes.Equal(vaultBytes(t), before) {
		t.Fatal("a failed restore changed vault.json")
	}
	if s.fwdHostGroup(s.hostID) != g {
		t.Fatal("a failed restore dropped the forwards group")
	}
	g.out.mu.Lock()
	attached := g.out.attached
	g.out.mu.Unlock()
	if !attached {
		t.Fatal("the group is no longer attached")
	}
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFwdState(t, f.ID, "running", 0, 5*time.Second)
	roundTrip(t, f.Listen)
}

// TestLaunchForwardRefusesAReplacedVault: a forward read from a vault generation that a restore
// replaced is not launched, so no orphan run outlives the old store.
func TestLaunchForwardRefusesAReplacedVault(t *testing.T) {
	s := newForwardsApp(t, sshtest.Opts{}, true)
	f := s.saveL(t, freePort(t), echoServer(t), false)
	mf, err := s.findForward(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := s.refs.Load()
	r := s.current()
	s.setVault(r.vault, r.store, nil) // a new generation, as a restore publishes
	if err := s.startForward(stale, mf); err != errRestoring {
		t.Fatalf("stale generation: %v", err)
	}
	if s.currentRun(f.ID) != nil {
		t.Fatal("a run was launched for the replaced vault")
	}
	s.restoring.Store(true)
	if err := s.startForward(s.refs.Load(), mf); err != errRestoring {
		t.Fatalf("during a restore: %v", err)
	}
	s.restoring.Store(false)
	if err := s.ForwardStart(f.ID); err != nil {
		t.Fatal(err)
	}
}
