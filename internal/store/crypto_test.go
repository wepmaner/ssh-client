package store_test

import (
	"bytes"
	"testing"

	"sshclient/internal/store"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	n, c, err := store.Seal(key, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(n) != 12 {
		t.Fatalf("nonce len %d, want 12", len(n))
	}
	p, err := store.Open(key, n, c)
	if err != nil || string(p) != "hello" {
		t.Fatalf("got %q %v", p, err)
	}
}

func TestOpenWithWrongKeyFails(t *testing.T) {
	key, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	n, c, err := store.Seal(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(other, n, c); err == nil {
		t.Fatal("open with wrong key must fail")
	}
}

func TestOpenTamperedCiphertextFails(t *testing.T) {
	key, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	n, c, err := store.Seal(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	c[0] ^= 0xff
	if _, err := store.Open(key, n, c); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestNewDEKLength(t *testing.T) {
	a, err := store.NewDEK()
	if err != nil || len(a) != 32 {
		t.Fatalf("len %d err %v", len(a), err)
	}
	b, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("DEKs must differ")
	}
}

func TestDeriveKEKDeterministic(t *testing.T) {
	p := store.NewKDFParams()
	a := store.DeriveKEK([]byte("pw"), p)
	b := store.DeriveKEK([]byte("pw"), p)
	if !bytes.Equal(a, b) || len(a) != 32 {
		t.Fatal("kek must be stable, 32 bytes")
	}
	if bytes.Equal(a, store.DeriveKEK([]byte("pw2"), p)) {
		t.Fatal("different passwords same kek")
	}
}

func TestKDFParamsDefaults(t *testing.T) {
	p := store.NewKDFParams()
	if p.Time != 3 || p.MemoryKiB != 65536 || p.Threads != 4 || len(p.Salt) != 16 {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if bytes.Equal(p.Salt, store.NewKDFParams().Salt) {
		t.Fatal("salt must be random")
	}
}

func TestSealOpenRejectNon256Key(t *testing.T) {
	for _, n := range []int{16, 24, 31, 33} {
		if _, _, err := store.Seal(make([]byte, n), []byte("x")); err == nil {
			t.Errorf("Seal accepted %d-byte key", n)
		}
		if _, err := store.Open(make([]byte, n), make([]byte, 12), []byte("x")); err == nil {
			t.Errorf("Open accepted %d-byte key", n)
		}
	}
}

func TestOpenRejectsBadNonceLength(t *testing.T) {
	key, err := store.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	_, c, err := store.Seal(key, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(key, make([]byte, 5), c); err == nil {
		t.Fatal("bad nonce length must fail")
	}
}
