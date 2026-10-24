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
)

const (
	ppkMagic     = "PuTTY-User-Key-File-"
	opensshMagic = "openssh-key-v1\x00"
)

// ParsePrivate parses an OpenSSH, PEM or PuTTY PPK private key. The result is
// always re-encoded as an unencrypted OpenSSH private key.
func ParsePrivate(data []byte, passphrase []byte) (model.Key, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte(ppkMagic)) {
		return parsePPK(data, passphrase)
	}
	return parseOpenSSH(data, passphrase)
}

func parseOpenSSH(data, passphrase []byte) (model.Key, error) {
	raw, err := ssh.ParseRawPrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if len(passphrase) == 0 {
			return model.Key{}, ErrPassphraseRequired
		}
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(data, passphrase)
		if errors.Is(err, x509.IncorrectPasswordError) {
			return model.Key{}, fmt.Errorf("%w: %v", ErrWrongPassphrase, err)
		}
	}
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: parse private key: %w", err)
	}
	return build(raw, opensshComment(data), "openssh")
}

func parsePPK(data, passphrase []byte) (model.Key, error) {
	pk, err := putty.New(data)
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: parse ppk: %w", err)
	}
	if pk.Encryption != "none" && len(passphrase) == 0 {
		return model.Key{}, ErrPassphraseRequired
	}
	raw, err := pk.ParseRawPrivateKey(passphrase)
	if err != nil {
		if pk.Encryption != "none" {
			return model.Key{}, fmt.Errorf("%w: %v", ErrWrongPassphrase, err)
		}
		return model.Key{}, fmt.Errorf("keys: parse ppk: %w", err)
	}
	return build(raw, pk.Comment, "ppk")
}

// build normalizes a parsed private key into a model.Key with an unencrypted
// OpenSSH private key, public line and fingerprint.
func build(raw any, comment, source string) (model.Key, error) {
	var priv any
	var kind string
	switch v := raw.(type) {
	case *rsa.PrivateKey:
		v.Precompute()
		priv, kind = v, "rsa"
	case ed25519.PrivateKey:
		priv, kind = v, "ed25519"
	case *ed25519.PrivateKey:
		priv, kind = *v, "ed25519"
	case *ecdsa.PrivateKey:
		return model.Key{}, fmt.Errorf("%w: ecdsa", ErrUnsupportedKeyType)
	default:
		return model.Key{}, fmt.Errorf("%w: %T", ErrUnsupportedKeyType, raw)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: signer: %w", err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return model.Key{}, fmt.Errorf("keys: encode private key: %w", err)
	}
	pub := signer.PublicKey()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment != "" {
		line += " " + comment
	}
	return model.Key{
		Type:        kind,
		PrivatePEM:  pem.EncodeToMemory(blk),
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
	if blk == nil || blk.Type != "OPENSSH PRIVATE KEY" || !bytes.HasPrefix(blk.Bytes, []byte(opensshMagic)) {
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
