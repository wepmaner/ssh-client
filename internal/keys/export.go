package keys

import (
	"crypto/ed25519"
	"encoding/pem"
	"fmt"

	"github.com/liornabat/putty"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// PublicLine returns the authorized_keys style public line of the key.
func PublicLine(k model.Key) string { return k.PublicLine }

// PrivateOriginal returns the stored key file as it was imported or
// generated, encrypted or not, and the file extension of its format.
func PrivateOriginal(k model.Key) ([]byte, string) {
	switch k.Format {
	case FormatPPK:
		return k.Private, ".ppk"
	case FormatPEM:
		return k.Private, ".pem"
	}
	return k.Private, ""
}

// plainRaw parses an unencrypted key for conversion; encrypted keys are only
// exported as they are (PrivateOriginal).
func plainRaw(k model.Key) (any, error) {
	if k.Encrypted {
		return nil, ErrKeyEncrypted
	}
	raw, err := rawPrivate(k, nil)
	if err != nil {
		return nil, err
	}
	if p, ok := raw.(*ed25519.PrivateKey); ok {
		raw = *p
	}
	return raw, nil
}

// PrivateOpenSSH returns the key as an unencrypted OpenSSH private key. An
// OpenSSH original is returned unchanged; an encrypted key gives ErrKeyEncrypted.
func PrivateOpenSSH(k model.Key) ([]byte, error) {
	if !k.Encrypted && k.Format == FormatOpenSSH {
		return k.Private, nil
	}
	raw, err := plainRaw(k)
	if err != nil {
		return nil, fmt.Errorf("keys: export openssh: %w", err)
	}
	blk, err := ssh.MarshalPrivateKey(raw, k.Comment)
	if err != nil {
		return nil, fmt.Errorf("keys: export openssh: %w", err)
	}
	return pem.EncodeToMemory(blk), nil
}

// PrivatePPK exports an unencrypted key as an unencrypted PuTTY PPK v3 file;
// an encrypted key gives ErrKeyEncrypted.
func PrivatePPK(k model.Key) ([]byte, error) {
	raw, err := plainRaw(k)
	if err != nil {
		return nil, fmt.Errorf("keys: export ppk: %w", err)
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
