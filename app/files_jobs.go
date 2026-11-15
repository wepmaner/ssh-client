package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/sshclient"
)

const (
	jobUpload   = "upload"
	jobDownload = "download"
	jobDelete   = "delete"
	jobCopy     = "copy" // inside the server, streamed through the client

	jobQueued    = "queued"
	jobRunning   = "running"
	jobWaiting   = "waiting"
	jobDone      = "done"
	jobFailed    = "failed"
	jobCancelled = "cancelled"

	// filesRetryTime is how long a transfer reconnects after the connection dropped before it fails.
	filesRetryTime = 10 * time.Minute
	// jobEmitEvery spaces the progress events of a job: at most 5 a second, plus every state change.
	jobEmitEvery = 200 * time.Millisecond
	// jobDetailLines caps the failed entries listed in a job's details.
	jobDetailLines = 50
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errFilesJobNotFound = errors.New("Задача не найдена")
	errFilesJobActive   = errors.New("Задача ещё выполняется")
	errFilesParentFail  = errors.New("Папка не создана")
	errFilesLinkGone    = errors.New("Связь с сервером не восстановилась за 10 минут")
	errFilesCopyItself  = errors.New("Нельзя скопировать папку в неё саму")
)

// filesDefaultBackoff is the pause before reconnect attempt n (from 1): 1, 2, 5, 10, then 30 s.
func filesDefaultBackoff(attempt int) time.Duration {
	steps := []time.Duration{1, 2, 5, 10, 30}
	i := min(max(attempt, 1), len(steps)) - 1
	return steps[i] * time.Second
}

// filesJob is one transfer or delete of a tab. The tab's runner runs one job at a time; while it
// runs, the job is a user of its host's connection with its own sftp session, and after the
// connection drops it reconnects with pauses and resumes the current file from its part file.
type filesJob struct {
	tab     *filesTab
	id      string
	kind    string
	sources []string
	dest    string
	replace bool
	now     chan struct{} // FilesJobRetry on a waiting job: the pause ends at once

	// Touched by the runner goroutine only.
	sc        *sftp.Client
	scClose   func()
	cl        *ssh.Client
	acked     map[string]int64  // confirmed bytes of upload parts, kept across runs
	finished  map[string]bool   // targets completed by an earlier run: Retry does not copy them again
	copyNames map[string]string // copy: the top-level target name of each source, chosen once per job
	lost      bool              // the connection dropped: the next connect pauses first
	attempt   int
	outage    time.Time // the drop that is not yet followed by progress
	lastErr   error
	fileBase  int64 // bytes of the files done in this run
	failures  []string
	changed   []string // remote folders the job changed

	mu        sync.Mutex
	dto       FilesJobDTO
	ctx       context.Context
	cancel    context.CancelFunc // of the current run; nil while not running
	cancelled bool               // FilesJobCancel or the tab's end asked
	parts     map[string]bool    // part files written and not yet moved into place
	lastEmit  time.Time
	speedAt   time.Time
	speedDone int64
}

func (j *filesJob) event() string { return "files:job:" + j.tab.id }

// update changes the job's DTO and emits it; a progress update (force false) goes out at most every
// jobEmitEvery.
func (j *filesJob) update(force bool, fn func(d *FilesJobDTO)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.updateLocked(force, fn)
}

// updateLocked is update; the caller holds mu.
func (j *filesJob) updateLocked(force bool, fn func(d *FilesJobDTO)) {
	fn(&j.dto)
	now := time.Now()
	switch {
	case j.dto.State != jobRunning:
		j.dto.SpeedBps, j.speedAt = 0, time.Time{}
	case j.speedAt.IsZero():
		j.speedAt, j.speedDone = now, j.dto.DoneBytes
	default:
		if dt := now.Sub(j.speedAt); dt >= 500*time.Millisecond {
			inst := float64(j.dto.DoneBytes-j.speedDone) / dt.Seconds()
			if j.dto.SpeedBps > 0 {
				inst = 0.6*inst + 0.4*float64(j.dto.SpeedBps)
			}
			j.dto.SpeedBps = int64(math.Max(inst, 0))
			j.speedAt, j.speedDone = now, j.dto.DoneBytes
		}
	}
	if !force && now.Sub(j.lastEmit) < jobEmitEvery {
		return
	}
	j.lastEmit = now
	j.tab.a.events().Emit(j.event(), j.dto)
}

func (j *filesJob) snapshot() FilesJobDTO {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.dto
}

func (j *filesJob) finishedState() bool {
	switch j.snapshot().State {
	case jobDone, jobFailed, jobCancelled:
		return true
	}
	return false
}

// cancelRun cancels a queued or running job; a finished one stays as it is.
func (j *filesJob) cancelRun() {
	j.mu.Lock()
	switch j.dto.State {
	case jobQueued:
		j.updateLocked(true, func(d *FilesJobDTO) {
			d.State, d.Message, d.RetryInS = jobCancelled, "Отменено", 0
		})
	case jobRunning, jobWaiting:
		j.cancelled = true
		if j.cancel != nil {
			j.cancel()
		}
	}
	j.mu.Unlock()
}

func (j *filesJob) addPart(p string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.parts == nil {
		j.parts = map[string]bool{}
	}
	j.parts[p] = true
}

func (j *filesJob) dropPart(p string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.parts, p)
}

// removeParts deletes the job's part files: on the computer at once, on the server over the live
// client of the job's or the tab's connection, best effort and bounded. Without a live connection
// remote parts stay.
func (j *filesJob) removeParts() {
	j.mu.Lock()
	parts := make([]string, 0, len(j.parts))
	for p := range j.parts {
		parts = append(parts, p)
	}
	j.parts = nil
	j.mu.Unlock()
	if len(parts) == 0 {
		return
	}
	clear(j.acked)
	if j.kind == jobDownload {
		for _, p := range parts {
			_ = os.Remove(p) //nolint:gosec // G104: best-effort cleanup of a part file
		}
		return
	}
	a := j.tab.a
	var cl *ssh.Client
	for _, u := range []any{j, j.tab} {
		if c := a.connOf(u); c != nil {
			if live, _, err := c.clientFor(u); err == nil && live != nil {
				cl = live
				break
			}
		}
	}
	sshclient.RemoveRemoteFiles(cl, parts)
}

// copyOpts is the copy description of target for this job.
func (j *filesJob) copyOpts(target string) sshclient.Copy {
	return sshclient.Copy{
		JobID:   j.id,
		Replace: j.replace,
		Acked:   j.acked,
		OnPart:  j.addPart,
		Progress: func(pos int64) {
			if h := j.tab.a.filesProgressHook; h != nil {
				h(j.id, target, pos)
			}
			j.outage = time.Time{}
			base := j.fileBase
			j.update(false, func(d *FilesJobDTO) { d.DoneBytes = base + pos })
		},
	}
}

// run is one run of the job on the tab's runner goroutine.
func (j *filesJob) run() {
	t := j.tab
	a := t.a
	j.mu.Lock()
	ctx := j.ctx
	j.mu.Unlock()
	j.fileBase, j.failures, j.changed, j.lost, j.attempt, j.outage = 0, nil, nil, false, 0, time.Time{}
	if j.acked == nil {
		j.acked = map[string]int64{}
	}
	if j.finished == nil {
		j.finished = map[string]bool{}
	}

	var err error
	if _, err = a.joinHostConn(t.hostID, j); err == nil {
		stop := context.AfterFunc(ctx, j.closeSFTP) // unblocks an sftp call on a stalled connection
		switch j.kind {
		case jobUpload:
			err = j.upload(ctx)
		case jobDownload:
			err = j.download(ctx)
		case jobDelete:
			err = j.remove(ctx)
		case jobCopy:
			err = j.copyRemote(ctx)
		}
		stop()
	}
	j.closeSFTP()
	j.finish(ctx, err)
	a.mcpLeave(j)
}

func (j *filesJob) closeSFTP() {
	j.mu.Lock()
	cl := j.scClose
	j.scClose = nil
	j.mu.Unlock()
	if cl != nil {
		cl()
	}
}

// finish publishes the end of a run. A cancelled job deletes its part files; a failed one keeps
// them for Retry.
func (j *filesJob) finish(ctx context.Context, err error) {
	j.mu.Lock()
	cancelled := j.cancelled || ctx.Err() != nil
	j.cancel, j.ctx = nil, nil
	j.mu.Unlock()
	if cancelled {
		j.removeParts()
	}
	detail := j.failureText()
	switch {
	case cancelled:
		j.update(true, func(d *FilesJobDTO) {
			d.State, d.Message, d.Detail, d.RetryInS, d.Current = jobCancelled, "Отменено", detail, 0, ""
		})
	case err != nil:
		msg := err.Error()
		full := sshclient.ErrorDetail(err)
		if detail != "" {
			full += "\n" + detail
		}
		j.update(true, func(d *FilesJobDTO) {
			d.State, d.Message, d.Detail, d.RetryInS, d.Current = jobFailed, msg, full, 0, ""
		})
	default:
		j.update(true, func(d *FilesJobDTO) {
			d.State, d.Message, d.Detail, d.RetryInS, d.Current = jobDone, doneMessage(*d), detail, 0, ""
		})
	}
	for _, dir := range j.changed {
		j.tab.a.events().Emit("files:changed:"+j.tab.id, FilesChangedDTO{Dir: dir})
	}
}

func doneMessage(d FilesJobDTO) string {
	msg := "Готово"
	if d.Failed > 0 {
		msg = fmt.Sprintf("Готово, с ошибками: %d", d.Failed)
	}
	if d.Skipped > 0 {
		msg += fmt.Sprintf(" · пропущено: %d", d.Skipped)
	}
	if d.Renamed > 0 {
		msg += fmt.Sprintf(" · переименовано: %d", d.Renamed)
	}
	return msg
}

func (j *filesJob) failureText() string {
	if len(j.failures) == 0 {
		return ""
	}
	lines := j.failures
	more := 0
	if len(lines) > jobDetailLines {
		more = len(lines) - jobDetailLines
		lines = lines[:jobDetailLines]
	}
	s := strings.Join(lines, "\n")
	if more > 0 {
		s += fmt.Sprintf("\n… и ещё %d", more)
	}
	return s
}

// fail records a failed entry; files count in failed.
func (j *filesJob) fail(err error, file bool) {
	j.failures = append(j.failures, err.Error())
	if file {
		j.update(true, func(d *FilesJobDTO) { d.Failed++ })
	}
}

// fileDone counts a finished file of size bytes.
func (j *filesJob) fileDone(size int64) {
	j.fileBase += size
	base := j.fileBase
	j.update(false, func(d *FilesJobDTO) {
		d.DoneFiles++
		d.DoneBytes = base
	})
}

func (j *filesJob) startFile(rel string) {
	j.mu.Lock()
	j.speedAt = time.Time{}
	j.mu.Unlock()
	j.update(true, func(d *FilesJobDTO) { d.Current = rel })
}

func (j *filesJob) setTotals(plan []sshclient.PlanItem) {
	var files int
	var bytes int64
	for _, it := range plan {
		if !it.Dir {
			files++
			bytes += it.Size
		}
	}
	j.update(true, func(d *FilesJobDTO) { d.TotalFiles, d.TotalBytes = files, bytes })
}

// connFatal reports a connect error that pausing will not cure.
func connFatal(err error) bool {
	if errors.Is(err, errConnLost) {
		return false
	}
	for _, e := range []error{sshclient.ErrAuthDenied, sshclient.ErrHostKeyChanged, sshclient.ErrHostNotTrusted,
		sshclient.ErrCancelled, sshclient.ErrNoSFTP, errVaultLocked, errHostNotFound, errFilesClosed} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// conn returns the job's sftp session, connecting first. After a drop it pauses by the backoff
// before each attempt and gives up filesRetryTime after the drop with no progress since.
func (j *filesJob) conn(ctx context.Context) (*sftp.Client, error) {
	if j.sc != nil {
		return j.sc, nil
	}
	a := j.tab.a
	backoff, limit := a.filesBackoff, a.filesRetryFor
	if backoff == nil {
		backoff = filesDefaultBackoff
	}
	if limit <= 0 {
		limit = filesRetryTime
	}
	for {
		if j.lost {
			j.attempt++
			d := backoff(j.attempt)
			if time.Since(j.outage)+d > limit {
				return nil, fmt.Errorf("%w: %w", errFilesLinkGone, j.lastErr)
			}
			if err := j.pause(ctx, d); err != nil {
				return nil, err
			}
		}
		cl, err := j.tab.acquire(ctx, j)
		if err == nil {
			var sc *sftp.Client
			var closeSC func()
			sc, closeSC, err = sshclient.OpenSFTP(ctx, cl)
			if err != nil && clientEndedWithin(cl, deadClientGrace) {
				err = fmt.Errorf("%w: %w", errConnLost, err)
			}
			if err == nil {
				j.mu.Lock()
				j.scClose = closeSC
				j.mu.Unlock()
				j.sc, j.cl, j.lost, j.attempt = sc, cl, false, 0
				if ctx.Err() != nil { // the AfterFunc may have run before scClose was set
					j.closeSFTP()
				}
				j.update(true, func(d *FilesJobDTO) { d.State, d.Message, d.RetryInS = jobRunning, "", 0 })
				return sc, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if connFatal(err) {
			return nil, err
		}
		j.lostErr(err)
	}
}

// pause waits d in the waiting state, counting down the seconds; FilesJobRetry ends it at once.
func (j *filesJob) pause(ctx context.Context, d time.Duration) error {
	select { // a request from before this pause is for an attempt that has already run
	case <-j.now:
	default:
	}
	end := time.Now().Add(d)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		left := int(math.Ceil(time.Until(end).Seconds()))
		j.update(true, func(dto *FilesJobDTO) {
			dto.State, dto.RetryInS = jobWaiting, max(left, 0)
			dto.Message = fmt.Sprintf("Связь прервана, повтор через %d с", max(left, 0))
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			continue
		case <-timer.C:
		case <-j.now:
		}
		j.update(true, func(dto *FilesJobDTO) {
			dto.RetryInS, dto.Message = 0, "Переподключение…"
		})
		return nil
	}
}

// lostConn reports an error after which the session must be opened anew: the sftp session or the
// connection under it ended.
func (j *filesJob) lostConn(err error) bool {
	if sshclient.IsLost(err) {
		return true
	}
	return j.cl != nil && clientEndedWithin(j.cl, deadClientGrace)
}

// clientEndedWithin reports whether c's connection ends within grace.
func clientEndedWithin(c *ssh.Client, grace time.Duration) bool {
	ended := make(chan struct{})
	go func() { // a live client keeps this until it closes
		_ = c.Wait() //nolint:gosec // G104: only the end of the connection matters
		close(ended)
	}()
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-ended:
		return true
	case <-t.C:
		return false
	}
}

// withConn runs op on the job's session, reconnecting and running op again after a drop. It
// returns op's own error as itemErr and a connect failure (or the end of ctx) as fatal.
func (j *filesJob) withConn(ctx context.Context, op func(sc *sftp.Client) error) (itemErr, fatal error) {
	for {
		sc, err := j.conn(ctx)
		if err != nil {
			return nil, err
		}
		err = op(sc)
		if err == nil {
			return nil, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		if !j.lostConn(err) {
			return err, nil
		}
		j.closeSFTP()
		j.sc, j.cl = nil, nil
		j.lostErr(err)
	}
}

func (j *filesJob) lostErr(err error) {
	j.lastErr, j.lost = err, true
	if j.outage.IsZero() {
		j.outage = time.Now()
	}
}

// under reports whether rel lies in one of the folders dirs.
func under(rel string, dirs []string) bool {
	return slices.ContainsFunc(dirs, func(d string) bool { return strings.HasPrefix(rel, d+"/") })
}

func (j *filesJob) upload(ctx context.Context) error {
	plan, err := sshclient.PlanLocal(ctx, j.sources, func(p string, err error) { j.fail(err, true) })
	if err != nil {
		return err
	}
	j.setTotals(plan)
	j.changed = []string{j.dest}
	var failedDirs []string
	for _, it := range plan {
		target := path.Join(j.dest, it.Rel)
		if under(it.Rel, failedDirs) {
			if it.Dir {
				failedDirs = append(failedDirs, it.Rel)
			} else {
				j.fail(fmt.Errorf("%w: %s", errFilesParentFail, it.Src), true)
			}
			continue
		}
		if it.Dir {
			ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error { return sshclient.EnsureRemoteDir(sc, target) })
			if ferr != nil {
				return ferr
			}
			if ierr != nil {
				j.fail(ierr, false)
				failedDirs = append(failedDirs, it.Rel)
			}
			continue
		}
		if j.finished[target] {
			j.fileDone(it.Size)
			continue
		}
		j.startFile(it.Rel)
		part := sshclient.PartName(target, j.id)
		skipped := false
		ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
			res, err := sshclient.UploadFile(ctx, sc, it.Src, target, j.copyOpts(target))
			skipped = res == sshclient.Skipped
			return err
		})
		if ferr != nil {
			return ferr
		}
		if ierr != nil {
			j.fail(ierr, true)
			j.removeRemotePart(part)
			j.fileBase += it.Size
			continue
		}
		j.dropPart(part)
		j.finished[target] = true
		if skipped {
			j.update(false, func(d *FilesJobDTO) { d.Skipped++ })
		}
		j.fileDone(it.Size)
	}
	return nil
}

// removeRemotePart deletes the part of a file that failed on its own, over the job's session.
func (j *filesJob) removeRemotePart(part string) {
	if j.sc != nil {
		_ = j.sc.Remove(part) //nolint:gosec // G104: best-effort cleanup; a missing part is fine
	}
	j.dropPart(part)
	delete(j.acked, part)
}

func (j *filesJob) download(ctx context.Context) error {
	var plan []sshclient.PlanItem
	var planFails []error
	ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
		planFails = nil
		var err error
		plan, err = sshclient.PlanRemote(ctx, sc, j.sources, func(_ string, err error) { planFails = append(planFails, err) })
		return err
	})
	if ferr != nil {
		return ferr
	}
	if ierr != nil {
		return ierr
	}
	for _, e := range planFails {
		j.fail(e, true)
	}
	j.setTotals(plan)
	var failedDirs []string
	for _, it := range plan {
		if under(it.Rel, failedDirs) {
			if it.Dir {
				failedDirs = append(failedDirs, it.Rel)
			} else {
				j.fail(fmt.Errorf("%w: %s", errFilesParentFail, it.Src), true)
			}
			continue
		}
		target, _, err := sshclient.LocalTarget(j.dest, it.Rel)
		if err != nil {
			j.fail(err, !it.Dir)
			if it.Dir {
				failedDirs = append(failedDirs, it.Rel)
			}
			continue
		}
		if _, changed := sshclient.SafeLocalName(path.Base(it.Rel)); changed {
			j.update(false, func(d *FilesJobDTO) { d.Renamed++ })
		}
		if it.Dir {
			if err := sshclient.EnsureLocalDir(target); err != nil {
				j.fail(err, false)
				failedDirs = append(failedDirs, it.Rel)
			}
			continue
		}
		if j.finished[target] {
			j.fileDone(it.Size)
			continue
		}
		j.startFile(it.Rel)
		part := sshclient.PartName(target, j.id)
		skipped := false
		ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
			res, err := sshclient.DownloadFile(ctx, sc, it.Src, target, j.copyOpts(target))
			skipped = res == sshclient.Skipped
			return err
		})
		if ferr != nil {
			return ferr
		}
		if ierr != nil {
			j.fail(ierr, true)
			_ = os.Remove(part) //nolint:gosec // G104: best-effort cleanup; a missing part is fine
			j.dropPart(part)
			j.fileBase += it.Size
			continue
		}
		j.dropPart(part)
		j.finished[target] = true
		if skipped {
			j.update(false, func(d *FilesJobDTO) { d.Skipped++ })
		}
		j.fileDone(it.Size)
	}
	return nil
}

func (j *filesJob) remove(ctx context.Context) error {
	total := 0
	for _, p := range j.sources {
		n := 0
		ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
			n = sshclient.CountTree(ctx, sc, p)
			return nil
		})
		if ferr != nil {
			return ferr
		}
		if ierr != nil {
			return ierr
		}
		total += n
	}
	j.update(true, func(d *FilesJobDTO) { d.TotalFiles = total })
	seen := map[string]bool{}
	for _, p := range j.sources {
		if parent := path.Dir(p); !slices.Contains(j.changed, parent) {
			j.changed = append(j.changed, parent)
		}
		j.startFile(p)
		ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
			return sshclient.RemoveTree(ctx, sc, p, func(q string, err error) {
				if seen[q] {
					return // counted before the connection dropped
				}
				seen[q] = true
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					j.fail(err, false)
					j.update(true, func(d *FilesJobDTO) { d.Failed++ })
					return
				}
				j.update(false, func(d *FilesJobDTO) { d.DoneFiles = min(d.DoneFiles+1, d.TotalFiles) })
			})
		})
		if ferr != nil {
			return ferr
		}
		if ierr != nil {
			j.fail(ierr, false)
		}
	}
	return nil
}

// copyTarget is the top-level name src gets in j.dest: its own name, or a free «(копия)» name when
// it is copied into the folder it is in. The choice is kept for the job's later runs.
func (j *filesJob) copyTarget(sc *sftp.Client, src string, isDir bool) (string, error) {
	if name, ok := j.copyNames[src]; ok {
		return name, nil
	}
	name := path.Base(src)
	if path.Dir(src) == j.dest {
		var err error
		if name, err = sshclient.CopyName(sc, j.dest, name, isDir); err != nil {
			return "", err
		}
	}
	if j.copyNames == nil {
		j.copyNames = map[string]string{}
	}
	j.copyNames[src] = name
	return name, nil
}

// planCopy walks the sources of a copy into one plan whose Rel starts with each target name. A
// folder copied into itself is a failure of its own.
func (j *filesJob) planCopy(ctx context.Context, sc *sftp.Client) ([]sshclient.PlanItem, []error, error) {
	var plan []sshclient.PlanItem
	var fails []error
	for _, s := range j.sources {
		src := path.Clean(s)
		if j.dest == src || strings.HasPrefix(j.dest+"/", src+"/") {
			fails = append(fails, &sshclient.FSError{Msg: errFilesCopyItself.Error() + ": " + path.Base(src), Err: errFilesCopyItself})
			continue
		}
		items, err := sshclient.PlanRemote(ctx, sc, []string{src}, func(_ string, err error) { fails = append(fails, err) })
		if err != nil {
			return nil, nil, err
		}
		if len(items) == 0 {
			continue
		}
		name, err := j.copyTarget(sc, src, items[0].Dir)
		if err != nil {
			if sshclient.IsLost(err) {
				return nil, nil, err
			}
			fails = append(fails, err)
			continue
		}
		base := path.Base(src)
		for i := range items {
			items[i].Rel = name + strings.TrimPrefix(items[i].Rel, base)
		}
		plan = append(plan, items...)
	}
	return plan, fails, nil
}

// copyRemote copies remote files and folders into another remote folder through the job's sftp
// session: folders merge, files go through part files, replaced or skipped by j.replace.
func (j *filesJob) copyRemote(ctx context.Context) error {
	j.dest = path.Clean(j.dest)
	var plan []sshclient.PlanItem
	var planFails []error
	ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
		var err error
		plan, planFails, err = j.planCopy(ctx, sc)
		return err
	})
	if ferr != nil {
		return ferr
	}
	if ierr != nil {
		return ierr
	}
	for _, e := range planFails {
		j.fail(e, true)
	}
	j.setTotals(plan)
	j.changed = []string{j.dest}
	var failedDirs []string
	for _, it := range plan {
		target := path.Join(j.dest, it.Rel)
		if under(it.Rel, failedDirs) {
			if it.Dir {
				failedDirs = append(failedDirs, it.Rel)
			} else {
				j.fail(fmt.Errorf("%w: %s", errFilesParentFail, it.Src), true)
			}
			continue
		}
		if it.Dir {
			ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error { return sshclient.EnsureRemoteDir(sc, target) })
			if ferr != nil {
				return ferr
			}
			if ierr != nil {
				j.fail(ierr, false)
				failedDirs = append(failedDirs, it.Rel)
			}
			continue
		}
		if j.finished[target] {
			j.fileDone(it.Size)
			continue
		}
		j.startFile(it.Rel)
		part := sshclient.PartName(target, j.id)
		skipped := false
		ierr, ferr := j.withConn(ctx, func(sc *sftp.Client) error {
			res, err := sshclient.CopyRemoteFile(ctx, sc, it.Src, target, j.copyOpts(target))
			skipped = res == sshclient.Skipped
			return err
		})
		if ferr != nil {
			return ferr
		}
		if ierr != nil {
			j.fail(ierr, true)
			j.removeRemotePart(part)
			j.fileBase += it.Size
			continue
		}
		j.dropPart(part)
		j.finished[target] = true
		if skipped {
			j.update(false, func(d *FilesJobDTO) { d.Skipped++ })
		}
		j.fileDone(it.Size)
	}
	return nil
}

// runJobs runs the tab's queued jobs one at a time until the tab ends.
func (t *filesTab) runJobs() {
	defer close(t.runnerDone)
	for {
		j := t.nextJob()
		if j == nil {
			select {
			case <-t.kick:
				continue
			case <-t.ctx.Done():
				return
			}
		}
		j.run()
	}
}

// nextJob starts the first queued job, nil if none or the tab has ended.
func (t *filesTab) nextJob() *filesJob {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ctx.Err() != nil {
		return nil
	}
	for _, j := range t.jobs {
		j.mu.Lock()
		if j.dto.State == jobQueued {
			j.ctx, j.cancel = context.WithCancel(t.ctx)
			j.cancelled = false
			j.updateLocked(true, func(d *FilesJobDTO) { d.State, d.Message = jobRunning, "" })
			j.mu.Unlock()
			return j
		}
		j.mu.Unlock()
	}
	return nil
}

func (t *filesTab) wake() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// addJob queues a new job and returns its id.
func (t *filesTab) addJob(kind string, sources []string, dest string, replace bool) (string, error) {
	j := &filesJob{tab: t, id: newJobID(), kind: kind, sources: sources, dest: dest, replace: replace, now: make(chan struct{}, 1)}
	j.dto = FilesJobDTO{ID: j.id, Kind: kind, Title: jobTitle(kind, sources), State: jobQueued, Dest: jobDest(kind, sources, dest)}
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		return "", errFilesClosed
	}
	t.jobs = append(t.jobs, j)
	t.mu.Unlock()
	j.update(true, func(*FilesJobDTO) {})
	t.wake()
	return j.id, nil
}

func (t *filesTab) job(id string) (*filesJob, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, j := range t.jobs {
		if j.id == id {
			return j, nil
		}
	}
	return nil, errFilesJobNotFound
}

// retryJob reconnects a waiting job at once, or queues a failed, cancelled or partly failed job again; it resumes its kept parts and does
// not copy again what it has finished.
func (t *filesTab) retryJob(id string) error {
	j, err := t.job(id)
	if err != nil {
		return err
	}
	j.mu.Lock()
	d := j.dto
	if d.State == jobWaiting { // «Повторить сейчас»: the pause ends and the job reconnects at once
		j.mu.Unlock()
		select {
		case j.now <- struct{}{}:
		default:
		}
		return nil
	}
	if d.State != jobFailed && d.State != jobCancelled && (d.State != jobDone || d.Failed == 0) {
		j.mu.Unlock()
		return errFilesJobActive
	}
	j.updateLocked(true, func(d *FilesJobDTO) {
		*d = FilesJobDTO{ID: d.ID, Kind: d.Kind, Title: d.Title, Dest: d.Dest, State: jobQueued}
	})
	j.mu.Unlock()
	t.wake()
	return nil
}

// removeJob forgets a finished job and deletes the part files it kept.
func (t *filesTab) removeJob(id string) error {
	j, err := t.job(id)
	if err != nil {
		return err
	}
	if !j.finishedState() {
		return errFilesJobActive
	}
	t.mu.Lock()
	t.jobs = slices.DeleteFunc(t.jobs, func(x *filesJob) bool { return x == j })
	t.mu.Unlock()
	go j.removeParts()
	return nil
}

func (t *filesTab) jobList() []FilesJobDTO {
	t.mu.Lock()
	jobs := append([]*filesJob(nil), t.jobs...)
	t.mu.Unlock()
	out := make([]FilesJobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.snapshot())
	}
	return out
}

// newJobID is a short id: it goes into the names of the job's part files.
func newJobID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func jobTitle(kind string, sources []string) string {
	verb := map[string]string{jobUpload: "Загрузка", jobDownload: "Скачивание", jobDelete: "Удаление", jobCopy: "Копирование"}[kind]
	if len(sources) == 1 {
		name := path.Base(sources[0])
		if kind == jobUpload {
			name = filepath.Base(sources[0])
		}
		return fmt.Sprintf("%s «%s»", verb, name)
	}
	return fmt.Sprintf("%s: %d %s", verb, len(sources), objectsWord(len(sources)))
}

// objectsWord is «объект» in the form that goes with n.
func objectsWord(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 14:
		return "объектов"
	case n%10 == 1:
		return "объект"
	case n%10 >= 2 && n%10 <= 4:
		return "объекта"
	}
	return "объектов"
}

// jobDest is where «Показать в папке» points: the downloaded item itself when there is one.
func jobDest(kind string, sources []string, dest string) string {
	switch kind {
	case jobDownload:
		if len(sources) == 1 {
			if p, _, err := sshclient.LocalTarget(dest, path.Base(sources[0])); err == nil {
				return p
			}
		}
		return dest
	case jobUpload:
		return dest
	}
	return ""
}
