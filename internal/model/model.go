// Package model defines the persisted data types of the SSH client.
package model

import "time"

// Host is a saved SSH connection target. OS is one of ubuntu|debian|other
// and only drives the monogram colour.
type Host struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	GroupID   string    `json:"group_id"`
	KeyID     string    `json:"key_id"`
	OS        string    `json:"os"`
	CreatedAt time.Time `json:"created_at"`
	// MCPAccess is what a connected AI client may do on the host: closed (empty), read or full.
	MCPAccess string `json:"mcp_access,omitempty"`
}

// Host access modes for MCP clients.
const (
	MCPAccessClosed = ""
	MCPAccessRead   = "read"
	MCPAccessFull   = "full"
)

// DefaultMCPPort is the loopback port of the MCP server; a stored zero means this value.
const DefaultMCPPort = 47821

// Group collects hosts in the sidebar.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Key is a stored private key. Type is ed25519|rsa; Source is generated|openssh|ppk.
// Private holds the original key file bytes in Format (openssh|pem|ppk); when
// Encrypted, they stay encrypted with the key's own passphrase, which is never stored.
type Key struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Private     []byte    `json:"private"`
	Format      string    `json:"format"`
	Encrypted   bool      `json:"encrypted"`
	PublicLine  string    `json:"public_line"`
	Comment     string    `json:"comment"`
	Fingerprint string    `json:"fingerprint"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

// Forward is a port forwarding rule. Kind is L|R|D; Listen and Target are
// host:port, Target is empty for D.
type Forward struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	HostID    string `json:"host_id"`
	Listen    string `json:"listen"`
	Target    string `json:"target"`
	AutoStart bool   `json:"auto_start"`
}

// Settings are user preferences.
type Settings struct {
	TerminalFont string      `json:"terminal_font"`
	TerminalSize int         `json:"terminal_size"`
	CopyOnSelect bool        `json:"copy_on_select"`
	BlinkCursor  bool        `json:"blink_cursor"`
	WebGL        bool        `json:"webgl"`
	LockAfterMin int         `json:"lock_after_min"`
	Window       WindowState `json:"window"`
	// MCP server switch, port and bearer token. They are Go's: SaveSettings keeps the stored
	// values and GetSettings never returns the token.
	MCPEnabled bool   `json:"mcp_enabled,omitempty"`
	MCPPort    int    `json:"mcp_port,omitempty"`
	MCPToken   string `json:"mcp_token,omitempty"`
}

// MCPRecord is one entry of the journal of MCP tool calls. Refused is empty for an executed
// call, otherwise the reason; ExitCode and DurationMs are set when a command finishes.
type MCPRecord struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	HostID     string    `json:"host_id"`
	HostName   string    `json:"host_name"`
	Tool       string    `json:"tool"`
	Text       string    `json:"text"`
	Access     string    `json:"access"`
	Refused    string    `json:"refused,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	DurationMs int64     `json:"duration_ms,omitempty"`
}

// WindowState is the last window geometry.
type WindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
	Maximized bool `json:"maximized"`
}

// VaultData is the whole decrypted vault payload.
type VaultData struct {
	Hosts    []Host    `json:"hosts"`
	Groups   []Group   `json:"groups"`
	Keys     []Key     `json:"keys"`
	Forwards []Forward `json:"forwards"`
	Settings Settings  `json:"settings"`
	// MCPJournal is the last MCP calls, oldest first.
	MCPJournal []MCPRecord `json:"mcp_journal,omitempty"`
}

// DefaultSettings returns the settings used for a new vault.
func DefaultSettings() Settings {
	return Settings{
		TerminalFont: "Cascadia Mono",
		TerminalSize: 13,
		CopyOnSelect: true,
		BlinkCursor:  true,
		WebGL:        true,
		MCPPort:      DefaultMCPPort,
		Window:       WindowState{W: 1280, H: 800},
	}
}
