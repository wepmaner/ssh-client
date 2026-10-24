package store

import (
	"encoding/json"
	"errors"
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
		if err := validatePasswordWrap(e); err != nil {
			return Envelope{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
	default:
		return Envelope{}, fmt.Errorf("%w: unknown wrap %q", ErrCorrupt, e.Wrap)
	}
	if len(e.DEKEnc) == 0 {
		return Envelope{}, fmt.Errorf("%w: missing wrapped key", ErrCorrupt)
	}
	if len(e.Data) == 0 || len(e.Nonce) == 0 {
		return Envelope{}, fmt.Errorf("%w: missing data or nonce", ErrCorrupt)
	}
	return e, nil
}

const (
	kdfSaltLen      = 16
	kdfMaxMemoryKiB = 1 << 20 // 1 GiB ceiling against absurd allocations from a damaged file
	dekNonceLen     = 12
)

func validatePasswordWrap(e Envelope) error {
	k := e.KDF
	switch {
	case k == nil:
		return errors.New("missing kdf")
	case len(k.Salt) != kdfSaltLen:
		return errors.New("bad kdf salt")
	case k.Time < 1 || k.Threads < 1:
		return errors.New("bad kdf time or threads")
	case k.MemoryKiB < 8*uint32(k.Threads) || k.MemoryKiB > kdfMaxMemoryKiB:
		return errors.New("bad kdf memory")
	case len(e.DEKNonce) != dekNonceLen:
		return errors.New("bad dek nonce")
	}
	return nil
}
