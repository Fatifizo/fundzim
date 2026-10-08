// Package passwords hashes and checks passwords (ADR-032, SECURITY §4.1).
//
// Hashing is Argon2id (golang.org/x/crypto/argon2) with a unique 16-byte random salt per password, stored as a
// PHC string ($argon2id$v=19$m=…,t=…,p=…$salt$hash) so parameters can be raised later without a migration:
// NeedsRehash reports hashes made with weaker parameters, and they are upgraded at the next successful login.
// Concurrency is bounded by a semaphore because each hash uses tens of MiB (memory-exhaustion DoS).
//
// Policy follows NIST SP 800-63B style guidance: length-based (minimum configurable, default 12; long
// passwords and any Unicode allowed; NFKC-normalised before hashing), screened against a list of the most
// common passwords and against context words, no composition rules, no periodic expiry.
package passwords

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// Params are Argon2id cost parameters.
type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
}

// Hasher hashes and verifies passwords with bounded concurrency.
type Hasher struct {
	p   Params
	sem chan struct{}
}

// NewHasher returns a hasher allowing maxConcurrent simultaneous hash computations.
func NewHasher(p Params, maxConcurrent int) *Hasher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Hasher{p: p, sem: make(chan struct{}, maxConcurrent)}
}

const saltLen, keyLen = 16, 32

// ErrBusy is returned when the context ends while waiting for a hashing slot.
var ErrBusy = errors.New("passwords: hashing capacity exhausted")

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	}
}

func (h *Hasher) release() { <-h.sem }

// Normalize applies NFKC so the same password typed on different keyboards/IMEs hashes identically.
func Normalize(pw string) string { return norm.NFKC.String(pw) }

// Hash returns the PHC string for pw (normalised first).
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(Normalize(pw)), salt, h.p.Iterations, h.p.MemoryKiB, h.p.Parallelism, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.p.MemoryKiB, h.p.Iterations,
		h.p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type parsed struct {
	p          Params
	salt, hash []byte
}

func parse(phc string) (parsed, error) {
	var out parsed
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return out, errors.New("passwords: not an argon2id PHC string")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return out, errors.New("passwords: unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &out.p.MemoryKiB, &out.p.Iterations, &out.p.Parallelism); err != nil {
		return out, errors.New("passwords: bad parameters")
	}
	var err error
	if out.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return out, errors.New("passwords: bad salt")
	}
	if out.hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(out.hash) == 0 {
		return out, errors.New("passwords: bad hash")
	}
	return out, nil
}

// Verify reports whether pw matches the PHC string, comparing in constant time.
func (h *Hasher) Verify(ctx context.Context, phc, pw string) (bool, error) {
	ph, err := parse(phc)
	if err != nil {
		return false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	key := argon2.IDKey([]byte(Normalize(pw)), ph.salt, ph.p.Iterations, ph.p.MemoryKiB, ph.p.Parallelism, uint32(len(ph.hash)))
	return subtle.ConstantTimeCompare(key, ph.hash) == 1, nil
}

// NeedsRehash reports whether phc was made with parameters weaker than the hasher's.
func (h *Hasher) NeedsRehash(phc string) bool {
	ph, err := parse(phc)
	if err != nil {
		return true
	}
	return ph.p.MemoryKiB < h.p.MemoryKiB || ph.p.Iterations < h.p.Iterations || ph.p.Parallelism < h.p.Parallelism
}

// DummyHash is verified against when an account does not exist, so response time does not reveal it.
func (h *Hasher) DummyHash(ctx context.Context) string {
	s, err := h.Hash(ctx, "dummy-password-for-timing-equalisation")
	if err != nil {
		return "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return s
}

//go:embed data/common-passwords.txt
var commonList string

var common = func() map[string]bool {
	m := make(map[string]bool, 10000)
	sc := bufio.NewScanner(strings.NewReader(commonList))
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			m[strings.ToLower(w)] = true
		}
	}
	return m
}()

// Violation codes returned in error details.
const (
	TooShort       = "TOO_SHORT"
	TooLong        = "TOO_LONG"
	Common         = "COMMONLY_USED"
	ContainsSelf   = "CONTAINS_PERSONAL_INFO"
	RepetitiveText = "TOO_REPETITIVE"
	InvalidChars   = "INVALID_CHARACTERS"
)

// Policy validates new passwords.
type Policy struct {
	MinLength, MaxLength int
}

// Check returns the violated rules (empty when acceptable). context holds personal words (email local part,
// display name) that must not form the password.
func (p Policy) Check(pw string, context ...string) []string {
	n := Normalize(pw)
	length := utf8.RuneCountInString(n)
	var v []string
	if !utf8.ValidString(pw) || strings.ContainsFunc(n, func(r rune) bool { return unicode.IsControl(r) }) {
		return []string{InvalidChars}
	}
	if length < p.MinLength {
		v = append(v, TooShort)
	}
	if length > p.MaxLength {
		v = append(v, TooLong)
	}
	lower := strings.ToLower(n)
	if common[lower] || common[strings.TrimRight(lower, "0123456789!.@#$")] || strings.Contains(lower, "fundzim") {
		v = append(v, Common)
	}
	for _, c := range context {
		c = strings.ToLower(strings.TrimSpace(c))
		if utf8.RuneCountInString(c) >= 4 && strings.Contains(lower, c) {
			v = append(v, ContainsSelf)
			break
		}
	}
	if distinct(lower) < 4 {
		v = append(v, RepetitiveText)
	}
	return v
}

func distinct(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}
