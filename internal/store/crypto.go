// Package store implements the encrypted vault: envelope crypto and persistence.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/argon2"
)

var (
	// ErrWrongPassword is returned when the master password does not unlock the vault.
	ErrWrongPassword = errors.New("store: wrong password")
	// ErrCorrupt is returned when the vault file is damaged or belongs to another user.
	ErrCorrupt = errors.New("store: vault file is corrupt or belongs to another user")
	// ErrNoPassword is returned when locking a vault that only DPAPI protects: nothing could unlock it again.
	ErrNoPassword = errors.New("store: vault has no password")
)

const keyLen = 32

// KDFParams are the Argon2id parameters stored next to the wrapped DEK.
type KDFParams struct {
	Salt      []byte `json:"salt"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
}

// NewKDFParams returns default parameters with a fresh random salt.
func NewKDFParams() KDFParams {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic("store: crypto/rand failed: " + err.Error())
	}
	return KDFParams{Salt: salt, Time: 3, MemoryKiB: 65536, Threads: 4}
}

// DeriveKEK derives a 32-byte key-encryption key from the password with Argon2id.
func DeriveKEK(password []byte, p KDFParams) []byte {
	return argon2.IDKey(password, p.Salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
}

// NewDEK returns a fresh random 32-byte data-encryption key.
func NewDEK() ([]byte, error) {
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keyLen {
		return nil, errors.New("store: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext with AES-256-GCM under a random 12-byte nonce.
func Seal(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, nil), nil
}

// Open decrypts and authenticates ciphertext; a wrong key yields the raw GCM error.
func Open(key, nonce, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("store: invalid nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}
