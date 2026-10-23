// Package crypto holds FundZim's application-level cryptographic helpers. It only composes standard,
// reviewed primitives from the Go standard library (AES-256-GCM, HMAC-SHA-256, SHA-256, crypto/rand);
// it implements no custom cryptography.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// AEAD encrypts small C3/C4 values (e.g. TOTP secrets) with AES-256-GCM. The key ID is stored next to the
// ciphertext. Rotation (Stage 18, ADR-050): new ciphertexts always use the active key; Open also accepts the previous
// keys (overlap) until every row has been re-encrypted, so a rotation never makes existing data unreadable. GCM
// authenticates, so trying a previous key on a ciphertext of another key fails cleanly; it never decrypts wrongly.
type AEAD struct {
	keyID    string
	gcm      cipher.AEAD
	previous []cipher.AEAD
}

// NewAEAD builds an AEAD from a 32-byte hex active key and optional previous keys (rotation overlap).
func NewAEAD(keyID, hexKey string, previousHexKeys ...string) (*AEAD, error) {
	gcm, err := newGCM(hexKey)
	if err != nil {
		return nil, err
	}
	a := &AEAD{keyID: keyID, gcm: gcm}
	for _, p := range previousHexKeys {
		if strings.EqualFold(p, hexKey) {
			return nil, errors.New("crypto: a previous field encryption key equals the active key")
		}
		g, err := newGCM(p)
		if err != nil {
			return nil, err
		}
		a.previous = append(a.previous, g)
	}
	return a, nil
}

func newGCM(hexKey string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("crypto: field encryption key must be 32 bytes of hex")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// KeyID identifies the key used for new ciphertexts.
func (a *AEAD) KeyID() string { return a.keyID }

// PreviousKeys is the number of previous keys still accepted by Open (0 when no rotation is in progress).
func (a *AEAD) PreviousKeys() int { return len(a.previous) }

// Seal encrypts plaintext bound to aad (e.g. "mfa_methods:<id>") under the active key, returning nonce||ciphertext.
func (a *AEAD) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// Open decrypts nonce||ciphertext produced by Seal with the same aad, under the active key or a previous one.
func (a *AEAD) Open(sealed, aad []byte) ([]byte, error) {
	pt, _, err := a.OpenWithStatus(sealed, aad)
	return pt, err
}

// OpenWithStatus is Open that also reports whether the ciphertext used a previous key (it needs re-encryption).
func (a *AEAD) OpenWithStatus(sealed, aad []byte) (plaintext []byte, previous bool, err error) {
	n := a.gcm.NonceSize()
	if len(sealed) < n {
		return nil, false, errors.New("crypto: ciphertext too short")
	}
	pt, err := a.gcm.Open(nil, sealed[:n], sealed[n:], aad)
	if err == nil {
		return pt, false, nil
	}
	for _, g := range a.previous {
		if pt, perr := g.Open(nil, sealed[:n], sealed[n:], aad); perr == nil {
			return pt, true, nil
		}
	}
	return nil, false, err
}

// Keyed computes HMAC-SHA-256 blind indexes and code hashes with a server key, so stored hashes of
// low-entropy values (OTP codes, phone numbers, recovery codes) cannot be brute-forced offline without it.
type Keyed struct {
	keyID string
	key   []byte
}

// NewKeyed builds a keyed hasher from a 32-byte hex key.
func NewKeyed(keyID, hexKey string) (*Keyed, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("crypto: blind index key must be 32 bytes of hex")
	}
	return &Keyed{keyID: keyID, key: key}, nil
}

// KeyID identifies the HMAC key.
func (k *Keyed) KeyID() string { return k.keyID }

// Sum returns HMAC-SHA-256(key, domain || 0x00 || parts...) — the domain separates uses of the key.
func (k *Keyed) Sum(domain string, parts ...string) []byte {
	m := hmac.New(sha256.New, k.key)
	m.Write([]byte(domain))
	for _, p := range parts {
		m.Write([]byte{0})
		m.Write([]byte(p))
	}
	return m.Sum(nil)
}

// Token is a random secret handed to a client (session, email link, MFA challenge). Only Hash(token) is stored.
type Token string

// NewToken returns 32 random bytes, base64url-encoded without padding (43 characters).
func NewToken() (Token, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

// Hash returns SHA-256 of the token string. Tokens have 256 bits of entropy, so an unkeyed hash is
// sufficient for storage (a database leak does not reveal usable tokens).
func HashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

// ValidTokenFormat reports whether s looks like a NewToken value (cheap rejection before any lookup).
func ValidTokenFormat(s string) bool {
	if len(s) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil
}

// Equal compares two byte strings in constant time.
func Equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// NumericCode returns a uniformly random decimal code of n digits (e.g. a 6-digit SMS code).
func NumericCode(n int) (string, error) {
	if n < 4 || n > 10 {
		return "", errors.New("crypto: code length out of range")
	}
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", n, v), nil
}

// RecoveryCode returns a 10-character code from an unambiguous base32 alphabet, grouped as xxxxx-xxxxx
// (50 bits of entropy).
func RecoveryCode() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // 31 symbols; no 0/o/1/l/i
	b := make([]byte, 10)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b[:5]) + "-" + string(b[5:]), nil
}
