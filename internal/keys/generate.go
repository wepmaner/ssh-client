// Package keys generates, parses and exports SSH keys (ed25519 and RSA).
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"sshclient/internal/model"
)

const rsaBits = 4096

// Generate creates a new key of the given kind ("ed25519" or "rsa"). The Name
// field is left empty for the caller to fill.
func Generate(kind string, comment string) (model.Key, error) {
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
		return model.Key{}, errors.New("keys: unknown key kind " + kind)
	}
	k, err := build(priv, comment, "generated")
	if err != nil {
		return model.Key{}, err
	}
	k.CreatedAt = time.Now()
	return k, nil
}
