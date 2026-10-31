package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"sshclient/internal/appdir"
	"sshclient/internal/mcpserver"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/sshclient/sshtest"
)

const mcpTestPassword = "correct horse"

// blockingExec is a server command that reports its start and runs until release closes or a
// signal arrives; its exit code tells which: 7 released, 143 stopped.
type blockingExec struct {
	started chan string
	release chan struct{}
	stopped chan struct{}
}

func newBlockingExec() *blockingExec {
	return &blockingExec{started: make(chan string, 8), release: make(chan struct{}), stopped: make(chan struct{}, 8)}
}

func (b *blockingExec) exec(cmd string, stdout, _ io.Writer, stop <-chan string) int {
	b.started <- cmd
	select {
	case <-b.release:
		_, _ = stdout.Write([]byte("done\n"))
		return 7
	case <-stop:
		b.stopped <- struct{}{}
		return 143
	}
}

func (b *blockingExec) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(hcWait):
		t.Fatal("the command did not start")
	}
}

// mcpApp is a plain-key host with the MCP server on: its server stops first in the cleanup.
func mcpApp(t *testing.T, opts sshtest.Opts, access string) (sessionsApp, *mcpBackend) {
	t.Helper()
	s := newHostConnApp(t, opts, true)
	t.Cleanup(s.mcpReleaseAll)
	s.setAccess(t, access)
	return s, s.enableMCP(t)
}

func (s sessionsApp) enableMCP(t *testing.T) *mcpBackend {
	t.Helper()
	t.Cleanup(s.mcpStop)
	if _, err := s.MCPSetPort(freeMCPPort(t)); err != nil {
		t.Fatal(err)
	}
	st, err := s.MCPSetEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "running" {
		t.Fatalf("status %+v", st)
	}
	return s.mcpLive.Load()
}

func (s sessionsApp) setAccess(t *testing.T, access string) {
	t.Helper()
	d, err := s.hostDTO(s.hostID)
	if err != nil {
		t.Fatal(err)
	}
	d.MCPAccess = access
	if _, err := s.HostSave(d); err != nil {
		t.Fatal(err)
	}
}

func (s sessionsApp) info(t *testing.T, b *mcpBackend) mcpserver.HostInfo {
	t.Helper()
	hosts, err := b.Hosts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h.ID == s.hostID {
			return h
		}
	}
	t.Fatal("host is not open")
	return mcpserver.HostInfo{}
}

// noHold reports that the host's hold is gone.
func (s sessionsApp) noHold() bool {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	return s.mcpHolds[s.hostID] == nil
}

func (s sessionsApp) hasCall() bool {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	h := s.mcpHolds[s.hostID]
	return h != nil && len(h.calls) > 0
}

type runResult struct {
	res sshclient.ExecResult
	err error
}

func runAsync(b *mcpBackend, h mcpserver.HostInfo, cmd string) chan runResult {
	ch := make(chan runResult, 1)
	go func() {
		res, err := b.Run(context.Background(), h, cmd, time.Minute)
		ch <- runResult{res, err}
	}()
	return ch
}

func waitRun(t *testing.T, ch chan runResult) runResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(hcWait):
		t.Fatal("the call did not end")
		return runResult{}
	}
}

func freeMCPPort(t *testing.T) int {
	t.Helper()
	l, err := mcpserver.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func portOpen(port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpConnect(port int, token string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token}},
		MaxRetries: -1,
	}, nil)
}

func toolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func storedToken(a *App) string { return a.storeRef().Settings().MCPToken }

func vaultIntoFolder(t *testing.T) {
	t.Helper()
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, vaultFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil { // Save cannot replace it
		t.Fatal(err)
	}
}

func TestMCPOffByDefault(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	st := s.MCPStatus()
	if st.Enabled || st.State != "off" || st.Port != model.DefaultMCPPort {
		t.Fatalf("status %+v", st)
	}
	if storedToken(s.App) != "" {
		t.Fatal("token before the first switch-on")
	}
	s.mcpStart()
	if s.mcpLive.Load() != nil || s.mcpToken != "" {
		t.Fatal("server started while switched off")
	}
}

func TestMCPEnableCreatesTokenOnce(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	s.enableMCP(t)
	first := storedToken(s.App)
	if len(first) != 64 {
		t.Fatalf("token length %d", len(first))
	}
	port := s.MCPStatus().Port
	if !portOpen(port) {
		t.Fatal("port closed")
	}
	if _, err := s.MCPSetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPSetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := storedToken(s.App); got != first {
		t.Fatal("token changed on switch-on")
	}
	if _, err := s.MCPSetEnabled(true); err != nil {
		t.Fatal("repeated switch-on:", err)
	}
	if cmd, err := s.MCPAddCommand(); err != nil || !strings.Contains(cmd, first) || !strings.Contains(cmd, strconv.Itoa(port)) {
		t.Fatalf("command: %v", err)
	}
	if p, err := s.MCPPrompt(); err != nil || !strings.Contains(p, first) {
		t.Fatalf("prompt: %v", err)
	}
	if got := s.GetSettings().MCPToken; got != "" {
		t.Fatal("token in GetSettings")
	}
}

func TestMCPDisableClosesPort(t *testing.T) {
	s, b := mcpApp(t, sshtest.Opts{Exec: newBlockingExec().exec}, model.MCPAccessFull)
	port := s.MCPStatus().Port
	h := s.info(t, b)
	r := s.mustBegin(t) // a hold on the connection
	s.mcpEnd(r.call)
	st, err := s.MCPSetEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.State != "off" {
		t.Fatalf("status %+v", st)
	}
	if portOpen(port) {
		t.Fatal("port open after switch-off")
	}
	if !s.noHold() {
		t.Fatal("hold left after switch-off")
	}
	if s.GetSettings().MCPEnabled {
		t.Fatal("switch-off not stored")
	}
	// A call that passed the guard before the switch-off does not bring the hold back.
	if _, err := b.Run(context.Background(), h, "ls", time.Minute); err == nil {
		t.Fatal("late call ran")
	}
	if !s.noHold() {
		t.Fatal("late call left a hold")
	}
}

func TestMCPPortBusyShowsError(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	t.Cleanup(s.mcpStop)
	l, err := mcpserver.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port
	if _, err := s.MCPSetPort(port); err != nil {
		t.Fatal(err)
	}
	st, err := s.MCPSetEnabled(true)
	if err != nil {
		t.Fatal("a busy port is a status, not a failure:", err)
	}
	if st.State != "error" || !st.Enabled || !strings.Contains(st.Error, "занят другой программой") ||
		!strings.Contains(st.Error, strconv.Itoa(port)) || st.Detail == "" {
		t.Fatalf("status %+v", st)
	}
	if got := s.MCPStatus(); got != st {
		t.Fatalf("status %+v, want %+v", got, st)
	}
	if len(s.HostsList()) != 1 {
		t.Fatal("app broken")
	}
	_ = l.Close()
	st, err = s.MCPSetEnabled(true)
	if err != nil || st.State != "running" || st.Error != "" || st.Detail != "" {
		t.Fatalf("after release: %+v, %v", st, err)
	}
	if !portOpen(port) {
		t.Fatal("port closed")
	}
}

func TestMCPSetPort(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	s.enableMCP(t)
	old := s.MCPStatus().Port
	for _, p := range []int{0, 1023, 65536} {
		if _, err := s.MCPSetPort(p); err == nil || !strings.HasPrefix(err.Error(), "field:port: ") {
			t.Fatalf("port %d: %v", p, err)
		}
	}
	next := freeMCPPort(t)
	st, err := s.MCPSetPort(next)
	if err != nil || st.State != "running" || st.Port != next {
		t.Fatalf("%+v, %v", st, err)
	}
	if portOpen(old) || !portOpen(next) {
		t.Fatal("the server did not move")
	}
	if s.GetSettings().MCPPort != next {
		t.Fatal("port not stored")
	}
}

func TestMCPRotateTokenInvalidatesOld(t *testing.T) {
	s, _ := mcpApp(t, sshtest.Opts{}, model.MCPAccessRead)
	old := storedToken(s.App)
	r := s.mustBegin(t)
	s.mcpEnd(r.call)
	if err := s.MCPRotateToken(); err != nil {
		t.Fatal(err)
	}
	fresh := storedToken(s.App)
	if fresh == old || len(fresh) != 64 {
		t.Fatal("token not replaced")
	}
	st := s.MCPStatus()
	if st.State != "running" {
		t.Fatalf("status %+v", st)
	}
	if s.noHold() {
		t.Fatal("rotation dropped the hold: the passphrase would be asked again")
	}
	if cs, err := mcpConnect(st.Port, old); err == nil {
		_ = cs.Close()
		t.Fatal("old token accepted")
	}
	cs, err := mcpConnect(st.Port, fresh)
	if err != nil {
		t.Fatal("new token refused:", err)
	}
	defer func() { _ = cs.Close() }()
	if text, isErr := toolText(t, cs, "list_hosts", nil); isErr || !strings.Contains(text, s.hostID) {
		t.Fatalf("list_hosts: %q", text)
	}
}

func TestMCPLockedRefusesNewCalls(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	if err := s.VaultSetPassword(mcpTestPassword); err != nil {
		t.Fatal(err)
	}
	token := storedToken(s.App)
	port := s.MCPStatus().Port
	h := s.info(t, b)
	running := runAsync(b, h, "sleep")
	be.waitStarted(t)
	if err := s.VaultLock(); err != nil {
		t.Fatal(err)
	}
	const refused = "Клиент заблокирован: разблокируйте его"
	if _, err := b.Hosts(context.Background()); err == nil || err.Error() != refused {
		t.Fatalf("hosts: %v", err)
	}
	if _, err := b.Run(context.Background(), h, "ls", time.Minute); err == nil || err.Error() != refused {
		t.Fatalf("run: %v", err)
	}
	if _, err := b.Upload(context.Background(), h, "x", "y", false); err == nil || err.Error() != refused {
		t.Fatalf("upload: %v", err)
	}
	if _, err := b.Download(context.Background(), h, "x", "y", false); err == nil || err.Error() != refused {
		t.Fatalf("download: %v", err)
	}
	if st := s.MCPStatus(); st.State != "running" {
		t.Fatalf("status %+v", st)
	}
	cs, err := mcpConnect(port, token)
	if err != nil {
		t.Fatal("the token works while locked:", err)
	}
	defer func() { _ = cs.Close() }()
	if text, isErr := toolText(t, cs, "list_hosts", nil); !isErr || text != refused {
		t.Fatalf("list_hosts: %q", text)
	}

	close(be.release)
	r := waitRun(t, running)
	if r.err != nil || r.res.ExitCode != 7 || r.res.Stdout != "done\n" {
		t.Fatalf("running call: %+v", r)
	}
	if err := s.VaultUnlock(mcpTestPassword); err != nil {
		t.Fatal(err)
	}
	j := s.MCPJournalList()
	if len(j) != 1 || j[0].ExitCode != nil {
		t.Fatalf("exit code written while locked: %+v", j)
	}

	// A locked start does not listen; the unlock starts the server.
	s.mcpStop()
	if err := s.VaultLock(); err != nil {
		t.Fatal(err)
	}
	next := &App{}
	next.initVault()
	t.Cleanup(next.mcpStop)
	if st := next.MCPStatus(); st.State != "off" || portOpen(port) {
		t.Fatalf("locked start: %+v", st)
	}
	if err := next.VaultUnlock(mcpTestPassword); err != nil {
		t.Fatal(err)
	}
	if st := next.MCPStatus(); st.State != "running" || !st.Enabled || st.Port != port || !portOpen(port) {
		t.Fatalf("after unlock: %+v", st)
	}
	if err := next.VaultLock(); err != nil {
		t.Fatal(err)
	}
	if err := next.VaultUnlock(mcpTestPassword); err != nil { // an unlock after the idle lock
		t.Fatal(err)
	}
	if st := next.MCPStatus(); st.State != "running" {
		t.Fatalf("second unlock: %+v", st)
	}
}

func TestMCPRestoreStopsServer(t *testing.T) {
	be := newBlockingExec()
	s, _ := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	t.Cleanup(func() { close(be.release) }) // the closed connection may outrun the signal
	port := s.MCPStatus().Port
	token := storedToken(s.App)
	copyOn := filepath.Join(t.TempDir(), "on.vault")
	if err := s.writeBackup(copyOn, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPSetEnabled(false); err != nil {
		t.Fatal(err)
	}
	copyOff := filepath.Join(t.TempDir(), "off.vault")
	if err := s.writeBackup(copyOff, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPSetEnabled(true); err != nil {
		t.Fatal(err)
	}
	b := s.mcpLive.Load()
	running := runAsync(b, s.info(t, b), "sleep")
	be.waitStarted(t)

	if err := s.VaultRestore(copyOff, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if r := waitRun(t, running); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("running call: %+v", r)
	}
	if st := s.MCPStatus(); st.State != "off" || st.Enabled || portOpen(port) || !s.noHold() {
		t.Fatalf("copy without MCP: %+v", st)
	}
	if err := s.VaultRestore(copyOn, "backup pass"); err != nil {
		t.Fatal(err)
	}
	if st := s.MCPStatus(); st.State != "running" || !portOpen(port) {
		t.Fatalf("copy with MCP: %+v", st)
	}
	cs, err := mcpConnect(port, token)
	if err != nil {
		t.Fatal("token of the copy refused:", err)
	}
	_ = cs.Close()
}

func TestMCPFailedRestoreRestartsServer(t *testing.T) {
	s, _ := mcpApp(t, sshtest.Opts{}, model.MCPAccessFull)
	backup := filepath.Join(t.TempDir(), "x.vault")
	if err := s.writeBackup(backup, "backup pass"); err != nil {
		t.Fatal(err)
	}
	vaultIntoFolder(t) // the install fails after the server stopped
	if err := s.VaultRestore(backup, "backup pass"); err == nil {
		t.Fatal("restore must fail")
	}
	if st := s.MCPStatus(); st.State != "running" || !portOpen(st.Port) {
		t.Fatalf("status %+v", st)
	}
}

func TestMCPShutdownClosesPort(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	t.Cleanup(func() { close(be.release) }) // the closed connection may outrun the signal
	port := s.MCPStatus().Port
	running := runAsync(b, s.info(t, b), "sleep")
	be.waitStarted(t)
	if err := s.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if portOpen(port) {
		t.Fatal("port open after shutdown")
	}
	if r := waitRun(t, running); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("running call: %+v", r)
	}
	s.mcpStart()
	if portOpen(port) {
		t.Fatal("started after shutdown")
	}
}

func TestMCPJournalBeforeRun(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	running := runAsync(b, s.info(t, b), "docker ps")
	be.waitStarted(t)
	j := s.MCPJournalList()
	if len(j) != 1 {
		t.Fatalf("journal %+v", j)
	}
	if r := j[0]; r.HostID != s.hostID || r.HostName != "srv" || r.Tool != "run_command" || r.Text != "docker ps" ||
		r.Access != model.MCPAccessFull || r.Refused != "" || r.ExitCode != nil || r.Time.IsZero() || r.ID == "" {
		t.Fatalf("record %+v", r)
	}
	close(be.release)
	if r := waitRun(t, running); r.err != nil {
		t.Fatal(r.err)
	}
	j = s.MCPJournalList()
	if len(j) != 1 || j[0].ExitCode == nil || *j[0].ExitCode != 7 {
		t.Fatalf("journal %+v", j)
	}
	if err := s.MCPJournalClear(); err != nil {
		t.Fatal(err)
	}
	if j := s.MCPJournalList(); j == nil || len(j) != 0 {
		t.Fatalf("cleared journal: %#v", j)
	}
}

func TestMCPJournalFailureBlocksRun(t *testing.T) {
	be := newBlockingExec()
	root := t.TempDir()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec, SFTPRoot: root}, model.MCPAccessFull)
	h := s.info(t, b)
	vaultIntoFolder(t)
	if _, err := b.Run(context.Background(), h, "ls", time.Minute); err == nil || !strings.Contains(err.Error(), "журнал") {
		t.Fatalf("run: %v", err)
	}
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Upload(context.Background(), h, src, "a.txt", false); err == nil || !strings.Contains(err.Error(), "журнал") {
		t.Fatalf("upload: %v", err)
	}
	if len(be.started) != 0 {
		t.Fatal("the command ran")
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err == nil {
		t.Fatal("the file went through")
	}
}

func TestMCPRefusalInJournal(t *testing.T) {
	root := t.TempDir()
	s, b := mcpApp(t, sshtest.Opts{SFTPRoot: root}, model.MCPAccessRead)
	cs, err := mcpConnect(s.MCPStatus().Port, storedToken(s.App))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	text, isErr := toolText(t, cs, "run_command", map[string]any{"host": s.hostID, "command": "rm -rf /"})
	if !isErr || !strings.Contains(text, "Только просмотр") {
		t.Fatalf("run_command: %q", text)
	}
	j := s.MCPJournalList()
	if len(j) != 1 || j[0].Refused == "" || j[0].Text != "rm -rf /" || j[0].Access != model.MCPAccessRead || j[0].HostName != "srv" {
		t.Fatalf("journal %+v", j)
	}

	// The data folder is refused in the backend and journalled the same way.
	dir, err := appdir.Dir()
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(dir, vaultFile)
	if _, err := b.Download(context.Background(), s.info(t, b), "a.txt", vault, true); err == nil || !strings.Contains(err.Error(), "папку данных") {
		t.Fatalf("download: %v", err)
	}
	j = s.MCPJournalList()
	if len(j) != 2 || j[0].Tool != "download_file" || !strings.Contains(j[0].Refused, "папку данных") {
		t.Fatalf("journal %+v", j)
	}
}

func TestMCPTransfers(t *testing.T) {
	root := t.TempDir()
	s, b := mcpApp(t, sshtest.Opts{SFTPRoot: root}, model.MCPAccessFull)
	h := s.info(t, b)
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Upload(context.Background(), h, src, "b.txt", false); err != nil || n != 5 {
		t.Fatalf("upload: %d, %v", n, err)
	}
	dst := filepath.Join(dir, "c.txt")
	if n, err := b.Download(context.Background(), h, "b.txt", dst, false); err != nil || n != 5 {
		t.Fatalf("download: %d, %v", n, err)
	}
	if _, err := b.Upload(context.Background(), h, src, "b.txt", false); !errors.Is(err, sshclient.ErrExists) {
		t.Fatalf("second upload: %v", err)
	}
	j := s.MCPJournalList()
	if len(j) != 3 || j[1].Tool != "download_file" || j[1].Text != "b.txt -> "+dst || j[2].Text != src+" -> b.txt" || j[1].ExitCode != nil {
		t.Fatalf("journal %+v", j)
	}
}

func TestMCPReleaseCancelsTransfer(t *testing.T) {
	s, b := mcpApp(t, sshtest.Opts{StallSFTP: true}, model.MCPAccessFull)
	h := s.info(t, b)
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(ctx context.Context) (int64, error){
		"upload": func(ctx context.Context) (int64, error) { return b.Upload(ctx, h, src, "b.txt", false) },
		"download": func(ctx context.Context) (int64, error) {
			return b.Download(ctx, h, "b.txt", filepath.Join(dir, "c.txt"), false)
		},
	} {
		ch := make(chan error, 1)
		go func() {
			_, err := run(context.Background())
			ch <- err
		}()
		deadline := time.Now().Add(hcWait)
		for !s.hasCall() {
			if time.Now().After(deadline) {
				t.Fatal("no call")
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond) // the call reaches the stalled handshake
		s.mcpReleaseHost(s.hostID)
		select {
		case err := <-ch:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(hcWait):
			t.Fatalf("%s did not end", name)
		}
	}
}

func TestMCPClosedHostCancelsRunning(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	running := runAsync(b, s.info(t, b), "sleep")
	be.waitStarted(t)
	s.setAccess(t, model.MCPAccessClosed)
	r := waitRun(t, running)
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("running call: %+v", r)
	}
	select {
	case <-be.stopped:
	case <-time.After(hcWait):
		t.Fatal("the command got no signal")
	}
	if !s.noHold() {
		t.Fatal("hold left on a closed host")
	}
}

func TestMCPDeletedHostCancelsRunning(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	t.Cleanup(func() { close(be.release) }) // the closed connection may outrun the signal
	running := runAsync(b, s.info(t, b), "sleep")
	be.waitStarted(t)
	if err := s.HostDelete(s.hostID); err != nil {
		t.Fatal(err)
	}
	if r := waitRun(t, running); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("running call: %+v", r)
	}
	if !s.noHold() {
		t.Fatal("hold left on a deleted host")
	}
}

func TestMCPReadModeLetsRunningFinish(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	running := runAsync(b, s.info(t, b), "sleep")
	be.waitStarted(t)
	s.setAccess(t, model.MCPAccessRead)
	close(be.release)
	r := waitRun(t, running)
	if r.err != nil || r.res.ExitCode != 7 {
		t.Fatalf("running call: %+v", r)
	}
}

// TestMCPStaleHostInfo: the tool resolved the host before the owner changed its mode.
func TestMCPStaleHostInfo(t *testing.T) {
	be := newBlockingExec()
	s, b := mcpApp(t, sshtest.Opts{Exec: be.exec}, model.MCPAccessFull)
	h := s.info(t, b) // says full
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	s.setAccess(t, model.MCPAccessRead)
	if _, err := b.Run(context.Background(), h, "rm -rf /", time.Minute); err == nil || !strings.Contains(err.Error(), "Только просмотр") {
		t.Fatalf("read run: %v", err)
	}
	if _, err := b.Upload(context.Background(), h, src, "a.txt", true); err == nil || !strings.Contains(err.Error(), "Только просмотр") {
		t.Fatalf("read upload: %v", err)
	}
	j := s.MCPJournalList()
	if len(j) != 2 || j[0].Refused == "" || j[1].Refused == "" || j[0].Access != model.MCPAccessRead {
		t.Fatalf("journal %+v", j)
	}

	s.setAccess(t, model.MCPAccessClosed)
	missing := mcpserver.HostNotFound(h.Name).Error()
	if _, err := b.Run(context.Background(), h, "ls", time.Minute); err == nil || err.Error() != missing {
		t.Fatalf("closed run: %v", err)
	}
	if _, err := b.Upload(context.Background(), h, src, "a.txt", true); err == nil || err.Error() != missing {
		t.Fatalf("closed upload: %v", err)
	}
	if _, err := b.Download(context.Background(), h, "a.txt", src, true); err == nil || err.Error() != missing {
		t.Fatalf("closed download: %v", err)
	}
	if hosts, err := b.Hosts(context.Background()); err != nil || len(hosts) != 0 {
		t.Fatalf("hosts: %+v, %v", hosts, err)
	}
	if len(be.started) != 0 {
		t.Fatal("a command reached the server")
	}
	if len(s.MCPJournalList()) != 2 {
		t.Fatal("a closed host left a record")
	}
}

func TestMCPCopyRefusedWhenOff(t *testing.T) {
	s := newHostConnApp(t, sshtest.Opts{}, true)
	if _, err := s.MCPAddCommand(); err == nil {
		t.Fatal("command while off")
	}
	if _, err := s.MCPPrompt(); err == nil {
		t.Fatal("prompt while off")
	}
	s.enableMCP(t)
	if _, err := s.MCPSetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPAddCommand(); err == nil {
		t.Fatal("command after switch-off")
	}
}

func TestMCPTokenNeverInEventsOrJournal(t *testing.T) {
	s, _ := mcpApp(t, sshtest.Opts{Exec: func(string, io.Writer, io.Writer, <-chan string) int { return 0 }}, model.MCPAccessFull)
	token := storedToken(s.App)
	cs, err := mcpConnect(s.MCPStatus().Port, token)
	if err != nil {
		t.Fatal(err)
	}
	if text, isErr := toolText(t, cs, "run_command", map[string]any{"host": s.hostID, "command": "ls"}); isErr {
		t.Fatal(text)
	}
	_ = cs.Close()
	if err := s.MCPRotateToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPSetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if s.ev.count("mcp:status") == 0 {
		t.Fatal("no status event")
	}
	for _, e := range s.ev.snapshot() {
		if d, ok := e.payload.(MCPStatusDTO); ok && (strings.Contains(d.Error, token) || strings.Contains(d.Detail, token)) {
			t.Fatal("token in a status event")
		}
		if text, ok := e.payload.(string); ok && strings.Contains(text, token) {
			t.Fatalf("token in event %s", e.name)
		}
	}
	for _, r := range s.MCPJournalList() {
		if strings.Contains(r.Text, token) || strings.Contains(r.Refused, token) {
			t.Fatal("token in the journal")
		}
	}
}
