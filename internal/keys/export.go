package keys

import (
	"crypto/ed25519"
	"fmt"

	"github.com/liornabat/putty"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// PublicLine returns the authorized_keys style public line of the key.
func PublicLine(k model.Key) string { return k.PublicLine }

// PrivateOpenSSH returns the unencrypted OpenSSH private key.
func PrivateOpenSSH(k model.Key) []byte { return k.PrivatePEM }

// PrivatePPK exports the key as an unencrypted PuTTY PPK v3 file.
func PrivatePPK(k model.Key) ([]byte, error) {
	raw, err := ssh.ParseRawPrivateKey(k.PrivatePEM)
	if err != nil {
		return nil, fmt.Errorf("keys: export ppk: %w", err)
	}
	if p, ok := raw.(*ed25519.PrivateKey); ok {
		raw = *p
	}
	pk := &putty.Key{Version: 3, Comment: k.Comment}
	if err := pk.SetKey(raw); err != nil {
		return nil, fmt.Errorf("keys: export ppk: %w", err)
	}
	out, err := pk.Marshal()
	if err != nil {
		return nil, fmt.Errorf("keys: export ppk: %w", err)
	}
	return out, nil
}
