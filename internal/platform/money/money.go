// Package money implements FundZim's exact money type (docs/MONEY.md, ADR-005, ADR-018).
//
// An amount is an int64 count of minor units plus a Currency. There is no floating point anywhere in this
// package. Operations across currencies fail with ErrCurrencyMismatch; arithmetic overflow fails with
// ErrOverflow. Nothing panics on bad input and nothing converts currencies.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrOverflow         = errors.New("money: overflow")
	ErrUnknownCurrency  = errors.New("money: unknown currency")
	ErrInvalidAmount    = errors.New("money: invalid amount")
	ErrInvalidWeights   = errors.New("money: invalid allocation weights")
)

// Currency is a validated currency. Construct it only through a Registry.
type Currency struct {
	code       string
	minorUnits uint8
	symbol     string
}

func (c Currency) Code() string      { return c.code }
func (c Currency) MinorUnits() uint8 { return c.minorUnits }
func (c Currency) Symbol() string    { return c.symbol }
func (c Currency) IsZero() bool      { return c.code == "" }
func (c Currency) String() string    { return c.code }

// Registry resolves currency codes. In production it is loaded from app.currencies at startup; Default
// mirrors the seeded rows (MONEY.md §2) for tests and tools.
type Registry struct{ byCode map[string]Currency }

// CurrencyDef defines one registry entry.
type CurrencyDef struct {
	Code       string
	MinorUnits uint8
	Symbol     string
}

// NewRegistry builds a registry. minorUnits must be 0..4 and codes three upper-case letters.
func NewRegistry(entries ...CurrencyDef) (*Registry, error) {
	r := &Registry{byCode: map[string]Currency{}}
	for _, e := range entries {
		if !codeRe.MatchString(e.Code) || e.MinorUnits > 4 {
			return nil, fmt.Errorf("money: invalid currency definition %q", e.Code)
		}
		r.byCode[e.Code] = Currency{code: e.Code, minorUnits: e.MinorUnits, symbol: e.Symbol}
	}
	return r, nil
}

var codeRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Default returns the registry matching the seeded currencies (USD, ZWG).
func Default() *Registry {
	r, _ := NewRegistry(CurrencyDef{"USD", 2, "US$"}, CurrencyDef{"ZWG", 2, "ZiG"})
	return r
}

// Lookup returns the currency for code.
func (r *Registry) Lookup(code string) (Currency, error) {
	c, ok := r.byCode[code]
	if !ok {
		return Currency{}, ErrUnknownCurrency
	}
	return c, nil
}

// Money is an exact amount in one currency.
type Money struct {
	amountMinor int64
	currency    Currency
}

// New returns amountMinor minor units of c.
func New(amountMinor int64, c Currency) Money { return Money{amountMinor: amountMinor, currency: c} }

func (m Money) AmountMinor() int64 { return m.amountMinor }
func (m Money) Currency() Currency { return m.currency }
func (m Money) IsZero() bool       { return m.amountMinor == 0 }
func (m Money) IsPositive() bool   { return m.amountMinor > 0 }
func (m Money) IsNegative() bool   { return m.amountMinor < 0 }

func (m Money) same(o Money) error {
	if m.currency.code != o.currency.code || m.currency.IsZero() {
		return ErrCurrencyMismatch
	}
	return nil
}

// Add returns m + o.
func (m Money) Add(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	s := m.amountMinor + o.amountMinor
	if (o.amountMinor > 0 && s < m.amountMinor) || (o.amountMinor < 0 && s > m.amountMinor) {
		return Money{}, ErrOverflow
	}
	return Money{amountMinor: s, currency: m.currency}, nil
}

// Sub returns m - o.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	d := m.amountMinor - o.amountMinor
	if (o.amountMinor > 0 && d > m.amountMinor) || (o.amountMinor < 0 && d < m.amountMinor) {
		return Money{}, ErrOverflow
	}
	return Money{amountMinor: d, currency: m.currency}, nil
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if m.amountMinor == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{amountMinor: -m.amountMinor, currency: m.currency}, nil
}

// Cmp compares m and o: -1, 0 or +1.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.same(o); err != nil {
		return 0, err
	}
	switch {
	case m.amountMinor < o.amountMinor:
		return -1, nil
	case m.amountMinor > o.amountMinor:
		return 1, nil
	}
	return 0, nil
}

// RoundingMode controls MulBasisPoints.
type RoundingMode int

const (
	HalfUp RoundingMode = iota // away from zero at .5 (MONEY.md §5 default)
	Floor                      // towards negative infinity
	Ceil                       // towards positive infinity
)

// MulBasisPoints returns m × bps / 10 000, rounded once with mode (128-bit intermediate, overflow-checked).
func (m Money) MulBasisPoints(bps int64, mode RoundingMode) (Money, error) {
	if bps < 0 {
		return Money{}, ErrInvalidAmount
	}
	neg := m.amountMinor < 0
	abs := absU(m.amountMinor)
	hi, lo := bits.Mul64(abs, uint64(bps))
	const div = 10_000
	if hi >= div {
		return Money{}, ErrOverflow
	}
	q, r := bits.Div64(hi, lo, div)
	switch mode {
	case HalfUp:
		if r*2 >= div {
			q++
		}
	case Floor:
		if neg && r != 0 {
			q++
		}
	case Ceil:
		if !neg && r != 0 {
			q++
		}
	default:
		return Money{}, ErrInvalidAmount
	}
	v, err := signed(q, neg)
	if err != nil {
		return Money{}, err
	}
	return Money{amountMinor: v, currency: m.currency}, nil
}

// Allocate splits m by weights using the largest-remainder method (MONEY.md §6). Parts sum exactly to
// m; ties go to the lowest index. Negative totals are allocated on the absolute value and negated.
func (m Money) Allocate(weights []int64) ([]Money, error) {
	if len(weights) == 0 {
		return nil, ErrInvalidWeights
	}
	var sum uint64
	for _, w := range weights {
		if w < 0 {
			return nil, ErrInvalidWeights
		}
		var carry uint64
		sum, carry = bits.Add64(sum, uint64(w), 0)
		if carry != 0 {
			return nil, ErrOverflow
		}
	}
	if sum == 0 {
		return nil, ErrInvalidWeights
	}
	neg := m.amountMinor < 0
	total := absU(m.amountMinor)
	parts := make([]uint64, len(weights))
	rems := make([]uint64, len(weights))
	var allocated uint64
	for i, w := range weights {
		hi, lo := bits.Mul64(total, uint64(w))
		q, r := bits.Div64(hi, lo, sum) // hi < sum always, since w ≤ sum
		parts[i], rems[i] = q, r
		allocated += q
	}
	leftover := total - allocated
	for leftover > 0 {
		best := -1
		for i := range parts {
			if weights[i] == 0 {
				continue
			}
			if best == -1 || rems[i] > rems[best] {
				best = i
			}
		}
		parts[best]++
		rems[best] = 0
		leftover--
	}
	out := make([]Money, len(parts))
	for i, p := range parts {
		v, err := signed(p, neg)
		if err != nil {
			return nil, err
		}
		out[i] = Money{amountMinor: v, currency: m.currency}
	}
	return out, nil
}

func absU(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1 // safe for MinInt64
	}
	return uint64(v)
}

func signed(u uint64, neg bool) (int64, error) {
	if neg {
		if u > uint64(math.MaxInt64)+1 {
			return 0, ErrOverflow
		}
		if u == uint64(math.MaxInt64)+1 {
			return math.MinInt64, nil
		}
		return -int64(u), nil
	}
	if u > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(u), nil
}

// Parse converts a canonical decimal string ("10", "10.5", "10.50") to exact minor units (MONEY.md §7).
// It never rounds: more decimal places than the currency allows is an error.
func Parse(s string, c Currency) (Money, error) {
	if c.IsZero() {
		return Money{}, ErrUnknownCurrency
	}
	intPart, frac, hasDot := strings.Cut(s, ".")
	if len(intPart) < 1 || len(intPart) > 15 || !allDigits(intPart) {
		return Money{}, ErrInvalidAmount
	}
	if hasDot && (len(frac) < 1 || len(frac) > int(c.minorUnits) || !allDigits(frac)) {
		return Money{}, ErrInvalidAmount
	}
	frac += strings.Repeat("0", int(c.minorUnits)-len(frac))
	v, err := strconv.ParseInt(intPart+frac, 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	return Money{amountMinor: v, currency: c}, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// DecimalString renders the exact decimal value ("100.00", "-0.05").
func (m Money) DecimalString() string {
	neg := m.amountMinor < 0
	digits := strconv.FormatUint(absU(m.amountMinor), 10)
	n := int(m.currency.minorUnits)
	if n > 0 {
		if len(digits) <= n {
			digits = strings.Repeat("0", n-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-n] + "." + digits[len(digits)-n:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// String renders e.g. "USD 100.00".
func (m Money) String() string { return m.currency.code + " " + m.DecimalString() }

// LogValue keeps the log format consistent with the API.
func (m Money) LogValue() slog.Value {
	return slog.GroupValue(slog.String("amount_minor", strconv.FormatInt(m.amountMinor, 10)),
		slog.String("currency", m.currency.code))
}

type wire struct {
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// amountMinorRe is the wire format (MONEY.md §3.3): no leading zeros, no sign other than '-', ≤ 19 digits.
var amountMinorRe = regexp.MustCompile(`^(0|-?[1-9][0-9]{0,18})$`)

// MarshalJSON produces {"amount_minor":"<int>","currency":"<code>"}.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(wire{AmountMinor: strconv.FormatInt(m.amountMinor, 10), Currency: m.currency.code})
}

// Unmarshal parses the wire format using reg. JSON numbers, decimals, exponents, whitespace, '+' and
// leading zeros are rejected.
func Unmarshal(data []byte, reg *Registry) (Money, error) {
	var raw struct {
		AmountMinor json.RawMessage `json:"amount_minor"`
		Currency    string          `json:"currency"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Money{}, ErrInvalidAmount
	}
	var s string
	if err := json.Unmarshal(raw.AmountMinor, &s); err != nil { // a JSON number fails here
		return Money{}, ErrInvalidAmount
	}
	if !amountMinorRe.MatchString(s) {
		return Money{}, ErrInvalidAmount
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	c, err := reg.Lookup(raw.Currency)
	if err != nil {
		return Money{}, err
	}
	return Money{amountMinor: v, currency: c}, nil
}
