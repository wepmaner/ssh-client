// Package keys generates, inspects, signs with and exports SSH keys (ed25519
// and RSA). Keys are kept in their original bytes; an encrypted key is only
// ever decrypted transiently, with a passphrase the caller supplies.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

const rsaBits = 4096

// Generate creates a new key of the given kind ("ed25519" or "rsa") as an
// OpenSSH private key. A non-empty passphrase encrypts it (bcrypt KDF,
// aes256-ctr); the passphrase is not kept. The Name field is left empty for
// the caller to fill.
func Generate(kind, comment string, passphrase []byte) (model.Key, error) {
	var priv any
	switch kind {
	case "ed25519":
		_, p, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return model.Key{}, fmt.Errorf("keys: generate ed25519: %w", err)
		}
		priv = p
	case "rsa":
		p, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			return model.Key{}, fmt.Errorf("keys: generate rsa: %w", err)
		}
		priv = p
	default:
		return model.Key{}, fmt.Errorf("%w: %s", ErrUnsupportedKeyType, kind)
	}
	comment = sanitizeComment(comment)
	s, err := signerFromRaw(priv)
	if err != nil {
		return model.Key{}, err
	}
	var blk *pem.Block
	if len(passphrase) > 0 {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, passphrase)
	} else {
		blk, err = ssh.MarshalPrivateKey(priv, comment)
	}
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: encode private key: %w", err)
	}
	k, err := newKey(s.PublicKey(), comment, FormatOpenSSH, len(passphrase) > 0, pem.EncodeToMemory(blk))
	if err != nil {
		return model.Key{}, err
	}
	k.Source = "generated"
	k.CreatedAt = time.Now()
	return k, nil
}
