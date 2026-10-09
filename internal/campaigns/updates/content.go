package updates

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Content rules (interface-contracts §9): plain text only. HTML tags are refused (HTML_NOT_ALLOWED);
// javascript:, vbscript: and data: URLs are refused (UNSAFE_LINK); control characters are stripped. The web
// app renders the text escaped and never turns it into links; these checks are defence in depth for every
// other consumer (emails, exports, staff tools).

// Limits are inclusive character (rune) bounds from the APPROVED campaign policy.
type Limits struct{ Min, Max int }

const (
	CodeHTMLNotAllowed = "HTML_NOT_ALLOWED"
	CodeUnsafeLink     = "UNSAFE_LINK"
)

var (
	// a tag, comment, doctype or processing instruction opener: "<" then optional space then a letter, "/", "!" or "?"
	tagRE = regexp.MustCompile(`<\s*[/!?A-Za-z]`)
	// script schemes, checked on a copy with all whitespace and controls removed (browsers ignore tabs and
	// newlines inside a scheme), so "java\tscript:" and "javascript :" are caught too
	scriptSchemeRE = regexp.MustCompile(`(?i)(javascript|vbscript|livescript|mocha):`)
	// data: URLs (a media type, a base64 marker or a bare comma after the scheme)
	dataURLRE = regexp.MustCompile(`(?i)data:([a-z]+/[a-z0-9.+\-]+|[^,]*;base64|,)`)
	spacesRE  = regexp.MustCompile(`[ \t\p{Zs}]+`)
	blankRE   = regexp.MustCompile(`\n{3,}`)
)

// Clean normalises user text: Unicode NFC; CRLF/CR, U+2028 and U+2029 become "\n"; tabs become spaces; C0
// and C1 control characters are removed; Unicode format characters (bidirectional overrides and isolates,
// zero-width spaces, BOM, soft hyphen, ...) are removed except the zero-width joiner and emoji tag characters
// used in emoji sequences. A single-line value has every run of whitespace collapsed to one space; a
// multi-line value keeps line breaks (at most one blank line in a row) and trims trailing spaces per line.
func Clean(s string, multiline bool) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n", "\t", " ").Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			if multiline {
				b.WriteRune('\n')
			} else {
				b.WriteRune(' ')
			}
		case unicode.IsControl(r):
			// dropped (C0, DEL, C1)
		case unicode.Is(unicode.Cf, r) && r != '\u200d' && !(r >= 0xE0020 && r <= 0xE007F):
			// dropped (format characters: bidi controls, zero-width, BOM, ...)
		default:
			b.WriteRune(r)
		}
	}
	s = norm.NFC.String(b.String())
	if !multiline {
		return strings.TrimSpace(spacesRE.ReplaceAllString(s, " "))
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(spacesRE.ReplaceAllString(l, " "), " ")
	}
	s = blankRE.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}

// Unsafe reports the §9 content code a cleaned value violates ("" when it is acceptable). It checks the text
// as written, its compatibility-folded form (fullwidth and other look-alike forms of "<" or of a scheme are
// folded to ASCII by NFKC) and the HTML-entity-decoded form (so "&lt;script&gt;" and "javascript&#58;" are
// refused as well).
func Unsafe(s string) string {
	folded := norm.NFKC.String(s)
	forms := []string{s, folded, html.UnescapeString(s), html.UnescapeString(folded)}
	for _, f := range forms {
		if tagRE.MatchString(f) {
			return CodeHTMLNotAllowed
		}
	}
	for _, f := range forms {
		sq := squeeze(f)
		if scriptSchemeRE.MatchString(sq) || dataURLRE.MatchString(sq) {
			return CodeUnsafeLink
		}
	}
	return ""
}

// squeeze removes whitespace, control and format characters (what a URL parser would skip inside a scheme).
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// Validate cleans raw and checks it against the §9 rules and lim. It returns the cleaned value or a 422 error:
// HTML_NOT_ALLOWED / UNSAFE_LINK (with the field in details), or VALIDATION_FAILED with REQUIRED, TOO_SHORT or
// TOO_LONG.
func Validate(field, raw string, multiline bool, lim Limits) (string, error) {
	v := Clean(raw, multiline)
	if code := Unsafe(v); code != "" {
		msg := "Plain text only: HTML is not allowed."
		if code == CodeUnsafeLink {
			msg = "This text contains a link type that is not allowed."
		}
		e := errs.New(errs.Unprocessable, code, msg)
		e.Details = []errs.Detail{{Field: field, Code: code}}
		return "", e
	}
	n := utf8.RuneCountInString(v)
	switch {
	case n == 0:
		return "", validation(field, "REQUIRED")
	case n < lim.Min:
		return "", validation(field, "TOO_SHORT")
	case n > lim.Max:
		return "", validation(field, "TOO_LONG")
	}
	return v, nil
}

func validation(field, code string) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = []errs.Detail{{Field: field, Code: code}}
	return e
}
