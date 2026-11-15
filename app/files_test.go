package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
	"sshclient/internal/sshclient/sshtest"
)

// filesApp is an App whose host serves sftp from root and logs in with a key encrypted with
// fwdPassphrase; transfers reconnect at once.
type filesApp struct {
	sessionsApp
	root string
}

func newFilesApp(t *testing.T) filesApp {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(fwdPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := newSessionsApp(t, sshtest.Opts{AcceptKey: sshPub, SFTPRoot: root}, false)
	t.Cleanup(s.closeAllHostConns) // after closeAllFiles
	t.Cleanup(s.closeAllFiles)
	k, err := s.storeRef().SaveKey(model.Key{Name: "work", Type: "ed25519", Private: pem.EncodeToMemory(block),
		Format: "openssh", Encrypted: true, Fingerprint: ssh.FingerprintSHA256(sshPub)})
	if err != nil {
		t.Fatal(err)
	}
	h := s.storeRef().Hosts()[0]
	h.KeyID = k.ID
	if _, err := s.storeRef().SaveHost(h); err != nil {
		t.Fatal(err)
	}
	s.filesBackoff = func(int) time.Duration { return 20 * time.Millisecond }
	return filesApp{sessionsApp: s, root: root}
}

func (s filesApp) loginData(t *testing.T, tab string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range s.ev.snapshot() {
		if e.name == "files:login:"+tab {
			raw, err := base64.StdEncoding.DecodeString(e.payload.(string))
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		}
	}
	return b.String()
}

func (s filesApp) tabStates(tab string) []FilesStateDTO {
	var out []FilesStateDTO
	for _, e := range s.ev.snapshot() {
		if e.name == "files:state:"+tab {
			out = append(out, e.payload.(FilesStateDTO))
		}
	}
	return out
}

func (s filesApp) waitTabState(t *testing.T, tab, state string) FilesStateDTO {
	t.Helper()
	var got FilesStateDTO
	s.ev.waitFor(t, hcWait, "tab state "+state, func([]fakeEvent) bool {
		st := s.tabStates(tab)
		if len(st) > 0 && st[len(st)-1].State == state {
			got = st[len(st)-1]
			return true
		}
		return false
	})
	return got
}

// openTab opens a files tab, attaches its login view and answers the passphrase.
func (s filesApp) openTab(t *testing.T) string {
	t.Helper()
	tab, err := s.FilesOpen(s.hostID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FilesLoginAttach(tab); err != nil {
		t.Fatal(err)
	}
	s.ev.waitFor(t, hcWait, "the passphrase question", func([]fakeEvent) bool {
		return strings.Contains(s.loginData(t, tab), passphrasePrompt)
	})
	if err := s.FilesLoginWrite(tab, base64.StdEncoding.EncodeToString([]byte(fwdPassphrase+"\r"))); err != nil {
		t.Fatal(err)
	}
	s.waitTabState(t, tab, stateConnected)
	return tab
}

func (s filesApp) jobStates(tab, job string) []FilesJobDTO {
	var out []FilesJobDTO
	for _, e := range s.ev.snapshot() {
		if e.name == "files:job:"+tab {
			if d := e.payload.(FilesJobDTO); d.ID == job {
				out = append(out, d)
			}
		}
	}
	return out
}

func (s filesApp) waitJob(t *testing.T, tab, job, state string, timeout time.Duration) FilesJobDTO {
	t.Helper()
	var got FilesJobDTO
	s.ev.waitFor(t, timeout, "job "+state, func([]fakeEvent) bool {
		st := s.jobStates(tab, job)
		if len(st) > 0 && st[len(st)-1].State == state {
			got = st[len(st)-1]
			return true
		}
		return false
	})
	return got
}

func (s filesApp) home(t *testing.T, tab string) string {
	t.Helper()
	h, err := s.FilesHome(tab)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (s filesApp) conn(t *testing.T) *hostConn {
	t.Helper()
	s.hcMu.Lock()
	defer s.hcMu.Unlock()
	c := s.hostConns[s.hostID]
	if c == nil {
		t.Fatal("the host has no connection")
	}
	return c
}

func writeData(t *testing.T, p string, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func checkFile(t *testing.T, p string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(p) //nolint:gosec // G304: test temp path
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s differs: %v", p, err)
	}
}

// noParts fails if a part file is left under dir.
func noParts(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".part") {
			t.Errorf("part file left: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFilesLoginListAndChanges(t *testing.T) {
	s := newFilesApp(t)
	writeData(t, filepath.Join(s.root, "d", "a.txt"), 10)
	tab := s.openTab(t)
	home := s.home(t, tab)

	l, err := s.FilesList(tab, home)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != home || len(l.Entries) != 1 || l.Entries[0].Name != "d" || !l.Entries[0].Dir || l.Entries[0].Mode[0] != 'd' {
		t.Fatalf("list %+v", l)
	}
	if l2, err := s.FilesList(tab, ""); err != nil || l2.Path != home {
		t.Fatalf("home list %+v %v", l2, err)
	}
	if _, err := s.FilesList(tab, path.Join(home, "missing")); err == nil || !strings.Contains(err.Error(), "Не найдено") {
		t.Fatalf("missing folder: %v", err)
	}

	p, err := s.FilesMkdir(tab, home, "new")
	if err != nil || p != path.Join(home, "new") {
		t.Fatalf("mkdir %q %v", p, err)
	}
	if _, err := s.FilesMkdir(tab, home, "new"); err == nil || !strings.Contains(err.Error(), "Уже есть") {
		t.Fatalf("taken name: %v", err)
	}
	if _, err := s.FilesRename(tab, path.Join(home, "new"), "d"); err == nil {
		t.Fatal("renamed over an existing folder")
	}
	if p, err := s.FilesRename(tab, path.Join(home, "new"), "newer"); err != nil || p != path.Join(home, "newer") {
		t.Fatalf("rename %q %v", p, err)
	}
	if err := s.FilesMove(tab, []string{path.Join(home, "d", "a.txt")}, path.Join(home, "newer")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "newer", "a.txt")); err != nil {
		t.Fatalf("not moved: %v", err)
	}
	if err := s.FilesMove(tab, []string{path.Join(home, "newer")}, path.Join(home, "newer")); err == nil {
		t.Fatal("moved a folder into itself")
	}
	// One login for everything.
	if n := strings.Count(s.loginData(t, tab), passphrasePrompt); n != 1 {
		t.Fatalf("%d passphrase questions", n)
	}
	s.checkAccepted(t, 1)
}

func TestFilesTabUsesLiveConsoleConnection(t *testing.T) {
	s := newFilesApp(t)
	id := s.open(t)
	s.ev.waitData(t, id, passphrasePrompt)
	s.write(t, id, fwdPassphrase+"\r")
	s.ev.waitState(t, id, "connected", hcWait)

	tab, err := s.FilesOpen(s.hostID)
	if err != nil {
		t.Fatal(err)
	}
	s.waitTabState(t, tab, stateConnected) // without attaching: the state is emitted at once too
	if strings.Contains(s.loginData(t, tab), passphrasePrompt) {
		t.Fatal("asked again on a live connection")
	}
	s.checkAccepted(t, 1)
}

func TestFilesUploadDownloadJobs(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	home := s.home(t, tab)
	local := t.TempDir()
	a := writeData(t, filepath.Join(local, "up", "a.bin"), 200_000)
	b := writeData(t, filepath.Join(local, "up", "sub", "b.bin"), 1000)
	c := writeData(t, filepath.Join(local, "c.txt"), 5)

	conf, err := s.FilesConflicts(tab, "upload", []string{filepath.Join(local, "up"), filepath.Join(local, "c.txt")}, home)
	if err != nil || len(conf) != 0 {
		t.Fatalf("conflicts %v %v", conf, err)
	}
	job, err := s.FilesUpload(tab, []string{filepath.Join(local, "up"), filepath.Join(local, "c.txt")}, home, "skip")
	if err != nil {
		t.Fatal(err)
	}
	d := s.waitJob(t, tab, job, jobDone, hcWait)
	if d.TotalFiles != 3 || d.DoneFiles != 3 || d.Failed != 0 || d.TotalBytes != 201_005 || d.DoneBytes != 201_005 {
		t.Fatalf("upload job %+v", d)
	}
	checkFile(t, filepath.Join(s.root, "up", "a.bin"), a)
	checkFile(t, filepath.Join(s.root, "up", "sub", "b.bin"), b)
	checkFile(t, filepath.Join(s.root, "c.txt"), c)
	noParts(t, s.root)
	s.ev.waitFor(t, hcWait, "files:changed", func([]fakeEvent) bool { return s.ev.count("files:changed:"+tab) > 0 })

	conf, err = s.FilesConflicts(tab, "upload", []string{filepath.Join(local, "c.txt")}, home)
	if err != nil || len(conf) != 1 || conf[0] != "c.txt" {
		t.Fatalf("conflicts %v %v", conf, err)
	}

	dl := t.TempDir()
	job, err = s.FilesDownload(tab, []string{path.Join(home, "up")}, dl, "replace")
	if err != nil {
		t.Fatal(err)
	}
	d = s.waitJob(t, tab, job, jobDone, hcWait)
	if d.DoneFiles != 2 || d.Dest != filepath.Join(dl, "up") {
		t.Fatalf("download job %+v", d)
	}
	checkFile(t, filepath.Join(dl, "up", "a.bin"), a)
	checkFile(t, filepath.Join(dl, "up", "sub", "b.bin"), b)
	noParts(t, dl)
	jobs, err := s.FilesJobs(tab)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	if err := s.FilesJobRemove(tab, job); err != nil {
		t.Fatal(err)
	}
	if jobs, err := s.FilesJobs(tab); err != nil || len(jobs) != 1 {
		t.Fatalf("jobs after remove %+v", jobs)
	}
}

func TestFilesDeleteJob(t *testing.T) {
	s := newFilesApp(t)
	writeData(t, filepath.Join(s.root, "d", "a"), 1)
	writeData(t, filepath.Join(s.root, "d", "e", "b"), 1)
	writeData(t, filepath.Join(s.root, "keep"), 1)
	tab := s.openTab(t)
	home := s.home(t, tab)
	job, err := s.FilesDelete(tab, []string{path.Join(home, "d")})
	if err != nil {
		t.Fatal(err)
	}
	d := s.waitJob(t, tab, job, jobDone, hcWait)
	if d.TotalFiles != 4 || d.DoneFiles != 4 || d.Failed != 0 {
		t.Fatalf("delete job %+v", d)
	}
	if _, err := os.Stat(filepath.Join(s.root, "d")); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.root, "keep")); err != nil {
		t.Fatal(err)
	}
}

// dropOnce drops the server's connections once a transfer of the job passes at bytes in a file.
type dropOnce struct {
	once    sync.Once
	dropped atomic.Bool
	resumed atomic.Int64 // the first position reported after the drop
}

func (s filesApp) dropAt(at int64) *dropOnce {
	d := &dropOnce{}
	d.resumed.Store(-1)
	s.filesProgressHook = func(_, _ string, pos int64) {
		if d.dropped.Load() {
			d.resumed.CompareAndSwap(-1, pos)
			return
		}
		if pos > at {
			d.once.Do(func() {
				s.srv.DropConnections()
				d.dropped.Store(true)
			})
		}
	}
	return d
}

func TestFilesUploadResumesAfterDrop(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	home := s.home(t, tab)
	local := t.TempDir()
	data := writeData(t, filepath.Join(local, "big.bin"), 8_000_000)
	drop := s.dropAt(2_000_000)

	job, err := s.FilesUpload(tab, []string{filepath.Join(local, "big.bin")}, home, "skip")
	if err != nil {
		t.Fatal(err)
	}
	s.waitJob(t, tab, job, jobDone, 3*hcWait)
	if !drop.dropped.Load() {
		t.Fatal("the connection was never dropped")
	}
	if r := drop.resumed.Load(); r <= 0 {
		t.Fatalf("restarted from %d instead of resuming", r)
	}
	if !slicesContainsState(s.jobStates(tab, job), jobWaiting) {
		t.Fatal("the job never showed the wait")
	}
	checkFile(t, filepath.Join(s.root, "big.bin"), data)
	noParts(t, s.root)
	// The secrets survived the drop: the reconnect asked nothing.
	if n := strings.Count(s.loginData(t, tab), passphrasePrompt); n != 1 {
		t.Fatalf("%d passphrase questions", n)
	}
	if s.srv.Accepted() < 2 {
		t.Fatalf("accepted %d", s.srv.Accepted())
	}
}

func TestFilesDownloadResumesAfterDrop(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	home := s.home(t, tab)
	data := writeData(t, filepath.Join(s.root, "big.bin"), 8_000_000)
	drop := s.dropAt(2_000_000)
	dl := t.TempDir()
	job, err := s.FilesDownload(tab, []string{path.Join(home, "big.bin")}, dl, "skip")
	if err != nil {
		t.Fatal(err)
	}
	s.waitJob(t, tab, job, jobDone, 3*hcWait)
	if r := drop.resumed.Load(); !drop.dropped.Load() || r <= 0 {
		t.Fatalf("dropped %v, resumed from %d", drop.dropped.Load(), r)
	}
	checkFile(t, filepath.Join(dl, "big.bin"), data)
	noParts(t, dl)
}

func slicesContainsState(ds []FilesJobDTO, state string) bool {
	for _, d := range ds {
		if d.State == state {
			return true
		}
	}
	return false
}

func TestFilesJobFailsWhenServerStaysAway(t *testing.T) {
	s := newFilesApp(t)
	s.filesRetryFor = 300 * time.Millisecond
	tab := s.openTab(t)
	home := s.home(t, tab)
	local := t.TempDir()
	writeData(t, filepath.Join(local, "big.bin"), 8_000_000)
	s.filesProgressHook = func(_, _ string, pos int64) {
		if pos > 2_000_000 && s.srv.Accepted() == 1 {
			s.srv.StopListening()
			s.srv.DropConnections()
		}
	}
	job, err := s.FilesUpload(tab, []string{filepath.Join(local, "big.bin")}, home, "skip")
	if err != nil {
		t.Fatal(err)
	}
	d := s.waitJob(t, tab, job, jobFailed, 3*hcWait)
	if !strings.Contains(d.Message, "10 минут") {
		t.Fatalf("message %q", d.Message)
	}
	// Failed reconnects of a files user keep the secrets: the server never rejected them.
	if p, _ := s.conn(t).secretsForTest(); string(p) != fwdPassphrase {
		t.Fatalf("passphrase after failed reconnects: %q", p)
	}
	// The part stays for Retry; the target never appears.
	if _, err := os.Stat(filepath.Join(s.root, "big.bin")); err == nil {
		t.Fatal("a half-written file under the real name")
	}
	ents, err := os.ReadDir(s.root)
	if err != nil || len(ents) != 1 || !strings.HasSuffix(ents[0].Name(), ".part") {
		t.Fatalf("server folder %v", ents)
	}
}

func TestFilesCancelRemovesParts(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	home := s.home(t, tab)
	local := t.TempDir()
	writeData(t, filepath.Join(local, "big.bin"), 8_000_000)
	var once sync.Once
	s.filesProgressHook = func(id, _ string, pos int64) {
		if pos > 1_000_000 {
			once.Do(func() { go func() { _ = s.FilesJobCancel(tab, id) }() }) //nolint:gosec // G104: checked through the job state
		}
	}
	id, err := s.FilesUpload(tab, []string{filepath.Join(local, "big.bin")}, home, "skip")
	if err != nil {
		t.Fatal(err)
	}
	s.waitJob(t, tab, id, jobCancelled, 3*hcWait)
	if ents, err := os.ReadDir(s.root); err != nil || len(ents) != 0 {
		t.Fatalf("left on the server: %v", ents)
	}
	// A cancelled job can run again from the start.
	s.filesProgressHook = nil
	if err := s.FilesJobRetry(tab, id); err != nil {
		t.Fatal(err)
	}
	s.waitJob(t, tab, id, jobDone, 3*hcWait)
	noParts(t, s.root)
}

func TestFilesCloseLeavesAndWipes(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	c := s.conn(t)
	pass, _ := c.secretsForTest()
	if string(pass) != fwdPassphrase {
		t.Fatalf("passphrase not kept: %q", pass)
	}
	if err := s.FilesClose(tab); err != nil {
		t.Fatal(err)
	}
	s.waitTabState(t, tab, stateClosed)
	checkWiped(t, pass)
	s.checkNoConns(t, s.hostID)
	if _, err := s.FilesList(tab, ""); err == nil {
		t.Fatal("a closed tab still lists")
	}
}

func TestFilesHostDeleteEndsTab(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	if err := s.HostDelete(s.hostID); err != nil {
		t.Fatal(err)
	}
	st := s.waitTabState(t, tab, stateError)
	if st.Retryable || st.Message != errFilesHostDeleted.Error() {
		t.Fatalf("state %+v", st)
	}
	if err := s.FilesRetry(tab); err == nil {
		t.Fatal("retried a tab of a deleted host")
	}
	if err := s.FilesClose(tab); err != nil {
		t.Fatal(err)
	}
}

func TestHostConnKeepsSecretsForFilesUsers(t *testing.T) {
	s := newFilesApp(t)
	tab := s.openTab(t)
	s.home(t, tab) // the tab's sftp session is open: nothing logs in on its own after the drop
	c := s.conn(t)
	pass, _ := c.secretsForTest()
	s.srv.DropConnections()
	waitConnState(t, c, "none after the drop", func(st HostConnStateDTO) bool { return st.Status == hostConnNone })
	if p, _ := c.secretsForTest(); string(p) != fwdPassphrase {
		t.Fatalf("passphrase wiped on a drop with a files tab: %q", p)
	}
	// The tab logs in again by itself.
	if _, err := s.FilesList(tab, ""); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(s.loginData(t, tab), passphrasePrompt); n != 1 {
		t.Fatalf("%d passphrase questions", n)
	}
	// Without files users a drop wipes them as before.
	if err := s.FilesClose(tab); err != nil {
		t.Fatal(err)
	}
	s.waitTabState(t, tab, stateClosed)
	checkWiped(t, pass)
}

func TestFilesDrop(t *testing.T) {
	ev := newFakeEmitter()
	a := &App{emit: ev}
	a.filesDrop([]string{`C:\a`}, nil)                          // not on a files tab
	a.filesDrop(nil, map[string]string{"data-files-tab": "t1"}) // nothing dropped
	a.filesDrop([]string{`C:\a`}, map[string]string{"data-files-tab": "t1", "data-files-dir": "/x"})
	evs := ev.snapshot()
	if len(evs) != 1 || evs[0].name != "files:drop" {
		t.Fatalf("events %+v", evs)
	}
	if d := evs[0].payload.(FilesDropDTO); d.Tab != "t1" || d.Dir != "/x" || len(d.Paths) != 1 || d.Paths[0] != `C:\a` {
		t.Fatalf("drop %+v", d)
	}
}
