package importers

import (
	"fmt"
	"net/url"
	"strings"

	"sshclient/internal/model"
)

// puttySessionsKey is where PuTTY keeps its saved sessions, under HKEY_CURRENT_USER.
const puttySessionsKey = `Software\SimonTatham\PuTTY\Sessions`

// puttyDefaults is the session PuTTY uses as the template of new ones; it is not a host.
const puttyDefaults = "Default Settings"

// RegistryReader reads PuTTY sessions: Sessions lists the subkey names as stored (URL-encoded),
// String and Int read a REG_SZ or REG_DWORD value of one session.
type RegistryReader interface {
	Sessions() ([]string, error)
	String(session, name string) (string, bool)
	Int(session, name string) (int, bool)
}

// ReadPuTTYSessions reads the saved PuTTY sessions of the current Windows user.
// Without PuTTY the result is empty.
func ReadPuTTYSessions() ([]model.Host, []string, error) {
	return ReadPuTTYSessionsFrom(newRegistryReader())
}

// ReadPuTTYSessionsFrom turns the sessions of reg into hosts named after the decoded session
// name. HostName may carry user@; non-SSH sessions and sessions without an address are
// skipped with a warning.
func ReadPuTTYSessionsFrom(reg RegistryReader) ([]model.Host, []string, error) {
	names, err := reg.Sessions()
	if err != nil {
		return nil, nil, fmt.Errorf("read PuTTY sessions: %w", err)
	}
	var hosts []model.Host
	var warns []string
	for _, raw := range names {
		name, err := url.PathUnescape(raw)
		if err != nil {
			name = raw
		}
		if name == puttyDefaults {
			continue
		}
		if proto, ok := reg.String(raw, "Protocol"); ok && proto != "" && proto != "ssh" {
			warns = append(warns, fmt.Sprintf("%s: сессия %s пропущена, импортируются только SSH", name, proto))
			continue
		}
		addr, _ := reg.String(raw, "HostName")
		addr = strings.TrimSpace(addr)
		user, _ := reg.String(raw, "UserName")
		if u, a, ok := strings.Cut(addr, "@"); ok {
			if user == "" {
				user = u
			}
			addr = a
		}
		if addr == "" {
			warns = append(warns, name+": не указан адрес")
			continue
		}
		port, ok := reg.Int(raw, "PortNumber")
		if !ok || port < 1 || port > 65535 {
			port = defaultPort
		}
		hosts = append(hosts, model.Host{Name: name, Address: addr, Port: port, User: strings.TrimSpace(user)})
	}
	return hosts, warns, nil
}
