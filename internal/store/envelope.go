package store

import (
	"encoding/json"
	"fmt"
)

const envelopeVersion = 1

// Wrap kinds for the data-encryption key.
const (
	WrapDPAPI    = "dpapi"
	WrapPassword = "password"
)

// Envelope is the on-disk vault format. Byte slices are base64 in JSON.
type Envelope struct {
	Version  int        `json:"version"`
	Wrap     string     `json:"wrap"`
	KDF      *KDFParams `json:"kdf,omitempty"`
	DEKNonce []byte     `json:"dek_nonce,omitempty"`
	DEKEnc   []byte     `json:"dek_enc"`
	Nonce    []byte     `json:"nonce"`
	Data     []byte     `json:"data"`
}

// MarshalEnvelope serializes the envelope to JSON.
func MarshalEnvelope(e Envelope) ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope parses and validates an envelope; problems wrap ErrCorrupt.
func UnmarshalEnvelope(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if e.Version != envelopeVersion {
		return Envelope{}, fmt.Errorf("%w: unknown version %d", ErrCorrupt, e.Version)
	}
	switch e.Wrap {
	case WrapDPAPI:
	case WrapPassword:
		if e.KDF == nil {
			return Envelope{}, fmt.Errorf("%w: missing kdf", ErrCorrupt)
		}
	default:
		return Envelope{}, fmt.Errorf("%w: unknown wrap %q", ErrCorrupt, e.Wrap)
	}
	if len(e.Data) == 0 || len(e.Nonce) == 0 {
		return Envelope{}, fmt.Errorf("%w: missing data or nonce", ErrCorrupt)
	}
	return e, nil
}
