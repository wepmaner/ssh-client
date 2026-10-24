package store_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"sshclient/internal/store"
)

func pwEnvelope() store.Envelope {
	kdf := store.NewKDFParams()
	return store.Envelope{
		Version: 1, Wrap: "password", KDF: &kdf,
		DEKNonce: []byte("123456789012"), DEKEnc: []byte("dek"),
		Nonce: []byte("abcdefghijkl"), Data: []byte("data"),
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := pwEnvelope()
	b, err := store.MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"version"`, `"wrap"`, `"kdf"`, `"memory_kib"`, `"dek_nonce"`, `"dek_enc"`, `"nonce"`, `"data"`} {
		if !strings.Contains(string(b), f) {
			t.Errorf("missing field %s in %s", f, b)
		}
	}
	got, err := store.UnmarshalEnvelope(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Wrap != "password" || got.KDF == nil ||
		!bytes.Equal(got.KDF.Salt, e.KDF.Salt) || got.KDF.Time != 3 ||
		!bytes.Equal(got.Data, e.Data) || !bytes.Equal(got.Nonce, e.Nonce) ||
		!bytes.Equal(got.DEKEnc, e.DEKEnc) || !bytes.Equal(got.DEKNonce, e.DEKNonce) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestEnvelopeDPAPIWithoutKDF(t *testing.T) {
	e := pwEnvelope()
	e.Wrap, e.KDF = "dpapi", nil
	b, err := store.MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UnmarshalEnvelope(b); err != nil {
		t.Fatal(err)
	}
}

func TestUnmarshalEnvelopeCorrupt(t *testing.T) {
	mut := map[string]func(*store.Envelope){
		"version": func(e *store.Envelope) { e.Version = 2 },
		"wrap":    func(e *store.Envelope) { e.Wrap = "rot13" },
		"data":    func(e *store.Envelope) { e.Data = nil },
		"nonce":   func(e *store.Envelope) { e.Nonce = nil },
		"kdf":     func(e *store.Envelope) { e.KDF = nil },
	}
	for name, f := range mut {
		e := pwEnvelope()
		f(&e)
		b, err := store.MarshalEnvelope(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UnmarshalEnvelope(b); !errors.Is(err, store.ErrCorrupt) {
			t.Errorf("%s: got %v, want ErrCorrupt", name, err)
		}
	}
	if _, err := store.UnmarshalEnvelope([]byte("{not json")); !errors.Is(err, store.ErrCorrupt) {
		t.Errorf("invalid json: got %v", err)
	}
}
