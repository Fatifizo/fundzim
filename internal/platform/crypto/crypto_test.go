package crypto

import (
	"bytes"
	"strings"
	"testing"
)

const k1 = "0000000000000000000000000000000000000000000000000000000000000001"
const k2 = "0000000000000000000000000000000000000000000000000000000000000002"

func TestAEADRoundTripAndBinding(t *testing.T) {
	a, err := NewAEAD("local-1", k1)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("mfa_methods:1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("JBSWY3DP")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	if pt, err := a.Open(sealed, []byte("mfa_methods:1")); err != nil || string(pt) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := a.Open(sealed, []byte("mfa_methods:2")); err == nil {
		t.Fatal("ciphertext moved to another row must not decrypt")
	}
	other, _ := NewAEAD("local-2", k2)
	if _, err := other.Open(sealed, []byte("mfa_methods:1")); err == nil {
		t.Fatal("wrong key decrypted")
	}
	if _, err := NewAEAD("x", "short"); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestKeyedDomainSeparation(t *testing.T) {
	k, _ := NewKeyed("bidx-1", k1)
	if bytes.Equal(k.Sum("otp", "123456"), k.Sum("recovery", "123456")) {
		t.Fatal("domains must separate")
	}
	if !bytes.Equal(k.Sum("otp", "a", "b"), k.Sum("otp", "a", "b")) || bytes.Equal(k.Sum("otp", "ab"), k.Sum("otp", "a", "b")) {
		t.Fatal("parts must be framed")
	}
}

func TestTokensAndCodes(t *testing.T) {
	seen := map[Token]bool{}
	for i := 0; i < 1000; i++ {
		tk, err := NewToken()
		if err != nil || !ValidTokenFormat(string(tk)) || seen[tk] {
			t.Fatalf("bad token %q %v", tk, err)
		}
		seen[tk] = true
	}
	if ValidTokenFormat("not a token") || ValidTokenFormat(strings.Repeat("!", 43)) {
		t.Fatal("invalid format accepted")
	}
	for i := 0; i < 1000; i++ {
		c, _ := NumericCode(6)
		if len(c) != 6 || strings.Trim(c, "0123456789") != "" {
			t.Fatalf("bad code %q", c)
		}
		r, _ := RecoveryCode()
		if len(r) != 11 || r[5] != '-' || strings.ContainsAny(r, "01lio") {
			t.Fatalf("bad recovery code %q", r)
		}
	}
	if len(HashToken("x")) != 32 || !Equal(HashToken("x"), HashToken("x")) || Equal(HashToken("x"), HashToken("y")) {
		t.Fatal("hash/equal broken")
	}
}

// Stage 18 (ADR-050): rotation with overlap. Data sealed under the old key stays readable after the rotation, new data
// uses the new key only, a removed key makes its data unreadable, and a ciphertext is never opened under the wrong AAD.
func TestAEADRotationOverlap(t *testing.T) {
	oldKey := strings.Repeat("11", 32)
	newKey := strings.Repeat("22", 32)
	before, err := NewAEAD("field-1", oldKey)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := before.Seal([]byte("totp secret"), []byte("mfa_methods:a"))

	rotated, err := NewAEAD("field-2", newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	pt, previous, err := rotated.OpenWithStatus(ct, []byte("mfa_methods:a"))
	if err != nil || string(pt) != "totp secret" || !previous {
		t.Fatalf("old ciphertext after rotation: %q previous=%v %v", pt, previous, err)
	}
	if _, _, err := rotated.OpenWithStatus(ct, []byte("mfa_methods:b")); err == nil {
		t.Fatal("opened under the wrong AAD")
	}
	fresh, _ := rotated.Seal([]byte("new secret"), []byte("x"))
	if _, err := before.Open(fresh, []byte("x")); err == nil {
		t.Fatal("new data must be sealed under the new key only")
	}
	if pt, previous, err := rotated.OpenWithStatus(fresh, []byte("x")); err != nil || previous || string(pt) != "new secret" {
		t.Fatalf("new ciphertext: %q %v %v", pt, previous, err)
	}
	retired, _ := NewAEAD("field-2", newKey)
	if _, err := retired.Open(ct, []byte("mfa_methods:a")); err == nil {
		t.Fatal("data under a retired key must not open (re-encrypt before retiring a key)")
	}
	if _, err := NewAEAD("field-2", newKey, strings.ToUpper(newKey)); err == nil {
		t.Fatal("a previous key equal to the active key must be refused")
	}
	if _, err := NewAEAD("field-2", newKey, "short"); err == nil {
		t.Fatal("a malformed previous key must be refused")
	}
	if rotated.PreviousKeys() != 1 || retired.PreviousKeys() != 0 {
		t.Fatal("previous key count")
	}
}
