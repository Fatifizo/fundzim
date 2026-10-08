package money

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

var reg = Default()

func usd(t testing.TB) Currency {
	c, err := reg.Lookup("USD")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func zwg(t testing.TB) Currency {
	c, err := reg.Lookup("ZWG")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLookupUnknownCurrency(t *testing.T) {
	if _, err := reg.Lookup("ZWL"); !errors.Is(err, ErrUnknownCurrency) {
		t.Fatal("unknown currency accepted")
	}
}

func TestCurrencyMismatchNeverConverts(t *testing.T) {
	a, b := New(100, usd(t)), New(100, zwg(t))
	if _, err := a.Add(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Error("Add across currencies")
	}
	if _, err := a.Sub(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Error("Sub across currencies")
	}
	if _, err := a.Cmp(b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Error("Cmp across currencies")
	}
}

func TestOverflowIsAnError(t *testing.T) {
	c := usd(t)
	if _, err := New(math.MaxInt64, c).Add(New(1, c)); !errors.Is(err, ErrOverflow) {
		t.Error("Add overflow")
	}
	if _, err := New(math.MinInt64, c).Sub(New(1, c)); !errors.Is(err, ErrOverflow) {
		t.Error("Sub overflow")
	}
	if _, err := New(math.MinInt64, c).Neg(); !errors.Is(err, ErrOverflow) {
		t.Error("Neg overflow")
	}
	if _, err := New(math.MaxInt64, c).MulBasisPoints(20_000, HalfUp); !errors.Is(err, ErrOverflow) {
		t.Error("MulBasisPoints overflow")
	}
}

// MONEY.md §5 worked examples.
func TestMulBasisPointsWorkedExamples(t *testing.T) {
	c := usd(t)
	for _, tc := range []struct {
		amount, bps, want int64
		mode              RoundingMode
	}{
		{5000, 500, 250, HalfUp},
		{333, 500, 17, HalfUp}, // 16.65 → 17
		{333, 500, 16, Floor},
		{333, 500, 17, Ceil},
		{-333, 500, -17, HalfUp}, // away from zero
		{-333, 500, -17, Floor},
		{-333, 500, -16, Ceil},
		{1, 5000, 1, HalfUp}, // 0.5 → 1
		{0, 500, 0, HalfUp},
	} {
		got, err := New(tc.amount, c).MulBasisPoints(tc.bps, tc.mode)
		if err != nil || got.AmountMinor() != tc.want {
			t.Errorf("%d × %dbp mode %d = %d (%v), want %d", tc.amount, tc.bps, tc.mode, got.AmountMinor(), err, tc.want)
		}
	}
	if _, err := New(1, c).MulBasisPoints(-1, HalfUp); err == nil {
		t.Error("negative bps accepted")
	}
}

// MONEY.md §6 worked examples.
func TestAllocateWorkedExamples(t *testing.T) {
	c := usd(t)
	check := func(total int64, w []int64, want []int64) {
		t.Helper()
		parts, err := New(total, c).Allocate(w)
		if err != nil {
			t.Fatal(err)
		}
		for i := range want {
			if parts[i].AmountMinor() != want[i] {
				t.Fatalf("Allocate(%d,%v) = %v, want %v", total, w, parts, want)
			}
		}
	}
	check(10000, []int64{1, 1, 1}, []int64{3334, 3333, 3333})
	check(5, []int64{70, 30}, []int64{4, 1})
	check(-10000, []int64{1, 1, 1}, []int64{-3334, -3333, -3333})
	check(7, []int64{0, 1, 1}, []int64{0, 4, 3})
	for _, w := range [][]int64{nil, {}, {0, 0}, {1, -1}} {
		if _, err := New(10, c).Allocate(w); !errors.Is(err, ErrInvalidWeights) {
			t.Errorf("weights %v accepted", w)
		}
	}
}

// Property: allocation parts always sum exactly to the total and differ from the exact share by < 1.
func TestAllocateProperty(t *testing.T) {
	c := usd(t)
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20_000; i++ {
		total := r.Int64N(1<<53) - (1 << 52)
		n := 1 + r.IntN(8)
		w := make([]int64, n)
		for j := range w {
			w[j] = r.Int64N(10_000)
		}
		w[r.IntN(n)]++ // at least one non-zero weight
		parts, err := New(total, c).Allocate(w)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, p := range parts {
			sum += p.AmountMinor()
		}
		if sum != total {
			t.Fatalf("sum %d != total %d (weights %v)", sum, total, w)
		}
	}
}

// Property: Add/Sub round-trip and HalfUp is within half a unit of the exact value.
func TestArithmeticProperty(t *testing.T) {
	c := usd(t)
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 20_000; i++ {
		a, b := New(r.Int64N(1<<60)-(1<<59), c), New(r.Int64N(1<<60)-(1<<59), c)
		s, err := a.Add(b)
		if err != nil {
			t.Fatal(err)
		}
		back, err := s.Sub(b)
		if err != nil || back != a {
			t.Fatalf("(a+b)-b != a for %v %v", a, b)
		}
		amt := r.Int64N(1 << 40)
		bps := r.Int64N(10_001)
		f, err := New(amt, c).MulBasisPoints(bps, HalfUp)
		if err != nil {
			t.Fatal(err)
		}
		// |f*10000 - amt*bps| <= 5000 (half a unit, in 1/10000ths)
		diff := f.AmountMinor()*10_000 - amt*bps
		if diff < -5000 || diff > 5000 {
			t.Fatalf("HalfUp(%d×%d) = %d off by %d", amt, bps, f.AmountMinor(), diff)
		}
	}
}

func TestParse(t *testing.T) {
	c := usd(t)
	ok := map[string]int64{"0": 0, "10": 1000, "10.5": 1050, "10.50": 1050, "0.01": 1, "999999999999999.99": 99999999999999999}
	for s, want := range ok {
		m, err := Parse(s, c)
		if err != nil || m.AmountMinor() != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", s, m.AmountMinor(), err, want)
		}
	}
	for _, s := range []string{"", ".5", "10.", "10.005", "1,000", "-1", "+1", "1e3", " 1", "1 ", "١٠", "1.2.3", "1234567890123456"} {
		if _, err := Parse(s, c); err == nil {
			t.Errorf("Parse(%q) accepted", s)
		}
	}
}

func TestDecimalString(t *testing.T) {
	c := usd(t)
	for amt, want := range map[int64]string{0: "0.00", 5: "0.05", 100: "1.00", -5: "-0.05", 123456: "1234.56",
		math.MaxInt64: "92233720368547758.07", math.MinInt64: "-92233720368547758.08"} {
		if got := New(amt, c).DecimalString(); got != want {
			t.Errorf("DecimalString(%d) = %s, want %s", amt, got, want)
		}
	}
}

func TestJSONRoundTripAndStrictness(t *testing.T) {
	c := usd(t)
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64} {
		b, err := json.Marshal(New(v, c))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Unmarshal(b, reg)
		if err != nil || got.AmountMinor() != v || got.Currency().Code() != "USD" {
			t.Fatalf("round trip %d: %s → %v %v", v, b, got, err)
		}
	}
	for _, bad := range []string{
		`{"amount_minor":100,"currency":"USD"}`,                   // JSON number
		`{"amount_minor":"1.00","currency":"USD"}`,                // decimal
		`{"amount_minor":"1e3","currency":"USD"}`,                 // exponent
		`{"amount_minor":"+1","currency":"USD"}`,                  // plus sign
		`{"amount_minor":"007","currency":"USD"}`,                 // leading zeros
		`{"amount_minor":" 1","currency":"USD"}`,                  // whitespace
		`{"amount_minor":"-0","currency":"USD"}`,                  // negative zero
		`{"amount_minor":"9223372036854775808","currency":"USD"}`, // overflow
		`{"amount_minor":"1","currency":"XXX"}`,                   // unknown currency
		`{"amount_minor":"1","currency":"USD","x":1}`,             // unknown field
	} {
		if _, err := Unmarshal([]byte(bad), reg); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func FuzzParseNeverPanicsOrRounds(f *testing.F) {
	for _, s := range []string{"1", "10.50", "1e3", "0.001", "-1", "99999999999999.99"} {
		f.Add(s)
	}
	c := Default()
	usdC, _ := c.Lookup("USD")
	f.Fuzz(func(t *testing.T, s string) {
		m, err := Parse(s, usdC)
		if err != nil {
			return
		}
		// Accepted values must render back to an equal value (no rounding happened).
		back, err := Parse(m.DecimalString(), usdC)
		if err != nil || back != m {
			t.Fatalf("Parse(%q) = %v, re-parse of %q gave %v %v", s, m, m.DecimalString(), back, err)
		}
	})
}

func FuzzUnmarshalNeverPanics(f *testing.F) {
	f.Add([]byte(`{"amount_minor":"100","currency":"USD"}`))
	f.Add([]byte(`{"amount_minor":100}`))
	r := Default()
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Unmarshal(b, r)
		if err != nil {
			return
		}
		out, _ := json.Marshal(m)
		m2, err := Unmarshal(out, r)
		if err != nil || m2 != m {
			t.Fatalf("round trip changed value: %s → %s", b, out)
		}
	})
}
