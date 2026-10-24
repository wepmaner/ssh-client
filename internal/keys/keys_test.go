package keys

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"sshclient/internal/model"
)

const ppkPassphrase = "testkey"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // fixed testdata directory
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestGenerateEd25519(t *testing.T) {
	k, err := Generate("ed25519", "me@host")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ed25519" || k.Source != "generated" || k.Comment != "me@host" || k.CreatedAt.IsZero() {
		t.Fatalf("unexpected metadata: %+v", k)
	}
	if !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") || !strings.HasSuffix(k.PublicLine, " me@host") {
		t.Fatalf("bad public line %q", k.PublicLine)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || strings.Contains(k.Fingerprint, "=") {
		t.Fatalf("bad fingerprint %q", k.Fingerprint)
	}
	got, err := ParsePrivate(k.PrivatePEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != k.Fingerprint {
		t.Fatalf("fingerprint mismatch %q vs %q", got.Fingerprint, k.Fingerprint)
	}
	if got.Comment != "me@host" {
		t.Fatalf("comment lost: %q", got.Comment)
	}
}

func TestGenerateRSAIs4096(t *testing.T) {
	k, err := Generate("rsa", "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "rsa" || !strings.HasPrefix(k.PublicLine, "ssh-rsa ") {
		t.Fatalf("unexpected key: %+v", k)
	}
	raw, err := ssh.ParseRawPrivateKey(k.PrivatePEM)
	if err != nil {
		t.Fatal(err)
	}
	rk, ok := raw.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("got %T", raw)
	}
	if rk.N.BitLen() != 4096 {
		t.Fatalf("modulus %d bits", rk.N.BitLen())
	}
}

func TestGenerateUnknownKind(t *testing.T) {
	if _, err := Generate("dsa", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseOpenSSHNoPassphrase(t *testing.T) {
	k, err := ParsePrivate(readFixture(t, "ed25519_openssh"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ed25519" || k.Source != "openssh" || k.Comment != "fixture@test" {
		t.Fatalf("unexpected: %+v", k)
	}
	if !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected: %+v", k)
	}
	if !strings.HasPrefix(string(k.PrivatePEM), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatal("not re-encoded as OpenSSH")
	}
}

func TestParseOpenSSHEncrypted(t *testing.T) {
	pk, err := ssh.ParseRawPrivateKey(readFixture(t, "ed25519_openssh"))
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(pk, "c", []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	enc := pem.EncodeToMemory(blk)
	if _, err := ParsePrivate(enc, nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
	if _, err := ParsePrivate(enc, []byte("bad")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("want ErrWrongPassphrase, got %v", err)
	}
	k, err := ParsePrivate(enc, []byte("pw"))
	if err != nil {
		t.Fatalf("got %+v, %v", k, err)
	}
}

func TestParsePEMRSA(t *testing.T) {
	k, err := ParsePrivate(readFixture(t, "rsa_pkcs1.pem"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "rsa" || k.Source != "openssh" || !strings.HasPrefix(k.PublicLine, "ssh-rsa ") {
		t.Fatalf("unexpected: %+v", k)
	}
	if !strings.HasPrefix(string(k.PrivatePEM), "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatal("not re-encoded as OpenSSH")
	}
	blk, _ := pem.Decode(readFixture(t, "rsa_pkcs1.pem"))
	orig, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&orig.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if k.Fingerprint != FingerprintSHA256(pub) {
		t.Fatal("fingerprint differs from original")
	}
}

func TestParsePPKv3NeedsPassphrase(t *testing.T) {
	data := readFixture(t, "v3.ppk")
	if _, err := ParsePrivate(data, nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
	if _, err := ParsePrivate(data, []byte("wrong")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("want ErrWrongPassphrase, got %v", err)
	}
	k, err := ParsePrivate(data, []byte(ppkPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	if k.Source != "ppk" || k.Type != "rsa" || k.Comment != "a@b" {
		t.Fatalf("unexpected: %+v", k)
	}
	plain, err := ParsePrivate(readFixture(t, "v3-plain.ppk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Fingerprint != k.Fingerprint {
		t.Fatal("encrypted and plain v3 fixtures should hold the same key")
	}
}

func TestParsePPKv2(t *testing.T) {
	k, err := ParsePrivate(readFixture(t, "v2.ppk"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Source != "ppk" || k.Type != "rsa" || k.Comment != "a@b" || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected: %+v", k)
	}
}

func TestParseECDSAUnsupported(t *testing.T) {
	if _, err := ParsePrivate(readFixture(t, "ecdsa_openssh"), nil); !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("want ErrUnsupportedKeyType, got %v", err)
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := ParsePrivate([]byte("not a key"), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestExportPPKRoundTrip(t *testing.T) {
	ed, err := Generate("ed25519", "rt@test")
	if err != nil {
		t.Fatal(err)
	}
	// Reuse a fixture instead of a second slow 4096-bit generation.
	rsaKey, err := ParsePrivate(readFixture(t, "rsa_pkcs1.pem"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey.Comment = "rt@test"
	for name, k := range map[string]model.Key{"ed25519": ed, "rsa": rsaKey} {
		t.Run(name, func(t *testing.T) {
			ppk, err := PrivatePPK(k)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(ppk), "PuTTY-User-Key-File-3: ") || !strings.Contains(string(ppk), "Encryption: none") {
				t.Fatalf("not an unencrypted v3 file:\n%s", ppk)
			}
			got, err := ParsePrivate(ppk, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Fingerprint != k.Fingerprint || got.Source != "ppk" || got.Comment != "rt@test" {
				t.Fatalf("round trip mismatch: %+v vs %+v", got, k)
			}
		})
	}
}

func TestExportHelpers(t *testing.T) {
	k, err := Generate("ed25519", "x")
	if err != nil {
		t.Fatal(err)
	}
	if PublicLine(k) != k.PublicLine || string(PrivateOpenSSH(k)) != string(k.PrivatePEM) {
		t.Fatal("helpers must return stored values")
	}
}

func TestGenerateUsesCryptoRand(t *testing.T) {
	a, err := Generate("ed25519", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("ed25519", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("two generated keys share a fingerprint")
	}
}
