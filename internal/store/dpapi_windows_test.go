package store

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("dek-bytes-0123456789")
	enc, err := dpapiProtect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("protected blob equals plaintext")
	}
	dec, err := dpapiUnprotect(enc)
	if err != nil || !bytes.Equal(dec, plain) {
		t.Fatalf("got %q %v", dec, err)
	}
	enc[len(enc)/2] ^= 0xff
	if _, err := dpapiUnprotect(enc); err == nil {
		t.Fatal("corrupted blob must fail")
	}
}
