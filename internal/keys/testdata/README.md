# Test fixtures

All keys here are throwaway test keys. Never use them anywhere real.

| File | Origin | Passphrase |
|---|---|---|
| `ed25519_openssh` | self-generated (throwaway Go program), OpenSSH format, comment `fixture@test` | none |
| `rsa_pkcs1.pem` | self-generated, 2048-bit, PKCS#1 `RSA PRIVATE KEY` | none |
| `ecdsa_openssh` | self-generated, P-256, OpenSSH format (unsupported-type test) | none |
| `v2.ppk` | PPK v2, RSA 512-bit, comment `a@b`; copied from `github.com/kayrus/putty@v1.0.5` `putty_test.go` (`keyContent`, Apache-2.0) | none |
| `v3-plain.ppk` | PPK v3, RSA 2048, from `kayrus/putty@v1.0.5` `putty_test.go` (Apache-2.0) | none |
| `v3.ppk` | PPK v3, RSA 2048, aes256-cbc + Argon2id, from `kayrus/putty@v1.0.5` `putty_test.go` (Apache-2.0) | `testkey` (not `test`: that is what the source uses) |

puttygen is not installed here, so the PPK files come from the kayrus/putty test suite (originally made with puttygen).
