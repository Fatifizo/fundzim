package campaigns

import (
	"context"
	"crypto/rand"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/money"
)

// Content rules (interface-contracts §9): plain text only. The web app renders text escaped and never turns it
// into links; the API still refuses HTML and dangerous URL schemes so stored content is inert everywhere.
var (
	htmlTagRe = regexp.MustCompile(`(?i)<\s*/?\s*[a-z!?][^>]*>`)
	// script schemes anywhere; data: only as a URL ("data:<type>/<subtype>;" or ","), so "Our data: 12 families" is fine
	unsafeLinkRe = regexp.MustCompile(`(?i)\b(java\s*script|vb\s*script)\s*:|\bdata\s*:\s*[a-z]+/[a-z0-9.+-]+\s*[;,]`)
	entityRe     = regexp.MustCompile(`(?i)&(#x?[0-9a-f]+|[a-z]+);`)
)

// CleanText normalises text (NFC, CRLF → LF, trailing spaces trimmed) and validates it. multiline allows
// newlines (story, update body). Returns the cleaned value or a validation detail code.
func CleanText(v string, min, max int, multiline bool) (string, string) {
	if !utf8.ValidString(v) {
		return "", "INVALID_ENCODING"
	}
	v = norm.NFC.String(strings.ReplaceAll(v, "\r\n", "\n"))
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '\n' && multiline, r == '\t' && multiline:
			b.WriteRune(r)
		case r == '\n', r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r), r == 0x200B, r == 0x202D, r == 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF: // zero-width, bidi overrides, BOM
			return "", "CONTROL_CHARACTERS"
		default:
			b.WriteRune(r)
		}
	}
	v = b.String()
	if multiline {
		lines := strings.Split(v, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		v = strings.TrimSpace(strings.Join(lines, "\n"))
	} else {
		v = strings.Join(strings.Fields(v), " ")
	}
	switch n := utf8.RuneCountInString(v); {
	case htmlTagRe.MatchString(v) || entityRe.MatchString(v):
		return "", "HTML_NOT_ALLOWED"
	case unsafeLinkRe.MatchString(v):
		return "", "UNSAFE_LINK"
	case n < min || n > max:
		return "", "INVALID_LENGTH"
	}
	return v, ""
}

// ParseAmountMinor parses a JSON amount_minor digit string into a positive int64 (no floats, no signs, no
// leading zeros, no overflow).
func ParseAmountMinor(s string) (int64, bool) {
	if s == "" || len(s) > 18 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// currencyUsable reports whether a currency is enabled and its minor units are verified (ZWG is not: LR-043).
func (s *Service) currencyUsable(ctx context.Context, code string) (bool, error) {
	list, err := s.Currencies(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range list {
		if c == code {
			return true, nil
		}
	}
	return false, nil
}

// Currencies lists the currencies a campaign goal may use now.
func (s *Service) Currencies(ctx context.Context) ([]string, error) {
	return money.UsableCurrencies(ctx, s.Pool)
}

func (s *Service) validateGoal(ctx context.Context, pol Policy, amount, currency string) (int64, []errs.Detail) {
	ok, err := s.currencyUsable(ctx, currency)
	if err != nil {
		return 0, []errs.Detail{{Field: "goal.currency", Code: "UNAVAILABLE"}}
	}
	if !ok {
		return 0, []errs.Detail{{Field: "goal.currency", Code: "CURRENCY_NOT_AVAILABLE"}}
	}
	n, ok := ParseAmountMinor(amount)
	if !ok {
		return 0, []errs.Detail{{Field: "goal.amount_minor", Code: "INVALID_FORMAT"}}
	}
	lim, ok := pol.Rules.Goals[currency]
	if !ok || n < lim.MinMinor || n > lim.MaxMinor {
		return 0, []errs.Detail{{Field: "goal.amount_minor", Code: "GOAL_OUT_OF_RANGE"}}
	}
	return n, nil
}

// ---- identifiers ------------------------------------------------------------------------------------------

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newPublicCode returns 10 random Crockford base32 characters (50 bits; not sequential, not enumerable).
func newPublicCode() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 10)
	for i, v := range b {
		out[i] = crockford[int(v)%32]
	}
	return string(out), nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a title into an ASCII slug base (max 80 characters; "campaign" when nothing is left).
func Slugify(title string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(title)) {
		if r < utf8.RuneSelf {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(nonSlug.ReplaceAllString(b.String(), "-"), "-")
	if len(s) > 80 {
		cut := s[:81]
		if i := strings.LastIndex(cut, "-"); i > 0 {
			s = s[:i] // cut at a word boundary
		} else {
			s = s[:80]
		}
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "campaign"
	}
	return s
}

// slugFor is the stable public slug: "<title-slug>-<public code in lower case>".
func slugFor(title, publicCode string) string {
	return Slugify(title) + "-" + strings.ToLower(publicCode)
}
