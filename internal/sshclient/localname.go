package sshclient

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutside is returned when a name from the server would put a file outside the chosen folder.
var ErrOutside = errors.New("имя с сервера выводит за пределы выбранной папки")

// reservedNames are the device names Windows reserves in every folder, with any extension.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeLocalName turns one name from the server into a name Windows accepts: the characters
// <>:"/\|?* and control characters become _, a trailing dot or space becomes _, and a reserved
// device name (CON, NUL.txt, COM1…) gets a leading _. changed reports that the name differs.
// The result is never empty, "." or "..".
func SafeLocalName(name string) (safe string, changed bool) {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s := []rune(b.String())
	for i := len(s) - 1; i >= 0 && (s[i] == '.' || s[i] == ' '); i-- {
		s[i] = '_'
	}
	out := string(s)
	if out == "" {
		out = "_"
	}
	stem := out
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	if reservedNames[strings.ToUpper(strings.TrimRight(stem, " "))] {
		out = "_" + out
	}
	return out, out != name
}

// LocalTarget joins root with the components of rel (slash-separated, from the server), each made
// safe with SafeLocalName, and checks that the result stays inside root. renamed counts the
// components that had to change.
func LocalTarget(root, rel string) (target string, renamed int, err error) {
	parts := strings.Split(rel, "/")
	safe := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		s, ch := SafeLocalName(p)
		if ch {
			renamed++
		}
		safe = append(safe, s)
	}
	if len(safe) == 0 {
		return "", 0, fmt.Errorf("%w: пустое имя", ErrOutside)
	}
	target = filepath.Join(append([]string{root}, safe...)...)
	if err := CheckInside(root, target); err != nil {
		return "", 0, err
	}
	return target, renamed, nil
}

// CheckInside reports ErrOutside unless target is root itself or lies under it.
func CheckInside(root, target string) error {
	r, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || r != "." && !filepath.IsLocal(r) {
		return fmt.Errorf("%w: %s", ErrOutside, target)
	}
	return nil
}
