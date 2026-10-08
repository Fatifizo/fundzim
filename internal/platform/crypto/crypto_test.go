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
