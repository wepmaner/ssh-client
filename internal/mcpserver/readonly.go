package mcpserver

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// token is one shell word after quote removal. glob is set when it held an
// unquoted wildcard, so the shell could expand it to something else.
type token struct {
	text string
	glob bool
}

// CheckReadOnly decides whether a command may run on a host in the
// "view only" mode. It is default-deny: anything it does not understand is
// refused, so a false refusal is acceptable and a false allow is a defect. The error is the reason
// alone; ReadOnlyCommand makes the answer for the client.
func CheckReadOnly(command string) error {
	if !utf8.ValidString(command) {
		return errors.New("команда не в кодировке UTF-8")
	}
	segments, err := splitPipeline(command)
	if err != nil {
		return err
	}
	for _, seg := range segments {
		if len(seg) == 0 {
			return errors.New("пустое звено конвейера")
		}
		if err := checkSegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// splitPipeline scans the line rune by rune and cuts it at "|" outside quotes.
func splitPipeline(command string) ([][]token, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("пустая команда")
	}
	const (
		outside = iota
		single
		double
	)
	var (
		state    = outside
		segments [][]token
		seg      []token
		cur      strings.Builder
		inTok    bool
		glob     bool
	)
	endToken := func() {
		if inTok {
			seg = append(seg, token{cur.String(), glob})
		}
		cur.Reset()
		inTok, glob = false, false
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r < 0x20 && r != '\t' || r == 0x7f {
			return nil, errors.New("управляющие символы и перевод строки не разрешены")
		}
		switch state {
		case single:
			if r == '\'' {
				state = outside
			} else {
				cur.WriteRune(r)
			}
		case double:
			switch r {
			case '"':
				state = outside
			case '$', '`':
				return nil, errors.New("символы $ и обратная кавычка не разрешены")
			case '\\':
				return nil, errors.New("обратная косая черта не разрешена")
			default:
				cur.WriteRune(r)
			}
		default:
			switch r {
			case '$', '`':
				return nil, errors.New("символы $ и обратная кавычка не разрешены")
			case ';', '&', '>', '<', '(', ')', '\\', '{', '}':
				return nil, fmt.Errorf("символ %q не разрешён", r)
			case '|':
				if i+1 < len(runes) && runes[i+1] == '|' {
					return nil, errors.New("оператор || не разрешён")
				}
				endToken()
				segments = append(segments, seg)
				seg = nil
			case ' ', '\t':
				endToken()
			case '\'':
				state, inTok = single, true
			case '"':
				state, inTok = double, true
			default:
				if r == '*' || r == '?' || r == '[' {
					glob = true
				}
				inTok = true
				cur.WriteRune(r)
			}
		}
	}
	if state != outside {
		return nil, errors.New("незакрытая кавычка")
	}
	endToken()
	segments = append(segments, seg)
	return segments, nil
}

var unrestricted = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "wc": true,
	"stat": true, "df": true, "du": true, "free": true, "uptime": true, "uname": true,
	"whoami": true, "id": true, "ps": true,
}

func checkSegment(seg []token) error {
	name := seg[0].text
	if seg[0].glob {
		return fmt.Errorf("в имени команды %q маска не разрешена", name)
	}
	args := seg[1:]
	if unrestricted[name] {
		return nil
	}
	// Restricted commands are matched by exact text, so a wildcard that
	// the shell could expand into a different argument is refused.
	for _, a := range args {
		if a.glob {
			return fmt.Errorf("маска %q в аргументах %s не разрешена", a.text, name)
		}
	}
	texts := make([]string, len(args))
	for i, a := range args {
		texts[i] = a.text
	}
	var ok bool
	switch name {
	case "date":
		ok = len(texts) == 0
	case "hostname":
		ok = onlyFrom(texts, "-f", "-I")
	case "ss":
		ok = checkSS(texts)
	case "ip":
		ok = checkIP(texts)
	case "systemctl":
		ok = checkSystemctl(texts)
	case "journalctl":
		ok = checkJournalctl(texts)
	case "docker":
		ok = checkDocker(texts)
	default:
		return fmt.Errorf("команда %q не входит в список разрешённых", name)
	}
	if !ok {
		return fmt.Errorf("%s с такими аргументами не разрешена", name)
	}
	return nil
}

func onlyFrom(args []string, allowed ...string) bool {
	for _, a := range args {
		found := false
		for _, w := range allowed {
			if a == w {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func isLong(a string) (string, bool) {
	if !strings.HasPrefix(a, "--") {
		return "", false
	}
	n := a[2:]
	if i := strings.IndexByte(n, '='); i >= 0 {
		n = n[:i]
	}
	return n, true
}

// checkSS refuses -K/--kill and -D/--diag (it writes a file). Long options
// may be abbreviated, so any prefix of those names is refused too.
func checkSS(args []string) bool {
	for _, a := range args {
		if n, long := isLong(a); long {
			if n == "" || strings.HasPrefix("kill", n) || strings.HasPrefix("diag", n) {
				return false
			}
			continue
		}
		if strings.HasPrefix(a, "-") && strings.ContainsAny(a, "KD") {
			return false
		}
	}
	return true
}

func checkIP(args []string) bool {
	safeOpts := []string{"-4", "-6", "-s", "-d", "-details", "-br", "-brief", "-j", "-json", "-p", "-o", "-oneline", "-h", "-human", "-c"}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if !onlyFrom(args[i:i+1], safeOpts...) {
			return false
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	switch args[i] {
	case "addr", "address", "route", "link":
	default:
		return false
	}
	i++
	return i == len(args) || args[i] == "show"
}

func checkSystemctl(args []string) bool {
	valueless := []string{"--no-pager", "-l", "--full", "-a", "--all", "--no-legend", "--plain", "--user", "--system", "-q", "--quiet", "--failed", "-r", "--recursive", "--value"}
	withValue := []string{"-n", "-p", "-o", "-t"}
	valuePrefixes := []string{"--lines=", "--output=", "--property=", "--type=", "--state="}
	safeAfter := func(a string) bool {
		if onlyFrom([]string{a}, valueless...) || onlyFrom([]string{a}, withValue...) {
			return true
		}
		for _, p := range valuePrefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
		return false
	}
	// Options before the verb must take no value: a value option could
	// swallow the next word and shift which word getopt treats as the verb.
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if !onlyFrom(args[i:i+1], valueless...) {
			return false
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	switch args[i] {
	case "status", "is-active", "is-enabled", "list-units", "show":
	default:
		return false
	}
	for _, a := range args[i+1:] {
		if strings.HasPrefix(a, "-") && !safeAfter(a) {
			return false
		}
	}
	return true
}

func checkJournalctl(args []string) bool {
	denied := []string{"rotate", "vacuum-size", "vacuum-time", "vacuum-files", "flush", "sync",
		"relinquish-var", "smart-relinquish-var", "setup-keys", "update-catalog", "cursor-file"}
	for _, a := range args {
		n, long := isLong(a)
		if !long {
			continue
		}
		if n == "" {
			return false
		}
		for _, d := range denied {
			if strings.HasPrefix(d, n) {
				return false
			}
		}
	}
	return true
}

func checkDocker(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "ps", "logs", "inspect", "stats", "images", "version", "info":
		return true
	case "compose":
		return len(args) >= 2 && (args[1] == "ps" || args[1] == "logs")
	}
	return false
}
