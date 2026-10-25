package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"sshclient/internal/appdir"
	"sshclient/internal/importers"
	"sshclient/internal/model"
	"sshclient/internal/store"
)

// User-facing messages of the import and export API.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errNoSSHConfig = errors.New("Файл ~/.ssh/config не найден")
	errReadConfig  = errors.New("Не удалось прочитать ~/.ssh/config")
	errReadPuTTY   = errors.New("Не удалось прочитать сессии PuTTY")
)

// ImportResult says what an import did. Warnings are shown as a list: keys to import
// separately, skipped duplicates, sessions and lines that could not be taken.
type ImportResult struct {
	Added    int      `json:"added"`
	Skipped  int      `json:"skipped"`
	Warnings []string `json:"warnings"`
}

// ImportSSHConfig adds the hosts of %USERPROFILE%\.ssh\config.
func (a *App) ImportSSHConfig() (ImportResult, error) {
	if !a.storeUnlocked() {
		return ImportResult{}, errVaultLocked
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ImportResult{}, errNoSSHConfig
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config")) //nolint:gosec // G304: the user's own ssh config
	if errors.Is(err, fs.ErrNotExist) {
		return ImportResult{}, errNoSSHConfig
	}
	if err != nil {
		return ImportResult{}, errReadConfig
	}
	defer func() { _ = f.Close() }() //nolint:gosec // G104: read-only file
	hosts, warns, err := importers.ParseSSHConfig(f)
	if err != nil {
		return ImportResult{}, errReadConfig
	}
	return a.importHosts(hosts, warns)
}

// ImportPuTTY adds the SSH sessions saved in PuTTY.
func (a *App) ImportPuTTY() (ImportResult, error) {
	if !a.storeUnlocked() {
		return ImportResult{}, errVaultLocked
	}
	read := importers.ReadPuTTYSessions
	if a.putty != nil {
		read = func() ([]model.Host, []string, error) { return importers.ReadPuTTYSessionsFrom(a.putty) }
	}
	hosts, warns, err := read()
	if err != nil {
		return ImportResult{}, errReadPuTTY
	}
	return a.importHosts(hosts, warns)
}

// hostIdentity is user@address:port, the address case-insensitive: the duplicate test of imports.
func hostIdentity(h model.Host) string {
	return h.User + "@" + strings.ToLower(h.Address) + ":" + strconv.Itoa(h.Port)
}

// importHosts validates the hosts like the host dialog does, except for the key, and saves the
// new ones in one write. Hosts already saved or repeated in the import are skipped.
func (a *App) importHosts(in []model.Host, warns []string) (ImportResult, error) {
	res := ImportResult{Warnings: append([]string{}, warns...)}
	known := map[string]bool{}
	for _, h := range a.store.Hosts() {
		known[hostIdentity(h)] = true
	}
	var add []model.Host
	for _, src := range in {
		label := src.Name
		if label == "" {
			label = src.Address
		}
		h, err := normalizeHost(HostDTO{Name: src.Name, Address: src.Address, Port: src.Port, User: src.User})
		if err != nil {
			res.Skipped++
			res.Warnings = append(res.Warnings, label+": "+fieldText(err))
			continue
		}
		id := hostIdentity(h)
		if known[id] {
			res.Skipped++
			res.Warnings = append(res.Warnings, label+": такой хост уже есть")
			continue
		}
		known[id] = true
		add = append(add, h)
	}
	if len(add) > 0 {
		if _, err := a.store.AddHosts(add); errors.Is(err, store.ErrLocked) {
			return ImportResult{}, errVaultLocked // the idle lock came during the import
		} else if err != nil {
			return ImportResult{}, err
		}
	}
	res.Added = len(add)
	return res, nil
}

// fieldText drops the field:<name>: prefix of a validation error.
func fieldText(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, fieldErrorPrefix); ok {
		if _, text, ok := strings.Cut(rest, ": "); ok {
			return text
		}
	}
	return msg
}

// exportHost is a host in the export file: connection data and the group name, never a key.
type exportHost struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Group   string `json:"group,omitempty"`
}

// hostsExportJSON is the export file: {"version":1,"hosts":[…]} sorted by name.
func (a *App) hostsExportJSON() ([]byte, error) {
	out := struct {
		Version int          `json:"version"`
		Hosts   []exportHost `json:"hosts"`
	}{Version: 1, Hosts: []exportHost{}}
	for _, h := range a.HostsList() {
		out.Hosts = append(out.Hosts, exportHost{Name: h.Name, Address: h.Address, Port: h.Port, User: h.User, Group: h.GroupName})
	}
	return json.MarshalIndent(out, "", "  ")
}

// HostsExport saves the hosts without keys as JSON to a file chosen in the system dialog.
func (a *App) HostsExport() error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	data, err := a.hostsExportJSON()
	if err != nil {
		return err
	}
	if a.wapp == nil {
		return errNoWindow
	}
	path, err := dialogPath(a.wapp.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "Экспорт хостов",
		Filename: "hosts.json",
		Filters:  []application.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}},
		Window:   a.mainWin,
	}).PromptForSingleSelection())
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// OpenDataFolder shows the data folder (%LOCALAPPDATA%\sshclient) in Explorer.
func (a *App) OpenDataFolder() error {
	dir, err := appdir.Dir()
	if err != nil {
		return err
	}
	if a.wapp == nil {
		return errNoWindow
	}
	if err := a.wapp.Env.OpenFileManager(dir, false); err != nil {
		return fmt.Errorf("open data folder: %w", err)
	}
	return nil
}
