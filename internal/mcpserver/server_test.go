package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const testToken = "tok-123"

type fakeBackend struct {
	mu       sync.Mutex
	hosts    []HostInfo
	hostsErr error
	calls    int
	runs     []string
	timeout  time.Duration
	result   sshclient.ExecResult
	runErr   error
	uploads  int
	downs    int
	refused  []string

	block   bool          // Run waits for ctx.Done
	started chan struct{} // closed-ish signal: sent when Run starts blocking
	done    chan struct{} // closed when the ctx of Run is cancelled
}

func (f *fakeBackend) Hosts(ctx context.Context) ([]HostInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.hosts, f.hostsErr
}

func (f *fakeBackend) Run(ctx context.Context, h HostInfo, command string, timeout time.Duration) (sshclient.ExecResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.runs = append(f.runs, command)
	f.timeout = timeout
	if f.block {
		f.mu.Unlock()
		f.started <- struct{}{}
		<-ctx.Done()
		close(f.done)
		f.mu.Lock()
	}
	return f.result, f.runErr
}

func (f *fakeBackend) Upload(ctx context.Context, h HostInfo, local, remote string, overwrite bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.uploads++
	return 42, nil
}

func (f *fakeBackend) Download(ctx context.Context, h HostInfo, remote, local string, overwrite bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.downs++
	return 7, nil
}

func (f *fakeBackend) Refused(h HostInfo, tool, text, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refused = append(f.refused, tool+"|"+text+"|"+reason)
}

type tokenRT struct{ token string }

func (t tokenRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func start(t *testing.T, b Backend) (string, int) {
	u, p, _ := startSrv(t, b)
	return u, p
}

func startSrv(t *testing.T, b Backend) (string, int, *http.Server) {
	t.Helper()
	l, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	srv := Serve(l, testToken, "test", b)
	t.Cleanup(func() { _ = srv.Close() })
	port := l.Addr().(*net.TCPAddr).Port
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp", port, srv
}

func connect(t *testing.T, url string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: tokenRT{testToken}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String(), res.IsError
}

func testHosts() []HostInfo {
	return []HostInfo{
		{ID: "h1", Name: "web", Address: "10.0.0.1", Port: 22, User: "root", Access: model.MCPAccessFull},
		{ID: "h2", Name: "db", Address: "10.0.0.2", Port: 22, User: "u", Access: model.MCPAccessRead},
		{ID: "h3", Name: "dup", Address: "a", Port: 22, User: "u", Access: model.MCPAccessFull},
		{ID: "h4", Name: "dup", Address: "b", Port: 22, User: "u", Access: model.MCPAccessFull},
	}
}

func rawPost(t *testing.T, url string, mod func(*http.Request)) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	mod(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func TestRejectsMissingToken(t *testing.T) {
	b := &fakeBackend{}
	url, _ := start(t, b)
	if c := rawPost(t, url, func(r *http.Request) {}); c != 401 {
		t.Fatalf("status %d", c)
	}
	if b.calls != 0 {
		t.Fatal("backend called")
	}
}

func TestRejectsWrongToken(t *testing.T) {
	b := &fakeBackend{}
	url, _ := start(t, b)
	c := rawPost(t, url, func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") })
	if c != 401 {
		t.Fatalf("status %d", c)
	}
	c = rawPost(t, url, func(r *http.Request) { r.Header.Set("Authorization", testToken) })
	if c != 401 {
		t.Fatalf("status %d", c)
	}
	if b.calls != 0 {
		t.Fatal("backend called")
	}
}

func TestRejectsForeignHostHeader(t *testing.T) {
	b := &fakeBackend{}
	url, _ := start(t, b)
	c := rawPost(t, url, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Host = "evil.example:80"
	})
	if c != 403 {
		t.Fatalf("status %d", c)
	}
	if b.calls != 0 {
		t.Fatal("backend called")
	}
}

func TestAcceptsLocalhostHost(t *testing.T) {
	b := &fakeBackend{}
	url, port := start(t, b)
	c := rawPost(t, url, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Host = "localhost:" + strconv.Itoa(port)
	})
	if c != 200 {
		t.Fatalf("status %d", c)
	}
}

func TestRejectsOrigin(t *testing.T) {
	b := &fakeBackend{}
	url, _ := start(t, b)
	c := rawPost(t, url, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Origin", "http://evil.example")
	})
	if c != 403 {
		t.Fatalf("status %d", c)
	}
	if b.calls != 0 {
		t.Fatal("backend called")
	}
}

func TestListenLoopbackOnly(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if !l.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatalf("addr %v", l.Addr())
	}
}

func TestToolsListed(t *testing.T) {
	url, _ := start(t, &fakeBackend{})
	s := connect(t, url)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"list_hosts": true, "run_command": true, "upload_file": true, "download_file": true}
	if len(res.Tools) != 4 {
		t.Fatalf("tools %d", len(res.Tools))
	}
	for _, tl := range res.Tools {
		if !want[tl.Name] {
			t.Fatalf("unexpected %s", tl.Name)
		}
	}
}

func TestListHosts(t *testing.T) {
	url, _ := start(t, &fakeBackend{hosts: testHosts()})
	s := connect(t, url)
	text, isErr := call(t, s, "list_hosts", nil)
	if isErr || !strings.Contains(text, `"web"`) || !strings.Contains(text, `"h2"`) {
		t.Fatalf("%v %s", isErr, text)
	}
}

func TestHostsErrorSurfaces(t *testing.T) {
	url, _ := start(t, &fakeBackend{hostsErr: errors.New("Клиент заблокирован: разблокируйте его")})
	s := connect(t, url)
	text, isErr := call(t, s, "list_hosts", nil)
	if !isErr || !strings.Contains(text, "Клиент заблокирован") {
		t.Fatalf("%v %s", isErr, text)
	}
}

func TestHostByNameAndID(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	for _, h := range []string{"web", "h1"} {
		_, isErr := call(t, s, "run_command", map[string]any{"host": h, "command": "ls"})
		if isErr {
			t.Fatalf("host %s failed", h)
		}
	}
	if len(b.runs) != 2 {
		t.Fatalf("runs %v", b.runs)
	}
}

func TestAmbiguousNameListsIDs(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "run_command", map[string]any{"host": "dup", "command": "ls"})
	if !isErr || !strings.Contains(text, "h3") || !strings.Contains(text, "h4") {
		t.Fatalf("%v %s", isErr, text)
	}
	if len(b.runs) != 0 {
		t.Fatal("run called")
	}
}

func TestClosedHostLooksMissing(t *testing.T) {
	// Backend.Hosts returns only open hosts, so a closed host is absent.
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	a, e1 := call(t, s, "run_command", map[string]any{"host": "closed-host", "command": "ls"})
	c, e2 := call(t, s, "run_command", map[string]any{"host": "nonexistent", "command": "ls"})
	if !e1 || !e2 {
		t.Fatal("expected errors")
	}
	if strings.ReplaceAll(a, "closed-host", "X") != strings.ReplaceAll(c, "nonexistent", "X") {
		t.Fatalf("texts differ: %q vs %q", a, c)
	}
	if len(b.runs) != 0 {
		t.Fatal("run called")
	}
}

func TestReadModeRefusesCommandWithoutRun(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "run_command", map[string]any{"host": "db", "command": "rm -rf /"})
	if !isErr || !strings.Contains(text, "Только просмотр") {
		t.Fatalf("%v %s", isErr, text)
	}
	if len(b.runs) != 0 {
		t.Fatal("run called")
	}
	if len(b.refused) != 1 || !strings.HasPrefix(b.refused[0], "run_command|rm -rf /|") {
		t.Fatalf("refused %v", b.refused)
	}
	if _, isErr := call(t, s, "run_command", map[string]any{"host": "db", "command": "ls -l | head"}); isErr {
		t.Fatal("allowed command refused")
	}
}

func TestReadModeRefusesUpload(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "upload_file", map[string]any{"host": "db", "local_path": "C:\\a", "remote_path": "/a"})
	if !isErr || !strings.Contains(text, "Только просмотр") {
		t.Fatalf("%v %s", isErr, text)
	}
	if b.uploads != 0 || len(b.refused) != 1 {
		t.Fatalf("uploads %d refused %v", b.uploads, b.refused)
	}
}

func TestReadModeAllowsDownload(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "download_file", map[string]any{"host": "db", "remote_path": "/a", "local_path": "C:\\a"})
	if isErr || b.downs != 1 || !strings.Contains(text, "7") {
		t.Fatalf("%v %d %s", isErr, b.downs, text)
	}
}

func TestFullModeUpload(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "upload_file", map[string]any{"host": "web", "local_path": "C:\\a", "remote_path": "/a"})
	if isErr || b.uploads != 1 || !strings.Contains(text, "42") {
		t.Fatalf("%v %d %s", isErr, b.uploads, text)
	}
}

func TestTimeoutClamped(t *testing.T) {
	b := &fakeBackend{hosts: testHosts()}
	url, _ := start(t, b)
	s := connect(t, url)
	for _, c := range []struct {
		in   any
		want time.Duration
	}{{nil, 60 * time.Second}, {0, 60 * time.Second}, {9999, 600 * time.Second}, {5, 5 * time.Second}} {
		args := map[string]any{"host": "web", "command": "ls"}
		if c.in != nil {
			args["timeout_sec"] = c.in
		}
		call(t, s, "run_command", args)
		if b.timeout != c.want {
			t.Fatalf("in %v: got %v want %v", c.in, b.timeout, c.want)
		}
	}
}

func TestRunResultFields(t *testing.T) {
	b := &fakeBackend{hosts: testHosts(), result: sshclient.ExecResult{Stdout: "out", Stderr: "err", ExitCode: 3, Truncated: true}}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "run_command", map[string]any{"host": "web", "command": "ls"})
	if isErr {
		t.Fatal(text)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(text, "\n", 2)[0]), &m); err != nil {
		t.Fatal(err, text)
	}
	if m["stdout"] != "out" || m["stderr"] != "err" || m["exit_code"] != float64(3) || m["truncated"] != true || m["timed_out"] != false {
		t.Fatalf("%v", m)
	}
}

func TestTimedOutTextWarns(t *testing.T) {
	b := &fakeBackend{hosts: testHosts(), result: sshclient.ExecResult{TimedOut: true, ExitCode: -1}}
	url, _ := start(t, b)
	s := connect(t, url)
	text, _ := call(t, s, "run_command", map[string]any{"host": "web", "command": "sleep 99"})
	if !strings.Contains(text, "может ещё выполняться") {
		t.Fatal(text)
	}
}

func TestBackendErrorIsError(t *testing.T) {
	b := &fakeBackend{hosts: testHosts(), runErr: errors.New("Ожидается вход в окне клиента, повторите команду")}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "run_command", map[string]any{"host": "web", "command": "ls"})
	if !isErr || !strings.Contains(text, "Ожидается вход") {
		t.Fatalf("%v %s", isErr, text)
	}
}

func TestPromptExact(t *testing.T) {
	cmd := `claude mcp add --transport http --scope user sshclient http://127.0.0.1:47821/mcp --header "Authorization: Bearer TOK"`
	want := "Подключи MCP-сервер SSH-клиента sshclient. Он работает на этом компьютере и даёт доступ к моим серверам.\n\n" +
		"1. Выполни команду:\n" + cmd + "\n" +
		"2. Скажи мне перезапустить сессию, если новые инструменты не появились сразу.\n\n" +
		"Инструменты: list_hosts показывает доступные серверы, run_command выполняет команду, upload_file и download_file передают файлы.\n\n" +
		"Для серверов используй эти инструменты, а не прямой ssh: ключи и парольные фразы хранит клиент, они тебе не нужны. Если клиент попросит вход, скажи мне ответить в его окне и повтори команду. Если сервера нет в list_hosts, его нужно открыть в клиенте: «Доступ по MCP» в настройках хоста. Токен никуда не записывай и не показывай, кроме этой команды."
	if got := Prompt(47821, "TOK"); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestAddCommandExact(t *testing.T) {
	want := `claude mcp add --transport http --scope user sshclient http://127.0.0.1:47821/mcp --header "Authorization: Bearer TOK"`
	if got := AddCommand(47821, "TOK"); got != want {
		t.Fatal(got)
	}
}

func blockingBackend() *fakeBackend {
	return &fakeBackend{hosts: testHosts(), block: true, started: make(chan struct{}, 1), done: make(chan struct{})}
}

func postRun(ctx context.Context, url string) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"host":"web","command":"sleep 100"}}}`
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+testToken)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

func waitDone(t *testing.T, b *fakeBackend) {
	t.Helper()
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		t.Fatal("backend context was not cancelled")
	}
}

func TestClientDisconnectCancelsBackend(t *testing.T) {
	b := blockingBackend()
	url, _ := start(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	go postRun(ctx, url)
	<-b.started
	cancel()
	waitDone(t, b)
}

func TestServerCloseCancelsBackend(t *testing.T) {
	b := blockingBackend()
	url, _, srv := startSrv(t, b)
	go postRun(context.Background(), url)
	<-b.started
	_ = srv.Close()
	waitDone(t, b)
}

func TestCancelledReturnsPartialOutput(t *testing.T) {
	r, _, _ := cancelledResult(sshclient.ExecResult{Stdout: "part", Stderr: "e", ExitCode: -1})
	var sb strings.Builder
	for _, c := range r.Content {
		sb.WriteString(c.(*mcp.TextContent).Text)
	}
	text := sb.String()
	if !r.IsError || !strings.Contains(text, "part") || !strings.Contains(text, "отменён") || !strings.Contains(text, "может ещё выполняться") {
		t.Fatalf("%v %s", r.IsError, text)
	}
}

func TestUnknownAccessLooksMissing(t *testing.T) {
	b := &fakeBackend{hosts: []HostInfo{
		{ID: "e1", Name: "empty", Access: ""},
		{ID: "u1", Name: "upper", Access: "READ"},
	}}
	url, _ := start(t, b)
	s := connect(t, url)
	missing, _ := call(t, s, "run_command", map[string]any{"host": "nope", "command": "ls"})
	for _, ref := range []string{"empty", "e1", "upper", "u1"} {
		text, isErr := call(t, s, "run_command", map[string]any{"host": ref, "command": "ls"})
		if !isErr || strings.ReplaceAll(text, ref, "X") != strings.ReplaceAll(missing, "nope", "X") {
			t.Fatalf("%s: %q vs %q", ref, text, missing)
		}
		_, isErr = call(t, s, "upload_file", map[string]any{"host": ref, "local_path": "C:\a", "remote_path": "/a"})
		if !isErr {
			t.Fatal("upload allowed")
		}
	}
	if len(b.runs) != 0 || b.uploads != 0 {
		t.Fatal("backend ran")
	}
}

func TestRejectsEmptyOriginHeader(t *testing.T) {
	b := &fakeBackend{}
	url, _ := start(t, b)
	c := rawPost(t, url, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header["Origin"] = []string{""}
	})
	if c != 403 {
		t.Fatalf("status %d", c)
	}
}

func TestBackendCancelledWithLiveContext(t *testing.T) {
	b := &fakeBackend{hosts: testHosts(), result: sshclient.ExecResult{Stdout: "part", ExitCode: -1}, runErr: context.Canceled}
	url, _ := start(t, b)
	s := connect(t, url)
	text, isErr := call(t, s, "run_command", map[string]any{"host": "web", "command": "ls"})
	if !isErr || !strings.Contains(text, "part") || !strings.Contains(text, "отменён") || strings.Contains(text, "context canceled") {
		t.Fatalf("%v %s", isErr, text)
	}
}
