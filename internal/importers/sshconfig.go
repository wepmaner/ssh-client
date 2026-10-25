// Package importers reads hosts saved by other SSH clients: the OpenSSH config file and
// PuTTY sessions. Keys are never imported here; the warnings name the key files to import
// separately in the Keys section.
package importers

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"sshclient/internal/model"
)

const defaultPort = 22

// sshBlock is one Host section: its patterns and its keyword values in file order.
type sshBlock struct {
	patterns []string
	values   [][2]string // lower-case keyword, value
}

// ParseSSHConfig reads an OpenSSH client config. Every alias of a Host line without
// wildcards becomes a host named after the alias; HostName (default: the alias), Port
// (default 22) and User are taken as OpenSSH would, from every Host section that matches
// the alias, the first value winning. Match sections and Include are skipped with a
// warning, an IdentityFile becomes a warning to import the key separately.
func ParseSSHConfig(r io.Reader) ([]model.Host, []string, error) {
	blocks, warns, err := readSSHBlocks(r)
	if err != nil {
		return nil, nil, err
	}
	var hosts []model.Host
	seen := map[string]bool{}
	for _, b := range blocks {
		for _, alias := range b.patterns {
			if isPattern(alias) || seen[alias] {
				continue
			}
			seen[alias] = true
			h, hw, ok := resolveAlias(alias, blocks)
			warns = append(warns, hw...)
			if ok {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, warns, nil
}

// readSSHBlocks splits the file into Host sections. Values before the first Host line
// apply to every host, like a Host * section.
func readSSHBlocks(r io.Reader) ([]sshBlock, []string, error) {
	var warns []string
	blocks := []sshBlock{{patterns: []string{"*"}}}
	cur := &blocks[0]
	inMatch := false
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		key, val := splitConfigLine(sc.Text())
		switch key {
		case "":
			continue
		case "host":
			blocks = append(blocks, sshBlock{patterns: splitArgs(val)})
			cur, inMatch = &blocks[len(blocks)-1], false
			continue
		case "match":
			warns = append(warns, fmt.Sprintf("строка %d: секция Match пропущена", n))
			inMatch = true
			continue
		case "include":
			warns = append(warns, fmt.Sprintf("строка %d: Include %s не читается, перенесите хосты в основной файл", n, val))
			continue
		}
		if !inMatch {
			cur.values = append(cur.values, [2]string{key, unquote(val)})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("read ssh config: %w", err)
	}
	return blocks, warns, nil
}

// resolveAlias collects the values of alias from the sections that match it.
func resolveAlias(alias string, blocks []sshBlock) (model.Host, []string, bool) {
	var warns []string
	h := model.Host{Name: alias}
	var port string
	for _, b := range blocks {
		if !matchesAny(alias, b.patterns) {
			continue
		}
		for _, kv := range b.values {
			switch kv[0] {
			case "hostname":
				if h.Address == "" {
					h.Address = kv[1]
				}
			case "user":
				if h.User == "" {
					h.User = kv[1]
				}
			case "port":
				if port == "" {
					port = kv[1]
				}
			case "identityfile":
				warns = append(warns, fmt.Sprintf("ключ для %s нужно импортировать отдельно: %s", alias, kv[1]))
			}
		}
	}
	if h.Address == "" {
		h.Address = alias
	}
	// OpenSSH expands %h in HostName to the alias.
	h.Address = strings.ReplaceAll(h.Address, "%h", alias)
	h.Port = defaultPort
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return model.Host{}, append(warns, fmt.Sprintf("%s: некорректный порт %q, хост пропущен", alias, port)), false
		}
		h.Port = p
	}
	return h, warns, true
}

// splitConfigLine returns the lower-case keyword and the rest of a config line;
// "Key value", "Key=value" and "Key = value" are all accepted, # starts a comment.
func splitConfigLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", ""
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return strings.ToLower(line), ""
	}
	key := strings.ToLower(line[:i])
	val := strings.TrimLeft(line[i:], " \t")
	val = strings.TrimPrefix(val, "=")
	val = strings.TrimSpace(val)
	if j := commentStart(val); j >= 0 {
		val = strings.TrimSpace(val[:j])
	}
	return key, val
}

// commentStart finds a # that starts a trailing comment outside quotes.
func commentStart(s string) int {
	quoted := false
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return i
		}
	}
	return -1
}

// splitArgs splits a value on blanks, keeping "quoted parts" together.
func splitArgs(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t') && !quoted:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func isPattern(s string) bool { return strings.ContainsAny(s, "*?!") }

// matchesAny applies a Host line to alias as OpenSSH does: some pattern matches and no
// negated !pattern does.
func matchesAny(alias string, patterns []string) bool {
	hit := false
	for _, p := range patterns {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if globMatch(neg, alias) {
				return false
			}
			continue
		}
		if globMatch(p, alias) {
			hit = true
		}
	}
	return hit
}

// globMatch matches s against an OpenSSH pattern of * and ?, case-insensitively.
func globMatch(pattern, s string) bool {
	p, str := []rune(strings.ToLower(pattern)), []rune(strings.ToLower(s))
	var pi, si, star, mark = 0, 0, -1, 0
	for si < len(str) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == str[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi, mark = star+1, mark+1
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
