package app

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/model"
	"sshclient/internal/sshclient"
)

//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errFilesBadConflict = errors.New("Неизвестный выбор при совпадении имён")
	errFilesBadDir      = errors.New("Папка на компьютере не найдена")
	errFilesNothing     = errors.New("Ничего не выбрано")
	errFilesIntoItself  = errors.New("Нельзя переместить папку в неё саму")
)

// FilesStateDTO is the payload of files:state:<tab>.
type FilesStateDTO struct {
	State     string `json:"state"` // connecting|connected|error|closed
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	Retryable bool   `json:"retryable"`
}

// FilesListDTO is one remote folder.
type FilesListDTO struct {
	Path    string          `json:"path"`
	Entries []FilesEntryDTO `json:"entries"`
}

// FilesEntryDTO is one entry of a remote folder.
type FilesEntryDTO struct {
	Name   string `json:"name"`
	Dir    bool   `json:"dir"`
	Link   bool   `json:"link"`
	Broken bool   `json:"broken"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"` // unix ms
	Mode   string `json:"mode"`
}

// FilesJobDTO is one job of a tab's queue, the payload of files:job:<tab>.
type FilesJobDTO struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // upload|download|delete
	Title      string `json:"title"`
	State      string `json:"state"` // queued|running|waiting|done|failed|cancelled
	Message    string `json:"message"`
	Detail     string `json:"detail"`
	TotalBytes int64  `json:"total_bytes"`
	DoneBytes  int64  `json:"done_bytes"`
	TotalFiles int    `json:"total_files"`
	DoneFiles  int    `json:"done_files"`
	Failed     int    `json:"failed"`
	Renamed    int    `json:"renamed"`
	Current    string `json:"current"`
	SpeedBps   int64  `json:"speed_bps"`
	Dest       string `json:"dest"`
	RetryInS   int    `json:"retry_in_s"`
}

// FilesDropDTO is the payload of files:drop: files dropped from Explorer on a files tab.
type FilesDropDTO struct {
	Tab   string   `json:"tab"`
	Dir   string   `json:"dir"`
	Paths []string `json:"paths"`
}

// FilesChangedDTO is the payload of files:changed:<tab>: a job changed the remote folder Dir.
type FilesChangedDTO struct {
	Dir string `json:"dir"`
}

// FilesOpen opens a files tab on the host's shared connection and returns its id at once; the login
// starts right away. Its questions arrive as files:login:<id>, held until FilesLoginAttach, and its
// state as files:state:<id>.
func (a *App) FilesOpen(hostID string) (string, error) {
	if a.restoring.Load() {
		return "", errRestoring
	}
	if a.exiting.Load() {
		return "", errFilesClosed
	}
	if !a.storeUnlocked() {
		return "", errVaultLocked
	}
	all := a.storeRef().Hosts()
	i := slices.IndexFunc(all, func(h model.Host) bool { return h.ID == hostID })
	if i < 0 {
		return "", errHostNotFound
	}
	h := all[i]
	ctx, cancel := context.WithCancel(context.Background())
	t := &filesTab{
		a: a, id: uuid.NewString(), hostID: h.ID, hostName: h.Name,
		ctx: ctx, cancel: cancel,
		kick: make(chan struct{}, 1), runnerDone: make(chan struct{}), done: make(chan struct{}),
		state: FilesStateDTO{State: stateConnecting, Retryable: true},
	}
	t.out = newOutStreamFor(a.events(), "files:login:"+t.id)
	t.out.latestEvent = t.stateEvent()
	t.out.onWrite = t.onLoginOutput
	if _, err := a.joinHostConn(h.ID, t); err != nil {
		cancel()
		return "", err
	}
	a.filesMu.Lock()
	if a.files == nil {
		a.files = map[string]*filesTab{}
	}
	a.files[t.id] = t
	a.filesMu.Unlock()
	go t.runJobs()
	go t.connect()
	return t.id, nil
}

// FilesLoginAttach tells Go the tab's mini-terminal listens to files:login:<id>: the output held
// since FilesOpen is released.
func (a *App) FilesLoginAttach(tabID string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	t.out.Attach()
	return nil
}

// FilesLoginWrite sends base64-encoded keystrokes to the question of the login running for the tab.
// Input with no login waiting is dropped.
func (a *App) FilesLoginWrite(tabID, dataB64 string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	b, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return errBadInput
	}
	defer clear(b) // may be a passphrase or a password; the input queue keeps its own copy
	t.mu.Lock()
	in := t.in
	t.mu.Unlock()
	if in != nil {
		in.Write(b)
	}
	return nil
}

// FilesRetry starts a new login of a tab whose login failed.
func (a *App) FilesRetry(tabID string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	t.mu.Lock()
	st, ended := t.state, t.ended || t.closing
	t.mu.Unlock()
	if ended || !st.Retryable {
		return errFilesClosed
	}
	if st.State != stateError {
		return nil
	}
	go t.connect()
	return nil
}

// FilesClose cancels the tab's transfers, deletes their part files and leaves the connection. It
// does not wait for the cleanup.
func (a *App) FilesClose(tabID string) error {
	a.filesMu.Lock()
	t := a.files[tabID]
	delete(a.files, tabID)
	a.filesMu.Unlock()
	if t == nil {
		return errFilesTabNotFound
	}
	go t.end(FilesStateDTO{State: stateClosed})
	return nil
}

// FilesHome returns the remote home folder.
func (a *App) FilesHome(tabID string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	var home string
	err = t.withSFTP(func(sc *sftp.Client) error {
		var err error
		home, err = sc.RealPath(".")
		return err
	})
	if err != nil {
		return "", sshclient.FileError(".", err)
	}
	return home, nil
}

// remoteDir is dir as an absolute clean path; "" and relative paths go through RealPath.
func remoteDir(sc *sftp.Client, dir string) (string, error) {
	if strings.HasPrefix(dir, "/") {
		return path.Clean(dir), nil
	}
	if dir == "" {
		dir = "."
	}
	return sc.RealPath(dir)
}

// FilesList reads the remote folder dir ("" is the home folder).
func (a *App) FilesList(tabID, dir string) (FilesListDTO, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return FilesListDTO{}, err
	}
	var out FilesListDTO
	err = t.withSFTP(func(sc *sftp.Client) error {
		p, err := remoteDir(sc, dir)
		if err != nil {
			return err
		}
		ents, err := sshclient.List(sc, p)
		if err != nil {
			return err
		}
		out = FilesListDTO{Path: p, Entries: make([]FilesEntryDTO, 0, len(ents))}
		for _, e := range ents {
			out.Entries = append(out.Entries, FilesEntryDTO{
				Name: e.Name, Dir: e.Dir, Link: e.Link, Broken: e.Broken, Size: e.Size,
				Mtime: e.ModTime.UnixMilli(), Mode: e.Mode,
			})
		}
		return nil
	})
	if err != nil {
		return FilesListDTO{}, sshclient.FileError(dir, err)
	}
	return out, nil
}

// FilesMkdir creates the folder name in dir and returns its path; a taken name is an error.
func (a *App) FilesMkdir(tabID, dir, name string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	var p string
	err = t.withSFTP(func(sc *sftp.Client) error {
		var err error
		p, err = sshclient.Mkdir(sc, dir, name)
		return err
	})
	if err != nil {
		return "", sshclient.FileError(name, err)
	}
	return p, nil
}

// FilesRename renames from to newName in the same folder, never over an existing entry, and
// returns the new path.
func (a *App) FilesRename(tabID, from, newName string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	if err := sshclient.CheckName(newName); err != nil {
		return "", err
	}
	to := path.Join(path.Dir(from), newName)
	err = t.withSFTP(func(sc *sftp.Client) error { return sshclient.RenameNoReplace(sc, from, to) })
	if err != nil {
		return "", sshclient.FileError(path.Base(from), err)
	}
	return to, nil
}

// FilesMove moves the entries into destDir, never over an existing one. The others are moved
// when one fails; the error lists the failures.
func (a *App) FilesMove(tabID string, paths []string, destDir string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	var fails []string
	for _, p := range paths {
		dest := path.Join(destDir, path.Base(p))
		if path.Clean(destDir) == path.Clean(p) || strings.HasPrefix(path.Clean(destDir)+"/", path.Clean(p)+"/") {
			fails = append(fails, errFilesIntoItself.Error()+": "+path.Base(p))
			continue
		}
		if path.Dir(path.Clean(p)) == path.Clean(destDir) {
			continue // already there
		}
		if err := t.withSFTP(func(sc *sftp.Client) error { return sshclient.RenameNoReplace(sc, p, dest) }); err != nil {
			fails = append(fails, sshclient.FileError(path.Base(p), err).Error())
		}
	}
	if len(fails) > 0 {
		return errors.New(strings.Join(fails, "\n"))
	}
	return nil
}

// FilesDelete queues the deletion of the entries, folders with all their contents, and returns the
// job id.
func (a *App) FilesDelete(tabID string, paths []string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errFilesNothing
	}
	return t.addJob(jobDelete, slices.Clone(paths), "", false)
}

// FilesConflicts returns the names of sources ("upload": local paths, "download": remote paths)
// already present in dest, by their top-level names as shown to the user.
func (a *App) FilesConflicts(tabID, direction string, sources []string, dest string) ([]string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	switch direction {
	case jobUpload:
		err = t.withSFTP(func(sc *sftp.Client) error {
			out = out[:0]
			for _, s := range sources {
				name := filepath.Base(s)
				if _, err := sc.Lstat(path.Join(dest, name)); err == nil {
					out = append(out, name)
				} else if sshclient.IsLost(err) {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, sshclient.FileError(dest, err)
		}
	case jobDownload:
		for _, s := range sources {
			target, _, err := sshclient.LocalTarget(dest, path.Base(s))
			if err != nil {
				continue
			}
			if _, err := os.Lstat(target); err == nil {
				out = append(out, path.Base(s))
			}
		}
	default:
		return nil, errBadInput
	}
	return out, nil
}

func replaceChoice(onConflict string) (bool, error) {
	switch onConflict {
	case "replace":
		return true, nil
	case "skip", "":
		return false, nil
	}
	return false, errFilesBadConflict
}

// FilesUpload queues the upload of local files and folders into remoteDir and returns the job id.
// onConflict is "replace" or "skip" for names already there; folders merge.
func (a *App) FilesUpload(tabID string, local []string, remoteDir, onConflict string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	replace, err := replaceChoice(onConflict)
	if err != nil {
		return "", err
	}
	if len(local) == 0 {
		return "", errFilesNothing
	}
	for _, p := range local {
		if !filepath.IsAbs(p) {
			return "", errBadInput
		}
	}
	return t.addJob(jobUpload, slices.Clone(local), remoteDir, replace)
}

// FilesDownload queues the download of remote files and folders into the local folder localDir and
// returns the job id.
func (a *App) FilesDownload(tabID string, remote []string, localDir, onConflict string) (string, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return "", err
	}
	replace, err := replaceChoice(onConflict)
	if err != nil {
		return "", err
	}
	if len(remote) == 0 {
		return "", errFilesNothing
	}
	if st, err := os.Stat(localDir); err != nil || !st.IsDir() || !filepath.IsAbs(localDir) {
		return "", errFilesBadDir
	}
	return t.addJob(jobDownload, slices.Clone(remote), filepath.Clean(localDir), replace)
}

// FilesJobCancel cancels a queued or running job; its part files are deleted.
func (a *App) FilesJobCancel(tabID, jobID string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	j, err := t.job(jobID)
	if err != nil {
		return err
	}
	j.cancelRun()
	return nil
}

// FilesJobRetry queues a failed or cancelled job again; it resumes from its part files.
func (a *App) FilesJobRetry(tabID, jobID string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	return t.retryJob(jobID)
}

// FilesJobRemove forgets a finished job.
func (a *App) FilesJobRemove(tabID, jobID string) error {
	t, err := a.filesTab(tabID)
	if err != nil {
		return err
	}
	return t.removeJob(jobID)
}

// FilesJobs lists the tab's jobs in queue order.
func (a *App) FilesJobs(tabID string) ([]FilesJobDTO, error) {
	t, err := a.filesTab(tabID)
	if err != nil {
		return nil, err
	}
	return t.jobList(), nil
}

// FilesPickUpload asks for files (several) or one folder to upload in the system dialog; [] when
// cancelled.
func (a *App) FilesPickUpload(folder bool) ([]string, error) {
	if a.wapp == nil {
		return nil, errNoWindow
	}
	d := a.wapp.Dialog.OpenFile().AttachToWindow(a.mainWin)
	if folder {
		p, err := dialogPath(d.CanChooseDirectories(true).CanChooseFiles(false).
			SetTitle("Загрузить папку").PromptForSingleSelection())
		if errors.Is(err, ErrCancelled) {
			return []string{}, nil
		}
		if err != nil {
			return nil, err
		}
		return []string{p}, nil
	}
	ps, err := d.CanChooseFiles(true).SetTitle("Загрузить файлы").PromptForMultipleSelection()
	if err != nil {
		if err.Error() == dialogCancelledText {
			return []string{}, nil
		}
		return nil, err
	}
	if ps == nil {
		ps = []string{}
	}
	return ps, nil
}

// FilesPickDownloadDir asks for the folder to download into; "" when cancelled. It starts in the
// folder chosen last in this run, else in Downloads, and remembers the choice.
func (a *App) FilesPickDownloadDir() (string, error) {
	if a.wapp == nil {
		return "", errNoWindow
	}
	a.filesMu.Lock()
	start := a.filesDir
	a.filesMu.Unlock()
	if start == "" {
		start = downloadsDir()
	}
	d := a.wapp.Dialog.OpenFile().AttachToWindow(a.mainWin).
		CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).
		SetTitle("Скачать в папку")
	if start != "" {
		d = d.SetDirectory(start)
	}
	p, err := dialogPath(d.PromptForSingleSelection())
	if errors.Is(err, ErrCancelled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	a.filesMu.Lock()
	a.filesDir = p
	a.filesMu.Unlock()
	return p, nil
}

// downloadsDir is %USERPROFILE%\Downloads when it exists.
func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, "Downloads")
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return home
}

// FilesShowLocal opens Explorer with the file or folder selected.
func (a *App) FilesShowLocal(p string) error {
	if a.wapp == nil {
		return errNoWindow
	}
	return a.wapp.Env.OpenFileManager(p, true)
}

// filesDropped turns a drop of files from Explorer on a files tab into files:drop. Drops outside a
// files tab's target carry no tab and are ignored.
func (a *App) filesDropped(e *application.WindowEvent) {
	ctx := e.Context()
	var attrs map[string]string
	if det := ctx.DropTargetDetails(); det != nil {
		attrs = det.Attributes
	}
	a.filesDrop(ctx.DroppedFiles(), attrs)
}

// filesDrop emits files:drop for files dropped on an element with the attributes attrs.
func (a *App) filesDrop(files []string, attrs map[string]string) {
	tab := attrs["data-files-tab"]
	if len(files) == 0 || tab == "" {
		return
	}
	a.events().Emit("files:drop", FilesDropDTO{Tab: tab, Dir: attrs["data-files-dir"], Paths: files})
}
