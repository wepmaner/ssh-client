package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCheckLocalPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	other := filepath.Join(root, "data-other")
	for _, d := range []string{filepath.Join(dir, "sub"), other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "vault.json"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	refused := map[string]string{
		"relative":       "vault.json",
		"relative dot":   `.\data\vault.json`,
		"empty":          "",
		"dir itself":     dir,
		"vault":          filepath.Join(dir, "vault.json"),
		"upper case":     filepath.Join(filepath.Dir(dir), "DATA", "VAULT.JSON"),
		"dotdot":         filepath.Join(dir, "sub", "..", "vault.json"),
		"new file":       filepath.Join(dir, "sub", "new", "file.txt"),
		"trailing dot":   filepath.Join(dir, "vault.json."),
		"trailing space": filepath.Join(dir, "vault.json "),
		"stream":         filepath.Join(dir, "vault.json:stream"),
		"extended":       `\\?\` + filepath.Join(dir, "vault.json"),
		"device":         `\\.\` + filepath.Join(dir, "vault.json"),
	}
	for name, p := range refused {
		if err := checkLocalPath(dir, p); err == nil {
			t.Errorf("%s: %q must be refused", name, p)
		}
	}

	// UNC-путь к собственной папке данных через административный ресурс.
	t.Run("UNC loopback", func(t *testing.T) {
		vol := filepath.VolumeName(dir)
		if len(vol) != 2 || vol[1] != ':' {
			t.Skipf("no drive letter in %q", dir)
		}
		unc := `\\localhost\` + vol[:1] + `$` + dir[2:]
		if _, err := os.Stat(unc); err != nil {
			t.Skipf("admin share not reachable: %v", err)
		}
		if err := checkLocalPath(dir, filepath.Join(unc, "vault.json")); err == nil {
			t.Errorf("UNC loopback %q must be refused", unc)
		}
	})

	allowed := map[string]string{
		"sibling prefix": filepath.Join(other, "x"),
		"root file":      filepath.Join(root, "a.txt"),
	}
	for name, p := range allowed {
		if err := checkLocalPath(dir, p); err != nil {
			t.Errorf("%s: %q refused: %v", name, p, err)
		}
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		// Без права на символические ссылки Windows всё равно даёт junction.
		if out, jerr := exec.Command("cmd", "/c", "mklink", "/J", link, dir).CombinedOutput(); jerr != nil { //nolint:gosec // G204: fixed command, paths of t.TempDir
			t.Skipf("no link available: %v; %v %s", err, jerr, out)
		}
	}
	for name, p := range map[string]string{
		"via link":          filepath.Join(link, "vault.json"),
		"new file via link": filepath.Join(link, "new", "file.txt"),
		"link itself":       link,
	} {
		if err := checkLocalPath(dir, p); err == nil {
			t.Errorf("%s: %q must be refused", name, p)
		}
	}
}
