package keys

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/kayrus/putty"
	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

var (
	// ErrPassphraseRequired means the key is encrypted and no passphrase was given.
	ErrPassphraseRequired = errors.New("keys: passphrase required")
	// ErrWrongPassphrase means the given passphrase did not decrypt the key.
	ErrWrongPassphrase = errors.New("keys: wrong passphrase")
	// ErrUnsupportedKeyType means the key is neither ed25519 nor RSA.
	ErrUnsupportedKeyType = errors.New("keys: unsupported key type")
	// ErrInvalidKey means the key material is internally inconsistent.
	ErrInvalidKey = errors.New("keys: invalid key")
	// ErrKeyEncrypted means the operation needs an unencrypted key.
	ErrKeyEncrypted = errors.New("keys: key is encrypted")
)

// Key container formats stored in model.Key.Format.
const (
	FormatOpenSSH = "openssh"
	FormatPEM     = "pem"
	FormatPPK     = "ppk"
)

const (
	ppkMagic     = "PuTTY-User-Key-File-"
	opensshMagic = "openssh-key-v1\x00"
	opensshBlock = "OPENSSH PRIVATE KEY"
)

func isPPK(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte(ppkMagic))
}

// Inspect reads an OpenSSH, PEM or PuTTY PPK private key without a
// passphrase. Encrypted OpenSSH and PPK keys keep their public key in the
// clear, so the metadata comes from there. A legacy encrypted PEM
// (Proc-Type: 4,ENCRYPTED) has no public part and returns
// ErrPassphraseRequired; use InspectWithPassphrase for it. Private is always
// a copy of data, never decrypted or re-encoded.
func Inspect(data []byte) (model.Key, error) {
	if isPPK(data) {
		return inspectPPK(data)
	}
	return inspectPEM(data, nil)
}

// InspectWithPassphrase is Inspect for a legacy encrypted PEM: the passphrase
// only decrypts a transient copy to read the public key. It is not kept, and
// Private stays the original encrypted bytes. Other formats ignore it.
func InspectWithPassphrase(data, passphrase []byte) (model.Key, error) {
	if isPPK(data) {
		return inspectPPK(data)
	}
	return inspectPEM(data, passphrase)
}

func inspectPEM(data, passphrase []byte) (model.Key, error) {
	blk, _ := pem.Decode(data)
	if blk == nil {
		return model.Key{}, errors.New("keys: parse private key: no PEM block")
	}
	format := FormatPEM
	if blk.Type == opensshBlock {
		format = FormatOpenSSH
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	var missing *ssh.PassphraseMissingError
	switch {
	case err == nil:
		s, err := signerFromRaw(raw)
		if err != nil {
			return model.Key{}, err
		}
		return newKey(s.PublicKey(), opensshComment(data), format, false, data)
	case errors.As(err, &missing) && missing.PublicKey != nil:
		// OpenSSH container: the public key is stored unencrypted, the
		// comment is inside the encrypted part.
		return newKey(missing.PublicKey, "", format, true, data)
	case errors.As(err, &missing):
		if len(passphrase) == 0 {
			return model.Key{}, ErrPassphraseRequired
		}
		raw, err := pemRaw(data, format, true, passphrase)
		if err != nil {
			return model.Key{}, err
		}
		s, err := signerFromRaw(raw)
		if err != nil {
			return model.Key{}, err
		}
		return newKey(s.PublicKey(), "", format, true, data)
	}
	return model.Key{}, fmt.Errorf("keys: parse private key: %w", err)
}

func inspectPPK(data []byte) (model.Key, error) {
	pk, err := putty.New(data)
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: parse ppk: %w", err)
	}
	encrypted := pk.Encryption != "none"
	var pub ssh.PublicKey
	if encrypted {
		// Public-Lines are stored in the clear.
		if pub, err = ssh.ParsePublicKey(pk.PublicKey); err != nil {
			return model.Key{}, fmt.Errorf("keys: parse ppk public key: %w", err)
		}
	} else {
		// An unencrypted key is read in full so a broken one fails at import.
		raw, err := ppkRaw(data, nil)
		if err != nil {
			return model.Key{}, err
		}
		s, err := signerFromRaw(raw)
		if err != nil {
			return model.Key{}, err
		}
		pub = s.PublicKey()
	}
	return newKey(pub, pk.Comment, FormatPPK, encrypted, data)
}

// pemRaw parses an OpenSSH or PEM key. For an encrypted legacy PEM any
// failure after decryption is a wrong passphrase: CBC padding catches most
// wrong passphrases, the rest surface as a garbled ASN.1 structure.
func pemRaw(data []byte, format string, encrypted bool, passphrase []byte) (any, error) {
	var raw any
	var err error
	if encrypted {
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(data, passphrase)
	} else {
		raw, err = ssh.ParseRawPrivateKey(data)
	}
	var missing *ssh.PassphraseMissingError
	switch {
	case err == nil:
		return raw, nil
	case errors.As(err, &missing):
		return nil, ErrPassphraseRequired
	case errors.Is(err, x509.IncorrectPasswordError) || (encrypted && format == FormatPEM):
		return nil, fmt.Errorf("%w: %v", ErrWrongPassphrase, err)
	}
	return nil, fmt.Errorf("keys: parse private key: %w", err)
}

// ppkRaw parses a PPK key; a nil passphrase reads an unencrypted one. A wrong
// passphrase is detected by the file MAC.
func ppkRaw(data, passphrase []byte) (any, error) {
	pk, err := putty.New(data)
	if err != nil {
		return nil, fmt.Errorf("keys: parse ppk: %w", err)
	}
	encrypted := pk.Encryption != "none"
	if encrypted && len(passphrase) == 0 {
		return nil, ErrPassphraseRequired
	}
	if !encrypted {
		passphrase = nil
	}
	raw, err := pk.ParseRawPrivateKey(passphrase)
	// The library decrypts in place; the parsed key holds its own copies.
	clear(pk.PrivateKey)
	if err != nil {
		if encrypted {
			return nil, fmt.Errorf("%w: %v", ErrWrongPassphrase, err)
		}
		return nil, fmt.Errorf("keys: parse ppk: %w", err)
	}
	return raw, nil
}

// signerFromRaw validates a parsed private key and makes a signer of it.
func signerFromRaw(raw any) (ssh.Signer, error) {
	var priv any
	switch v := raw.(type) {
	case *rsa.PrivateKey:
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		v.Precompute()
		priv = v
	case ed25519.PrivateKey:
		priv = v
	case *ed25519.PrivateKey:
		priv = *v
	case *ecdsa.PrivateKey:
		return nil, fmt.Errorf("%w: ecdsa", ErrUnsupportedKeyType)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedKeyType, raw)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("keys: signer: %w", err)
	}
	return s, nil
}

// keyKind maps an SSH public key type to model.Key.Type.
func keyKind(pub ssh.PublicKey) (string, error) {
	switch pub.Type() {
	case ssh.KeyAlgoED25519:
		return "ed25519", nil
	case ssh.KeyAlgoRSA:
		return "rsa", nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupportedKeyType, pub.Type())
}

// newKey fills the metadata of a key from its public part; Private is a copy
// of the original bytes.
func newKey(pub ssh.PublicKey, comment, format string, encrypted bool, data []byte) (model.Key, error) {
	kind, err := keyKind(pub)
	if err != nil {
		return model.Key{}, err
	}
	comment = sanitizeComment(comment)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	source := "openssh"
	if format == FormatPPK {
		source = "ppk"
	}
	return model.Key{
		Type:        kind,
		Private:     bytes.Clone(data),
		Format:      format,
		Encrypted:   encrypted,
		PublicLine:  line,
		Comment:     comment,
		Fingerprint: FingerprintSHA256(pub),
		Source:      source,
	}, nil
}

// opensshComment extracts the comment of an unencrypted OpenSSH-format key.
// x/crypto/ssh does not expose it. Returns "" for encrypted keys, legacy PEM
// or anything it cannot read.
func opensshComment(data []byte) string {
	blk, _ := pem.Decode(data)
	if blk == nil || blk.Type != opensshBlock || !bytes.HasPrefix(blk.Bytes, []byte(opensshMagic)) {
		return ""
	}
	r := &wireReader{b: blk.Bytes[len(opensshMagic):]}
	cipher := r.string()
	r.string() // kdf name
	r.string() // kdf options
	nkeys := r.uint32()
	if r.err || cipher != "none" || nkeys != 1 {
		return ""
	}
	r.string() // public key
	r = &wireReader{b: []byte(r.string())}
	r.uint32() // check ints
	r.uint32()
	var fields int
	switch r.string() {
	case "ssh-ed25519":
		fields = 2 // public, private
	case "ssh-rsa":
		fields = 6 // n, e, d, iqmp, p, q
	default:
		return ""
	}
	for range fields {
		r.string()
	}
	comment := r.string()
	if r.err {
		return ""
	}
	return comment
}

// wireReader reads SSH wire-format values; on any overrun it sets err and
// returns zero values.
type wireReader struct {
	b   []byte
	err bool
}

func (r *wireReader) uint32() uint32 {
	if len(r.b) < 4 {
		r.err = true
		r.b = nil
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *wireReader) string() string {
	n := r.uint32()
	if r.err || uint64(n) > uint64(len(r.b)) {
		r.err = true
		r.b = nil
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

// sanitizeComment replaces control characters (including newlines) with
// spaces so the comment can never break out of a one-line public key.
func sanitizeComment(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}
