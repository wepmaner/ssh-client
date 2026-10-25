package appdir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Builds before the Stash release kept the data in %LOCALAPPDATA%\sshclient. Stash
// expects %APPDATA%\sshclient (its manifest lists that folder as the app data), so the
// first start of a newer build moves the old folder once.

const (
	// MarkerFile is left in the old folder after a copy: it names the new folder.
	MarkerFile = "migrated-to.txt"
	// LogFile records what the move did; the app has no console in release builds.
	LogFile = "migrate.log"
	// cacheDir is the WebView2 cache: a locked file there is skipped, not fatal.
	cacheDir = "WebView2"
)

// Result is what Migrate did.
type Result int

const (
	// NothingToMove: no old folder, an empty one, or one already copied.
	NothingToMove Result = iota
	// Moved: the old folder was renamed to the new place.
	Moved
	// Copied: the rename failed, the content was copied and the old folder kept with a marker.
	Copied
	// NewHasData: both folders hold data; nothing is touched.
	NewHasData
	// Failed: neither rename nor copy worked; this run uses the old folder.
	Failed
)

func (r Result) String() string {
	switch r {
	case NothingToMove:
		return "nothing to move"
	case Moved:
		return "moved"
	case Copied:
		return "copied"
	case NewHasData:
		return "new folder has data"
	case Failed:
		return "failed"
	}
	return fmt.Sprintf("Result(%d)", int(r))
}

// Migrate moves %LOCALAPPDATA%\sshclient to %APPDATA%\sshclient when the old one holds
// data and the new one does not. Call it once at startup, before the first Dir.
// The old folder is never deleted before the new one is complete: a rename is atomic,
// and a copy goes to a staging folder that is renamed into place at the end.
func Migrate() (Result, error) {
	fallback.Store(nil)
	to, err := newDir()
	if err != nil {
		return Failed, err
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return NothingToMove, nil //nolint:nilerr // no LOCALAPPDATA means no old folder
	}
	from := filepath.Join(base, name)
	res, err := migrate(from, to)
	if res == Failed {
		fallback.Store(&from)
	}
	return res, err
}

func migrate(from, to string) (Result, error) {
	if strings.EqualFold(filepath.Clean(from), filepath.Clean(to)) {
		return NothingToMove, nil
	}
	old, err := os.ReadDir(from)
	if errors.Is(err, fs.ErrNotExist) {
		return NothingToMove, nil
	}
	if err != nil {
		return failed(from, fmt.Errorf("read %s: %w", from, err))
	}
	marked := false
	data := 0
	for _, e := range old {
		switch e.Name() {
		case MarkerFile:
			marked = true
		case LogFile:
		default:
			data++
		}
	}
	if data == 0 {
		return NothingToMove, nil
	}

	cur, err := os.ReadDir(to)
	switch {
	case err == nil && len(cur) > 0:
		if marked {
			return NothingToMove, nil // copied on an earlier start
		}
		note(to, fmt.Sprintf("both %s and %s hold data: the old folder is left as it is", from, to), true)
		return NewHasData, nil
	case err == nil:
		// An empty folder from an earlier Dir call: a folder cannot be renamed over it.
		if err := os.Remove(to); err != nil {
			return failed(from, fmt.Errorf("remove empty %s: %w", to, err))
		}
	case !errors.Is(err, fs.ErrNotExist):
		return failed(from, fmt.Errorf("read %s: %w", to, err))
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return failed(from, err)
	}

	renameErr := os.Rename(from, to)
	if renameErr == nil {
		note(to, fmt.Sprintf("moved %s to %s", from, to), false)
		return Moved, nil
	}

	// A file held open (a running older copy, a scanner) blocks the rename: copy instead.
	staging := to + ".migrating"
	if err := os.RemoveAll(staging); err != nil {
		return failed(from, fmt.Errorf("rename: %v; clear %s: %w", renameErr, staging, err))
	}
	skipped, err := copyTree(from, staging)
	if err != nil {
		discard(staging)
		return failed(from, fmt.Errorf("rename: %v; copy: %w", renameErr, err))
	}
	if err := os.Rename(staging, to); err != nil {
		discard(staging)
		return failed(from, fmt.Errorf("rename: %v; place copy: %w", renameErr, err))
	}
	marker := fmt.Sprintf("Данные sshclient перенесены в %s (%s).\r\nЭта папка больше не используется, её можно удалить.\r\n",
		to, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(from, MarkerFile), []byte(marker), 0o600); err != nil {
		log.Printf("appdir: write %s: %v", MarkerFile, err)
	}
	msg := fmt.Sprintf("copied %s to %s (rename failed: %v)", from, to, renameErr)
	if skipped > 0 {
		msg += fmt.Sprintf("; %d locked WebView2 cache files skipped", skipped)
	}
	note(to, msg, false)
	return Copied, nil
}

// failed logs err into the old folder, which this run keeps using.
func failed(from string, err error) (Result, error) {
	err = fmt.Errorf("appdir: move data folder: %w", err)
	note(from, err.Error(), false)
	return Failed, err
}

// note logs msg and appends it to LogFile in dir; once skips a message already there.
func note(dir, msg string, once bool) {
	log.Printf("appdir: %s", msg)
	path := filepath.Join(dir, LogFile)
	if once {
		if prev, err := os.ReadFile(path); err == nil && strings.Contains(string(prev), msg) { //nolint:gosec // G304: fixed name in the data folder
			return
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: fixed name in the data folder
	if err != nil {
		log.Printf("appdir: write %s: %v", LogFile, err)
		return
	}
	_, werr := fmt.Fprintf(f, "%s %s\r\n", time.Now().Format(time.RFC3339), msg)
	if err := errors.Join(werr, f.Close()); err != nil {
		log.Printf("appdir: write %s: %v", LogFile, err)
	}
}

// discard removes a half-made staging copy; the old folder is untouched either way.
func discard(staging string) {
	if err := os.RemoveAll(staging); err != nil {
		log.Printf("appdir: remove %s: %v", staging, err)
	}
}

// copyTree copies the folder from into to (which must not exist). Files under the
// WebView2 cache that cannot be read are skipped and counted; any other failure stops.
func copyTree(from, to string) (skipped int, err error) {
	err = filepath.WalkDir(from, func(path string, d fs.DirEntry, walkErr error) error {
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		cache := rel == cacheDir || strings.HasPrefix(rel, cacheDir+string(filepath.Separator))
		if walkErr != nil {
			if cache && d != nil && d.IsDir() {
				skipped++
				return fs.SkipDir
			}
			if cache {
				skipped++
				return nil
			}
			return walkErr
		}
		dst := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, 0o700)
		case !d.Type().IsRegular():
			return nil // no links or devices in the data folder
		}
		if err := copyFile(path, dst); err != nil {
			if cache {
				skipped++
				return nil
			}
			return err
		}
		return nil
	})
	return skipped, err
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: a file inside the old data folder
	if err != nil {
		return err
	}
	// A read-only handle: its close error changes nothing in the copy.
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: a file inside the staging folder
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}
