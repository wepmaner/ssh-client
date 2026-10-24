package keys

import "golang.org/x/crypto/ssh"

// FingerprintSHA256 returns the OpenSSH-style fingerprint: "SHA256:" followed
// by the unpadded base64 of the SHA-256 digest of the public key blob.
func FingerprintSHA256(pub ssh.PublicKey) string {
	return ssh.FingerprintSHA256(pub)
}
