package keys

import (
	"fmt"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

// Signer parses the stored key in its original format and returns an SSH
// signer. An encrypted key needs its passphrase: none gives
// ErrPassphraseRequired, a wrong one ErrWrongPassphrase. The passphrase is
// only read; the caller owns and clears it.
func Signer(k model.Key, passphrase []byte) (ssh.Signer, error) {
	if k.Encrypted && len(passphrase) == 0 {
		return nil, ErrPassphraseRequired
	}
	if !k.Encrypted {
		passphrase = nil
	}
	raw, err := rawPrivate(k, passphrase)
	if err != nil {
		return nil, err
	}
	s, err := signerFromRaw(raw)
	if err != nil {
		return nil, err
	}
	// The public part of an encrypted container is not authenticated by
	// the cipher; the decrypted key must be the one shown to the user.
	if k.Fingerprint != "" && FingerprintSHA256(s.PublicKey()) != k.Fingerprint {
		return nil, fmt.Errorf("%w: private key does not match its public key", ErrInvalidKey)
	}
	return s, nil
}

// rawPrivate parses k.Private by k.Format.
func rawPrivate(k model.Key, passphrase []byte) (any, error) {
	switch k.Format {
	case FormatPPK:
		return ppkRaw(k.Private, passphrase)
	case FormatOpenSSH, FormatPEM:
		return pemRaw(k.Private, k.Format, k.Encrypted, passphrase)
	}
	return nil, fmt.Errorf("keys: unknown key format %q", k.Format)
}
