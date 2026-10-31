// Package mcpserver is the local MCP server that gives an AI client access to
// the hosts of the app. It knows nothing about the vault or connections: all
// of that sits behind Backend.
package mcpserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"sshclient/internal/sshclient"
)

// HostInfo is what the server shows to a client about an open host.
type HostInfo struct {
	ID, Name, Group, Address, User, Access string
	Port                                   int
}

// Backend is implemented by the app.
type Backend interface {
	// Hosts returns only hosts open for MCP; it fails while the app is locked.
	Hosts(ctx context.Context) ([]HostInfo, error)
	Run(ctx context.Context, h HostInfo, command string, timeout time.Duration) (sshclient.ExecResult, error)
	Upload(ctx context.Context, h HostInfo, local, remote string, overwrite bool) (int64, error)
	Download(ctx context.Context, h HostInfo, remote, local string, overwrite bool) (int64, error)
	// Refused records a refusal by the host access mode in the journal.
	Refused(h HostInfo, tool, text, reason string)
}

// Listen opens a loopback-only listener on the given port.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// Serve starts serving MCP on l and returns the server; the caller closes it.
// There is no write timeout: a command call may run for 10 minutes.
func Serve(l net.Listener, token string, version string, b Backend) *http.Server {
	// A server per request: tool handlers see the request context, so a
	// client disconnect or Close cancels the Backend call.
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "sshclient", Version: version}, nil)
		registerTools(srv, b, r.Context())
		return srv
	},
		&mcp.StreamableHTTPOptions{Stateless: true})

	port := 0
	if a, ok := l.Addr().(*net.TCPAddr); ok {
		port = a.Port
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", guard(port, token, h))
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(l) }() //nolint:gosec // G104: Serve only ends when the server is closed
	return hs
}

// guard rejects a foreign Host, any Origin and a wrong token before the MCP
// handler (and so the Backend) sees the request.
func guard(port int, token string, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	okHost := map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !okHost[strings.ToLower(r.Host)] || len(r.Header.Values("Origin")) > 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AddCommand is the command that registers this server in Claude Code.
func AddCommand(port int, token string) string {
	return fmt.Sprintf(`claude mcp add --transport http --scope user sshclient http://127.0.0.1:%d/mcp --header "Authorization: Bearer %s"`, port, token)
}

// Prompt is the text the owner pastes into an AI session to connect the server.
func Prompt(port int, token string) string {
	return "Подключи MCP-сервер SSH-клиента sshclient. Он работает на этом компьютере и даёт доступ к моим серверам.\n\n" +
		"1. Выполни команду:\n" + AddCommand(port, token) + "\n" +
		"2. Скажи мне перезапустить сессию, если новые инструменты не появились сразу.\n\n" +
		"Инструменты: list_hosts показывает доступные серверы, run_command выполняет команду, upload_file и download_file передают файлы.\n\n" +
		"Для серверов используй эти инструменты, а не прямой ssh: ключи и парольные фразы хранит клиент, они тебе не нужны. Если клиент попросит вход, скажи мне ответить в его окне и повтори команду. Если сервера нет в list_hosts, его нужно открыть в клиенте: раздел «MCP». Токен никуда не записывай и не показывай, кроме этой команды."
}
