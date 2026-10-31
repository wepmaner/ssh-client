package app

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/appdir"
	"sshclient/internal/mcpserver"
	"sshclient/internal/model"
	"sshclient/internal/sshclient"
	"sshclient/internal/store"
)

const (
	// mcpOutputLimit is how much of each stream of a command the client gets back.
	mcpOutputLimit = 200 << 10
	// mcpTransferTime limits one file transfer.
	mcpTransferTime = 30 * time.Minute

	mcpToolRun      = "run_command"
	mcpToolUpload   = "upload_file"
	mcpToolDownload = "download_file"
)

//nolint:staticcheck // ST1005: Russian text for the client starts with a capital letter
var (
	errMCPLocked       = errors.New("Клиент заблокирован: разблокируйте его")
	errMCPJournal      = errors.New("Не удалось записать действие в журнал клиента, поэтому оно не выполнено. Сообщите об этом владельцу.")
	errMCPTransferTime = errors.New("Передача не уложилась в 30 минут: файл мог быть передан не полностью. Проверьте результат на сервере, прежде чем повторять.")
)

// mcpCheck decides whether a host in its present mode may run an action; a refusal is journalled.
type mcpCheck func(model.Host) error

// mcpAnyMode is the check of an action every open mode allows.
func mcpAnyMode(model.Host) error { return nil }

// mcpBackend is the app behind one run of the MCP server. The HostInfo a tool got is only as fresh
// as its Hosts call: every action reads the host's mode again.
type mcpBackend struct{ a *App }

var _ mcpserver.Backend = (*mcpBackend)(nil)

// Hosts lists the hosts open for MCP.
func (b *mcpBackend) Hosts(context.Context) ([]mcpserver.HostInfo, error) {
	a := b.a
	if !a.storeUnlocked() {
		return nil, errMCPLocked
	}
	groups := map[string]string{}
	for _, g := range a.storeRef().Groups() {
		groups[g.ID] = g.Name
	}
	out := []mcpserver.HostInfo{}
	for _, h := range a.storeRef().Hosts() {
		if mcpOpen(h) {
			out = append(out, mcpserver.HostInfo{
				ID: h.ID, Name: h.Name, Group: groups[h.GroupID], Address: h.Address, User: h.User,
				Access: h.MCPAccess, Port: h.Port,
			})
		}
	}
	return out, nil
}

// mcpOpen fails shut: an unknown mode is a closed host.
func mcpOpen(h model.Host) bool {
	return h.MCPAccess == model.MCPAccessRead || h.MCPAccess == model.MCPAccessFull
}

// current returns the host as it is now; a host closed or deleted since answers like a missing one.
func (b *mcpBackend) current(h mcpserver.HostInfo) (model.Host, error) {
	if !b.a.storeUnlocked() {
		return model.Host{}, errMCPLocked
	}
	hosts := b.a.storeRef().Hosts()
	i := slices.IndexFunc(hosts, func(x model.Host) bool { return x.ID == h.ID })
	if i < 0 || !mcpOpen(hosts[i]) {
		return model.Host{}, mcpserver.HostNotFound(h.Name)
	}
	return hosts[i], nil
}

// begin starts a call on the host's connection. What it checks after mcpBegin covers what happened
// meanwhile. A close of the host saves first and releases after, and a stop of the server drops
// mcpLive first and releases after: so a call either is seen by that release or sees the change
// here, and then it takes its hold off itself. The wait for a slot or a login has no limit the
// owner can see, so allowed judges the action again by the mode the host has now.
func (b *mcpBackend) begin(ctx context.Context, h mcpserver.HostInfo, allowed mcpCheck) (*ssh.Client, *mcpCall, error) {
	a := b.a
	client, call, err := a.mcpBegin(ctx, h.ID)
	host, curErr := b.current(h)
	live := a.mcpLive.Load()
	if err == nil && curErr == nil && live == b {
		if err := allowed(host); err != nil {
			a.mcpEnd(call)
			return nil, nil, err
		}
		return client, call, nil
	}
	if call != nil {
		a.mcpEnd(call)
	}
	if live == nil || (curErr != nil && !errors.Is(curErr, errMCPLocked)) {
		a.mcpReleaseHost(h.ID)
	}
	switch {
	case live != b:
		return nil, nil, context.Canceled
	case curErr != nil:
		return nil, nil, curErr
	case errors.Is(err, sshclient.ErrCancelled):
		return nil, nil, context.Canceled
	}
	return nil, nil, err
}

// journal records the action before it runs: without the record it does not run.
func (b *mcpBackend) journal(h model.Host, tool, text string) (string, error) {
	r := model.MCPRecord{
		ID: uuid.NewString(), Time: time.Now().UTC(), HostID: h.ID, HostName: h.Name,
		Tool: tool, Text: text, Access: h.MCPAccess,
	}
	if err := b.a.storeRef().MCPJournalAdd(r); err != nil {
		if errors.Is(err, store.ErrLocked) {
			return "", errMCPLocked
		}
		log.Printf("mcp journal: %v", err)
		return "", errMCPJournal
	}
	return r.ID, nil
}

// finish adds the result to the record; a vault locked meanwhile keeps the record without it.
func (b *mcpBackend) finish(id string, exit *int, start time.Time) {
	err := b.a.storeRef().MCPJournalFinish(id, exit, time.Since(start).Milliseconds())
	if err != nil && !errors.Is(err, store.ErrLocked) {
		log.Printf("mcp journal: %v", err)
	}
}

// Refused records a refusal.
func (b *mcpBackend) Refused(h mcpserver.HostInfo, tool, text, reason string) {
	r := model.MCPRecord{
		ID: uuid.NewString(), Time: time.Now().UTC(), HostID: h.ID, HostName: h.Name,
		Tool: tool, Text: text, Access: h.Access, Refused: reason,
	}
	if s := b.a.storeRef(); s != nil {
		if err := s.MCPJournalAdd(r); err != nil {
			log.Printf("mcp journal: %v", err)
		}
	}
}

// refused records the refusal of an action on the host as it is now.
func (b *mcpBackend) refused(h model.Host, tool, text, reason string) {
	b.Refused(mcpserver.HostInfo{ID: h.ID, Name: h.Name, Access: h.MCPAccess}, tool, text, reason)
}

// Run runs a command. A command that ended by a cancel returns context.Canceled, whatever the
// connection did on the way: the client must learn that it may still be running.
func (b *mcpBackend) Run(ctx context.Context, h mcpserver.HostInfo, command string, timeout time.Duration) (sshclient.ExecResult, error) {
	res := sshclient.ExecResult{ExitCode: -1}
	host, err := b.current(h)
	if err != nil {
		return res, err
	}
	allowed := func(host model.Host) error {
		if host.MCPAccess == model.MCPAccessFull {
			return nil
		}
		if cerr := mcpserver.CheckReadOnly(command); cerr != nil {
			b.refused(host, mcpToolRun, command, cerr.Error())
			return mcpserver.ReadOnlyCommand(cerr)
		}
		return nil
	}
	if err := allowed(host); err != nil {
		return res, err
	}
	id, err := b.journal(host, mcpToolRun, command)
	if err != nil {
		return res, err
	}
	start := time.Now()
	client, call, err := b.begin(ctx, h, allowed)
	if err != nil {
		b.finish(id, nil, start)
		return res, err
	}
	defer b.a.mcpEnd(call)
	runCtx, cancel := context.WithTimeout(call.ctx, timeout)
	defer cancel()
	res, err = sshclient.Exec(runCtx, client, command, mcpOutputLimit)
	if err != nil && errors.Is(call.ctx.Err(), context.Canceled) {
		err = context.Canceled
	}
	var exit *int
	if err == nil && !res.TimedOut {
		exit = &res.ExitCode
	}
	b.finish(id, exit, start)
	return res, err
}

// Upload sends a local file to the host.
func (b *mcpBackend) Upload(ctx context.Context, h mcpserver.HostInfo, local, remote string, overwrite bool) (int64, error) {
	host, err := b.current(h)
	if err != nil {
		return 0, err
	}
	text := local + " -> " + remote
	allowed := func(host model.Host) error {
		if host.MCPAccess == model.MCPAccessFull {
			return nil
		}
		b.refused(host, mcpToolUpload, text, "загрузка файлов запрещена в режиме «Только просмотр»")
		return errors.New(mcpserver.ReadOnlyUpload) //nolint:staticcheck // ST1005: Russian text for the client
	}
	if err := allowed(host); err != nil {
		return 0, err
	}
	return b.transfer(ctx, h, host, mcpToolUpload, text, local, allowed, func(ctx context.Context, c *ssh.Client) (int64, error) {
		return sshclient.Upload(ctx, c, local, remote, overwrite)
	})
}

// Download fetches a file of the host.
func (b *mcpBackend) Download(ctx context.Context, h mcpserver.HostInfo, remote, local string, overwrite bool) (int64, error) {
	host, err := b.current(h)
	if err != nil {
		return 0, err
	}
	text := remote + " -> " + local
	return b.transfer(ctx, h, host, mcpToolDownload, text, local, mcpAnyMode, func(ctx context.Context, c *ssh.Client) (int64, error) {
		return sshclient.Download(ctx, c, remote, local, overwrite)
	})
}

// transfer runs one file transfer: the check of the local path, the record, the connection, and the
// limit of mcpTransferTime. The cancel of a transfer is context.Canceled whatever the closed sftp
// session answered.
func (b *mcpBackend) transfer(ctx context.Context, h mcpserver.HostInfo, host model.Host, tool, text, local string, allowed mcpCheck,
	run func(context.Context, *ssh.Client) (int64, error)) (int64, error) {
	dir, err := appdir.Dir()
	if err == nil {
		err = checkLocalPath(dir, local)
	}
	if err != nil {
		b.refused(host, tool, text, err.Error())
		return 0, err
	}
	id, err := b.journal(host, tool, text)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	defer func() { b.finish(id, nil, start) }()
	client, call, err := b.begin(ctx, h, allowed)
	if err != nil {
		return 0, err
	}
	defer b.a.mcpEnd(call)
	runCtx, cancel := context.WithTimeout(call.ctx, mcpTransferTime)
	defer cancel()
	n, err := run(runCtx, client)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(call.ctx.Err(), context.Canceled):
		return 0, context.Canceled
	case runCtx.Err() != nil:
		return 0, errMCPTransferTime
	}
	return 0, err
}
