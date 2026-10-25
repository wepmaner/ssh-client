package keys

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
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

// encryptedOpenSSH re-encrypts the ed25519 fixture with passphrase pw.
func encryptedOpenSSH(t *testing.T, pw string) []byte {
	t.Helper()
	raw, err := ssh.ParseRawPrivateKey(readFixture(t, "ed25519_openssh"))
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(raw, "fixture@test", []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(blk)
}

// legacyPEM encrypts the RSA PKCS#1 fixture with Proc-Type: 4,ENCRYPTED.
func legacyPEM(t *testing.T, pw string) []byte {
	t.Helper()
	blk, _ := pem.Decode(readFixture(t, "rsa_pkcs1.pem"))
	//nolint:staticcheck // SA1019: legacy PEM encryption is exactly what this fixture needs
	enc, err := x509.EncryptPEMBlock(rand.Reader, blk.Type, blk.Bytes, []byte(pw), x509.PEMCipherAES256)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(enc)
}

func mustInspect(t *testing.T, data []byte) model.Key {
	t.Helper()
	k, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestGenerateEd25519(t *testing.T) {
	k, err := Generate("ed25519", "me@host", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "ed25519" || k.Source != "generated" || k.Comment != "me@host" || k.CreatedAt.IsZero() {
		t.Fatalf("unexpected metadata: %+v", k)
	}
	if k.Format != "openssh" || k.Encrypted {
		t.Fatalf("format %q encrypted %v", k.Format, k.Encrypted)
	}
	if !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") || !strings.HasSuffix(k.PublicLine, " me@host") {
		t.Fatalf("bad public line %q", k.PublicLine)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || strings.Contains(k.Fingerprint, "=") {
		t.Fatalf("bad fingerprint %q", k.Fingerprint)
	}
	got := mustInspect(t, k.Private)
	if got.Fingerprint != k.Fingerprint || got.Comment != "me@host" {
		t.Fatalf("round trip: %+v vs %+v", got, k)
	}
}

func TestGenerateRSAIs4096(t *testing.T) {
	k, err := Generate("rsa", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Type != "rsa" || !strings.HasPrefix(k.PublicLine, "ssh-rsa ") {
		t.Fatalf("unexpected key: %+v", k)
	}
	raw, err := ssh.ParseRawPrivateKey(k.Private)
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
	if _, err := Generate("dsa", "", nil); !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("want ErrUnsupportedKeyType, got %v", err)
	}
}

func TestGenerateWithPassphrase(t *testing.T) {
	k, err := Generate("ed25519", "c", []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if !k.Encrypted || k.Format != "openssh" || !strings.HasSuffix(k.PublicLine, " c") {
		t.Fatalf("unexpected: %+v", k)
	}
	if _, err := ssh.ParseRawPrivateKey(k.Private); err == nil {
		t.Fatal("stored key is not encrypted")
	}
	if _, err := Signer(k, nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
	s, err := Signer(k, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(s.PublicKey()) != k.Fingerprint {
		t.Fatal("signer does not match the stored public key")
	}
	again := mustInspect(t, k.Private)
	if !again.Encrypted || again.Fingerprint != k.Fingerprint {
		t.Fatalf("inspect of generated encrypted key: %+v", again)
	}
}

func TestInspectOpenSSHPlain(t *testing.T) {
	data := readFixture(t, "ed25519_openssh")
	k := mustInspect(t, data)
	if k.Type != "ed25519" || k.Source != "openssh" || k.Comment != "fixture@test" || k.Format != "openssh" || k.Encrypted {
		t.Fatalf("unexpected: %+v", k)
	}
	if !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected: %+v", k)
	}
	if !bytes.Equal(k.Private, data) {
		t.Fatal("private bytes must be the original file")
	}
}

func TestInspectOpenSSHEncryptedWithoutPassphrase(t *testing.T) {
	plain := mustInspect(t, readFixture(t, "ed25519_openssh"))
	enc := encryptedOpenSSH(t, "pw")
	k := mustInspect(t, enc)
	if !k.Encrypted || k.Format != "openssh" || k.Type != "ed25519" {
		t.Fatalf("unexpected: %+v", k)
	}
	if k.Fingerprint != plain.Fingerprint {
		t.Fatalf("fingerprint %q, want %q", k.Fingerprint, plain.Fingerprint)
	}
	if !strings.HasPrefix(k.PublicLine, "ssh-ed25519 ") {
		t.Fatalf("public line %q", k.PublicLine)
	}
	if !bytes.Equal(k.Private, enc) {
		t.Fatal("encrypted key must be stored as is")
	}
}

func TestInspectPEMRSA(t *testing.T) {
	data := readFixture(t, "rsa_pkcs1.pem")
	k := mustInspect(t, data)
	if k.Type != "rsa" || k.Format != "pem" || k.Encrypted || !strings.HasPrefix(k.PublicLine, "ssh-rsa ") {
		t.Fatalf("unexpected: %+v", k)
	}
	if !bytes.Equal(k.Private, data) {
		t.Fatal("private bytes must be the original file")
	}
	blk, _ := pem.Decode(data)
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

func TestInspectLegacyPEMNeedsPassphrase(t *testing.T) {
	plain := mustInspect(t, readFixture(t, "rsa_pkcs1.pem"))
	enc := legacyPEM(t, "pw")
	if _, err := Inspect(enc); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
	if _, err := InspectWithPassphrase(enc, []byte("bad")); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("want ErrWrongPassphrase, got %v", err)
	}
	k, err := InspectWithPassphrase(enc, []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if !k.Encrypted || k.Format != "pem" || k.Fingerprint != plain.Fingerprint {
		t.Fatalf("unexpected: %+v", k)
	}
	if !bytes.Equal(k.Private, enc) {
		t.Fatal("legacy PEM must stay encrypted in its original bytes")
	}
	if _, err := Signer(k, nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("want ErrPassphraseRequired, got %v", err)
	}
	if _, err := Signer(k, []byte("pw")); err != nil {
		t.Fatal(err)
	}
}

func TestInspectPPKEncrypted(t *testing.T) {
	data := readFixture(t, "v3.ppk")
	k := mustInspect(t, data)
	if !k.Encrypted || k.Format != "ppk" || k.Source != "ppk" || k.Type != "rsa" || k.Comment != "a@b" {
		t.Fatalf("unexpected: %+v", k)
	}
	if !strings.HasSuffix(k.PublicLine, " a@b") {
		t.Fatalf("public line %q", k.PublicLine)
	}
	if !bytes.Equal(k.Private, data) {
		t.Fatal("encrypted PPK must be stored as is")
	}
	plain := mustInspect(t, readFixture(t, "v3-plain.ppk"))
	if plain.Encrypted || plain.Fingerprint != k.Fingerprint {
		t.Fatal("encrypted and plain v3 fixtures should hold the same key")
	}
}

func TestInspectPPKv2(t *testing.T) {
	k := mustInspect(t, readFixture(t, "v2.ppk"))
	if k.Source != "ppk" || k.Format != "ppk" || k.Encrypted || k.Type != "rsa" || k.Comment != "a@b" || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected: %+v", k)
	}
}

func TestInspectECDSAUnsupported(t *testing.T) {
	if _, err := Inspect(readFixture(t, "ecdsa_openssh")); !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("want ErrUnsupportedKeyType, got %v", err)
	}
	raw, err := ssh.ParseRawPrivateKey(readFixture(t, "ecdsa_openssh"))
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(raw, "", []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(pem.EncodeToMemory(blk)); !errors.Is(err, ErrUnsupportedKeyType) {
		t.Fatalf("encrypted ecdsa: want ErrUnsupportedKeyType, got %v", err)
	}
}

func TestInspectGarbage(t *testing.T) {
	if _, err := Inspect([]byte("not a key")); err == nil {
		t.Fatal("expected error")
	}
}

func TestSignerPlainAllFormats(t *testing.T) {
	for _, name := range []string{"ed25519_openssh", "rsa_pkcs1.pem", "v2.ppk", "v3-plain.ppk"} {
		k := mustInspect(t, readFixture(t, name))
		s, err := Signer(k, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ssh.FingerprintSHA256(s.PublicKey()) != k.Fingerprint {
			t.Fatalf("%s: signer fingerprint differs", name)
		}
	}
}

func TestSignerCorrectPassphrase(t *testing.T) {
	cases := map[string]struct {
		data []byte
		pw   string
	}{
		"openssh": {encryptedOpenSSH(t, "pw"), "pw"},
		"ppk":     {readFixture(t, "v3.ppk"), ppkPassphrase},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k := mustInspect(t, c.data)
			s, err := Signer(k, []byte(c.pw))
			if err != nil {
				t.Fatal(err)
			}
			if ssh.FingerprintSHA256(s.PublicKey()) != k.Fingerprint {
				t.Fatal("signer fingerprint differs")
			}
			if !bytes.Equal(k.Private, c.data) {
				t.Fatal("Signer must not modify the stored bytes")
			}
		})
	}
}

func TestSignerWrongPassphrase(t *testing.T) {
	for name, data := range map[string][]byte{
		"openssh": encryptedOpenSSH(t, "pw"),
		"ppk":     readFixture(t, "v3.ppk"),
	} {
		t.Run(name, func(t *testing.T) {
			k := mustInspect(t, data)
			if _, err := Signer(k, nil); !errors.Is(err, ErrPassphraseRequired) {
				t.Fatalf("no passphrase: want ErrPassphraseRequired, got %v", err)
			}
			if _, err := Signer(k, []byte("wrong")); !errors.Is(err, ErrWrongPassphrase) {
				t.Fatalf("want ErrWrongPassphrase, got %v", err)
			}
		})
	}
}

func TestExportEncryptedKeepsOriginalBytes(t *testing.T) {
	cases := map[string]struct {
		data []byte
		ext  string
	}{
		"openssh": {encryptedOpenSSH(t, "pw"), ""},
		"ppk":     {readFixture(t, "v3.ppk"), ".ppk"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			k := mustInspect(t, c.data)
			got, ext := PrivateOriginal(k)
			if !bytes.Equal(got, c.data) || ext != c.ext {
				t.Fatalf("original export changed bytes or ext %q", ext)
			}
			if _, err := PrivateOpenSSH(k); !errors.Is(err, ErrKeyEncrypted) {
				t.Fatalf("OpenSSH export of encrypted key: %v", err)
			}
			if _, err := PrivatePPK(k); !errors.Is(err, ErrKeyEncrypted) {
				t.Fatalf("PPK export of encrypted key: %v", err)
			}
		})
	}
	k, err := InspectWithPassphrase(legacyPEM(t, "pw"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ext := PrivateOriginal(k); ext != ".pem" {
		t.Fatalf("pem ext %q", ext)
	}
}

func TestExportPlainConverts(t *testing.T) {
	ed, err := Generate("ed25519", "rt@test", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse fixtures instead of a second slow 4096-bit generation.
	rsaKey := mustInspect(t, readFixture(t, "rsa_pkcs1.pem"))
	rsaKey.Comment = "rt@test"
	ppkKey := mustInspect(t, readFixture(t, "v3-plain.ppk"))
	ppkKey.Comment = "rt@test"
	for name, k := range map[string]model.Key{"ed25519": ed, "pem": rsaKey, "ppk": ppkKey} {
		t.Run(name, func(t *testing.T) {
			ppk, err := PrivatePPK(k)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(ppk), "PuTTY-User-Key-File-3: ") || !strings.Contains(string(ppk), "Encryption: none") {
				t.Fatalf("not an unencrypted v3 file:\n%s", ppk)
			}
			got := mustInspect(t, ppk)
			if got.Fingerprint != k.Fingerprint || got.Source != "ppk" || got.Comment != "rt@test" {
				t.Fatalf("ppk round trip mismatch: %+v vs %+v", got, k)
			}
			op, err := PrivateOpenSSH(k)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(op), "-----BEGIN OPENSSH PRIVATE KEY-----") {
				t.Fatalf("not OpenSSH:\n%s", op)
			}
			got = mustInspect(t, op)
			if got.Fingerprint != k.Fingerprint || got.Encrypted || got.Comment != "rt@test" {
				t.Fatalf("openssh round trip mismatch: %+v vs %+v", got, k)
			}
		})
	}
}

func TestExportOpenSSHOriginalIsUnchanged(t *testing.T) {
	data := readFixture(t, "ed25519_openssh")
	k := mustInspect(t, data)
	got, err := PrivateOpenSSH(k)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) || PublicLine(k) != k.PublicLine {
		t.Fatal("helpers must return stored values")
	}
}

func TestGenerateUsesCryptoRand(t *testing.T) {
	a, err := Generate("ed25519", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("ed25519", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("two generated keys share a fingerprint")
	}
}

func TestInspectCRLFInput(t *testing.T) {
	for _, name := range []string{"v3-plain.ppk", "v3.ppk", "ed25519_openssh"} {
		data := strings.ReplaceAll(string(readFixture(t, name)), "\n", "\r\n")
		k, err := Inspect([]byte(data))
		if err != nil {
			t.Errorf("%s with CRLF: %v", name, err)
			continue
		}
		var pw []byte
		if k.Encrypted {
			pw = []byte(ppkPassphrase)
		}
		if _, err := Signer(k, pw); err != nil {
			t.Errorf("%s with CRLF signer: %v", name, err)
		}
	}
}

func TestCommentSanitized(t *testing.T) {
	k, err := Generate("ed25519", "x\nssh-ed25519 AAAA\t\x7f", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(k.PublicLine, "\r\n\t\x7f") || strings.ContainsAny(k.Comment, "\r\n\t\x7f") {
		t.Fatalf("control characters survived: %q / %q", k.PublicLine, k.Comment)
	}
}

func TestParseInvalidRSA(t *testing.T) {
	blk, _ := pem.Decode(readFixture(t, "rsa_pkcs1.pem"))
	orig, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	orig.Primes[0].Add(orig.Primes[0], big.NewInt(2)) // break N = p*q
	if _, err := signerFromRaw(orig); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
}

func TestSignerRejectsMismatchedPublicKey(t *testing.T) {
	k := mustInspect(t, readFixture(t, "ed25519_openssh"))
	other := mustInspect(t, readFixture(t, "rsa_pkcs1.pem"))
	k.Fingerprint = other.Fingerprint
	if _, err := Signer(k, nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("want ErrInvalidKey, got %v", err)
	}
}
