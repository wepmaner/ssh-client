package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

const (
	defaultTimeoutSec = 60
	maxTimeoutSec     = 600
)

type listHostsIn struct{}

type runIn struct {
	Host       string `json:"host" jsonschema:"Host ID or exact host name from list_hosts"`
	Command    string `json:"command" jsonschema:"Shell command to run on the host (no pseudo-terminal)"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"Time limit in seconds; default 60, maximum 600"`
}

type uploadIn struct {
	Host       string `json:"host" jsonschema:"Host ID or exact host name from list_hosts"`
	LocalPath  string `json:"local_path" jsonschema:"Absolute path of the local file to upload"`
	RemotePath string `json:"remote_path" jsonschema:"Destination path on the host"`
	Overwrite  bool   `json:"overwrite,omitempty" jsonschema:"Replace the destination if it exists; default false"`
}

type downloadIn struct {
	Host       string `json:"host" jsonschema:"Host ID or exact host name from list_hosts"`
	RemotePath string `json:"remote_path" jsonschema:"Path of the file on the host"`
	LocalPath  string `json:"local_path" jsonschema:"Absolute local destination path"`
	Overwrite  bool   `json:"overwrite,omitempty" jsonschema:"Replace the local file if it exists; default false"`
}

type hostOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Mode    string `json:"mode"`
}

type runOut struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
	TimedOut  bool   `json:"timed_out"`
}

func errResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func textResult(parts ...string) (*mcp.CallToolResult, any, error) {
	r := &mcp.CallToolResult{}
	for _, p := range parts {
		r.Content = append(r.Content, &mcp.TextContent{Text: p})
	}
	return r, nil, nil
}

const transferCancelled = "Передача отменена: файл мог быть передан не полностью. Проверьте результат на сервере, прежде чем повторять."

func cancelledResult(res sshclient.ExecResult) (*mcp.CallToolResult, any, error) {
	r, _, _ := textResult(
		jsonText(runOut{res.Stdout, res.Stderr, res.ExitCode, res.Truncated, res.TimedOut}),
		"Вызов отменён: команда может ещё выполняться на сервере. Проверьте это, прежде чем запускать её повторно. Показан вывод, полученный до отмены.")
	r.IsError = true
	return r, nil, nil
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func notFoundText(name string) string {
	return fmt.Sprintf("Сервер «%s» не найден. Список доступных серверов даёт list_hosts; если сервера там нет, его нужно открыть в клиенте: «Доступ по MCP» в настройках хоста.", name)
}

// resolve finds an open host by ID or exact name. The error text is ready to
// show to the client; closed hosts are absent from Hosts and so look missing.
func resolve(ctx context.Context, b Backend, ref string) (HostInfo, error) {
	hosts, err := b.Hosts(ctx)
	if err != nil {
		return HostInfo{}, err
	}
	// A host with an unknown access mode is treated as closed: fail shut.
	var open []HostInfo
	for _, h := range hosts {
		if h.Access == model.MCPAccessRead || h.Access == model.MCPAccessFull {
			open = append(open, h)
		}
	}
	for _, h := range open {
		if h.ID == ref {
			return h, nil
		}
	}
	var found []HostInfo
	for _, h := range open {
		if h.Name == ref {
			found = append(found, h)
		}
	}
	switch len(found) {
	case 0:
		return HostInfo{}, errors.New(notFoundText(ref))
	case 1:
		return found[0], nil
	}
	ids := make([]string, len(found))
	for i, h := range found {
		ids[i] = h.ID
	}
	return HostInfo{}, fmt.Errorf("Название «%s» неоднозначно. Укажите идентификатор хоста: %s.", ref, strings.Join(ids, ", "))
}

const readOnlyUpload = "Режим хоста «Только просмотр»: загрузка файлов на сервер запрещена. Скачивание и безопасные команды просмотра разрешены; режим меняет владелец в настройках хоста."

// withRequest ties the context of a tool call to the HTTP request, so that a
// client disconnect or a server shutdown reaches the Backend.
func withRequest(ctx, reqCtx context.Context) (context.Context, func()) {
	c, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(reqCtx, cancel)
	return c, func() { stop(); cancel() }
}

func registerTools(s *mcp.Server, b Backend, reqCtx context.Context) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_hosts",
		Description: "List the servers available through this client: id, name, group, address, port, user and access mode (read = read-only, full). Only hosts the owner opened for MCP are shown.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listHostsIn) (*mcp.CallToolResult, any, error) {
		ctx, done := withRequest(ctx, reqCtx)
		defer done()
		hosts, err := b.Hosts(ctx)
		if err != nil {
			return errResult(err.Error())
		}
		out := make([]hostOut, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, hostOut{h.ID, h.Name, h.Group, h.Address, h.Port, h.User, h.Access})
		}
		return textResult(jsonText(out))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "run_command",
		Description: "Run a shell command on a host over SSH (no pseudo-terminal). Returns stdout, stderr (last 200 KB each), exit_code, truncated and timed_out. On timeout the command may still be running on the server. Hosts in read-only mode accept only a fixed set of read-only commands.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, any, error) {
		ctx, done := withRequest(ctx, reqCtx)
		defer done()
		h, err := resolve(ctx, b, in.Host)
		if err != nil {
			return errResult(err.Error())
		}
		if h.Access != model.MCPAccessFull {
			if cerr := CheckReadOnly(in.Command); cerr != nil {
				b.Refused(h, "run_command", in.Command, cerr.Error())
				return errResult("Режим хоста «Только просмотр»: команда не выполнена. " + cerr.Error())
			}
		}
		sec := in.TimeoutSec
		if sec <= 0 {
			sec = defaultTimeoutSec
		}
		if sec > maxTimeoutSec {
			sec = maxTimeoutSec
		}
		res, err := b.Run(ctx, h, in.Command, time.Duration(sec)*time.Second)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return cancelledResult(res)
		}
		if err != nil {
			return errResult(err.Error())
		}
		parts := []string{jsonText(runOut{res.Stdout, res.Stderr, res.ExitCode, res.Truncated, res.TimedOut})}
		if res.TimedOut {
			parts = append(parts, "Время вышло: команда может ещё выполняться на сервере. Проверьте это (например, ps), прежде чем запускать её повторно.")
		}
		return textResult(parts...)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "upload_file",
		Description: "Upload one local file to a host over SFTP. The local path must be absolute. An existing remote file is not replaced unless overwrite is true. Not allowed on read-only hosts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in uploadIn) (*mcp.CallToolResult, any, error) {
		ctx, done := withRequest(ctx, reqCtx)
		defer done()
		h, err := resolve(ctx, b, in.Host)
		if err != nil {
			return errResult(err.Error())
		}
		if h.Access != model.MCPAccessFull {
			b.Refused(h, "upload_file", in.LocalPath+" -> "+in.RemotePath, "загрузка файлов запрещена в режиме «Только просмотр»")
			return errResult(readOnlyUpload)
		}
		n, err := b.Upload(ctx, h, in.LocalPath, in.RemotePath, in.Overwrite)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return errResult(transferCancelled)
		}
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(jsonText(map[string]int64{"size": n}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "download_file",
		Description: "Download one file from a host over SFTP. The local path must be absolute. An existing local file is not replaced unless overwrite is true. Allowed on read-only hosts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in downloadIn) (*mcp.CallToolResult, any, error) {
		ctx, done := withRequest(ctx, reqCtx)
		defer done()
		h, err := resolve(ctx, b, in.Host)
		if err != nil {
			return errResult(err.Error())
		}
		n, err := b.Download(ctx, h, in.RemotePath, in.LocalPath, in.Overwrite)
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return errResult(transferCancelled)
		}
		if err != nil {
			return errResult(err.Error())
		}
		return textResult(jsonText(map[string]int64{"size": n}))
	})
}
