//go:build !windows

package importers

// noRegistry stands in outside Windows, where PuTTY keeps no registry sessions.
type noRegistry struct{}

func newRegistryReader() RegistryReader { return noRegistry{} }

func (noRegistry) Sessions() ([]string, error)          { return nil, nil }
func (noRegistry) String(string, string) (string, bool) { return "", false }
func (noRegistry) Int(string, string) (int, bool)       { return 0, false }
