package passwords

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test parameters are deliberately cheap; production defaults are set in config (64 MiB, t=3).
var testParams = Params{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1}

func TestHashAndVerify(t *testing.T) {
	h := NewHasher(testParams, 2)
	ctx := context.Background()
	phc, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8192,t=1,p=1$") || strings.Contains(phc, "horse") {
		t.Fatalf("unexpected PHC %q", phc)
	}
	if ok, err := h.Verify(ctx, phc, "correct horse battery staple"); !ok || err != nil {
		t.Fatal("correct password rejected")
	}
	if ok, _ := h.Verify(ctx, phc, "correct horse battery stapler"); ok {
		t.Fatal("wrong password accepted")
	}
	other, _ := h.Hash(ctx, "correct horse battery staple")
	if other == phc {
		t.Fatal("salts must be unique")
	}
	if _, err := h.Verify(ctx, "$2a$10$bcrypt", "x"); err == nil {
		t.Fatal("non-argon2id hash accepted")
	}
}

func TestUnicodeNormalisation(t *testing.T) {
	h := NewHasher(testParams, 1)
	ctx := context.Background()
	phc, _ := h.Hash(ctx, "café-passphrase-ñ") // precomposed é
	if ok, _ := h.Verify(ctx, phc, "café-passphrase-ñ"); !ok {
		t.Fatal("NFKC-equivalent password rejected")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak := NewHasher(testParams, 1)
	strong := NewHasher(Params{MemoryKiB: 16 * 1024, Iterations: 2, Parallelism: 1}, 1)
	phc, _ := weak.Hash(context.Background(), "some long passphrase")
	if !strong.NeedsRehash(phc) || weak.NeedsRehash(phc) {
		t.Fatal("rehash detection wrong")
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	h := NewHasher(testParams, 1)
	h.sem <- struct{}{} // occupy the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "x"); err != ErrBusy {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	<-h.sem
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = h.Hash(context.Background(), "parallel passphrase") }()
	}
	wg.Wait()
}

func TestPolicy(t *testing.T) {
	p := Policy{MinLength: 12, MaxLength: 256}
	cases := map[string][]string{
		"short":                          {TooShort},
		"password1234":                   {Common},
		"qwertyuiop12":                   {Common},
		"aaaaaaaaaaaaaaaa":               {RepetitiveText},
		strings.Repeat("ab12", 70):       {TooLong},
		"tendai.moyo-secret!":            {ContainsSelf},
		"MyFundZimAccount!":              {Common},
		"bad\x00control-char-passphrase": {InvalidChars},
	}
	for pw, want := range cases {
		got := p.Check(pw, "tendai.moyo@example.invalid", "tendai.moyo", "Tendai Moyo")
		for _, w := range want {
			found := false
			for _, g := range got {
				found = found || g == w
			}
			if !found {
				t.Errorf("%q: got %v, want %v", pw, got, want)
			}
		}
	}
	for _, ok := range []string{"correct horse battery staple", "Kurima kwakanaka mangwanani 🌅", "mhoro-shamwari-2026-nzira"} {
		if v := p.Check(ok, "tendai.moyo"); len(v) != 0 {
			t.Errorf("%q rejected: %v", ok, v)
		}
	}
}
