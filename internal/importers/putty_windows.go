package importers

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// winRegistry reads the sessions from HKEY_CURRENT_USER.
type winRegistry struct{}

func newRegistryReader() RegistryReader { return winRegistry{} }

func (winRegistry) Sessions() ([]string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsKey, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = k.Close() }() //nolint:gosec // G104: read-only key, close errors are irrelevant
	return k.ReadSubKeyNames(-1)
}

func (winRegistry) open(session string) (registry.Key, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsKey+`\`+session, registry.QUERY_VALUE)
	return k, err == nil
}

func (r winRegistry) String(session, name string) (string, bool) {
	k, ok := r.open(session)
	if !ok {
		return "", false
	}
	defer func() { _ = k.Close() }() //nolint:gosec // G104: read-only key, close errors are irrelevant
	v, _, err := k.GetStringValue(name)
	return v, err == nil
}

func (r winRegistry) Int(session, name string) (int, bool) {
	k, ok := r.open(session)
	if !ok {
		return 0, false
	}
	defer func() { _ = k.Close() }() //nolint:gosec // G104: read-only key, close errors are irrelevant
	v, _, err := k.GetIntegerValue(name)
	if err != nil || v > 65535 {
		return 0, false
	}
	return int(v), true //nolint:gosec // G115: bounded above
}
