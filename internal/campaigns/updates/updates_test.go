package updates

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

var lim = Limits{Min: 3, Max: 120}
var bodyLim = Limits{Min: 10, Max: 10000}

func code(err error) (string, []errs.Detail) {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code, e.Details
	}
	return "", nil
}

func TestValidateRefusesHTMLAndXSSPayloads(t *testing.T) {
	cases := []string{
		`<script>alert(1)</script>`,
		`Hello <img src=x onerror=alert(1)> world`,
		`<IMG SRC=x OnError=alert(1)>`,
		`< img src=x onerror=alert(1)>`,
		`text </p> more`,
		`<!-- comment --> hello there`,
		`<?xml version="1.0"?> payload`,
		`<svg/onload=alert(1)>`,
		`<a href="https://example.org">link</a>`,
		`<iframe src=//evil.example></iframe>`,
		`<b>bold</b> text here`,
		"<\u200bscript>alert(1)</script>",               // zero-width space inside the tag is stripped first
		"<\u202escript>alert(1)",                        // bidi override inside the tag
		"\uff1cscript\uff1ealert(1)\uff1c/script\uff1e", // fullwidth angle brackets fold to ASCII
		"\ufe64img src=x onerror=alert(1)\ufe65",        // small form variants
		`&lt;script&gt;alert(1)&lt;/script&gt;`,         // entity-encoded tags
		`&#60;img src=x onerror=alert(1)&#62;`,
		"<\x00script>alert(1)", // NUL inside the tag is stripped first
	}
	for _, c := range cases {
		_, err := Validate("body", c, true, bodyLim)
		got, details := code(err)
		if got != CodeHTMLNotAllowed {
			t.Errorf("%q: want HTML_NOT_ALLOWED, got %q (%v)", c, got, err)
			continue
		}
		if len(details) != 1 || details[0].Field != "body" || details[0].Code != CodeHTMLNotAllowed {
			t.Errorf("%q: details %v", c, details)
		}
		if e := errs.As(err); e.Kind.Status() != 422 {
			t.Errorf("%q: status %d", c, e.Kind.Status())
		}
	}
}

func TestValidateRefusesUnsafeLinks(t *testing.T) {
	cases := []string{
		`Visit javascript:alert(1) now please`,
		`JaVaScRiPt:alert(document.cookie) here`,
		"java\tscript:alert(1) please now",
		"java\nscript:alert(1) please now",
		`javascript :alert(1) spaced out`,
		`vbscript:msgbox("x") is old`,
		`see data:text/html;base64,PHNjcmlwdD4= here`,
		`see data:text/plain,hello there`,
		`DATA:image/svg+xml;utf8,payload here`,
		`data:;base64,AAAA is a thing`,
		"\uff4aavascript:alert(1) fullwidth j", // fullwidth letter folds to ASCII
		`javascript&#58;alert(1) entity colon`, // entity-encoded colon
		`&#x6A;avascript:alert(1) entity j`,
		"java\u200bscript:alert(1) zero width", // stripped format char
		"java\u00adscript:alert(1) soft hyphen",
	}
	for _, c := range cases {
		_, err := Validate("body", c, true, bodyLim)
		if got, _ := code(err); got != CodeUnsafeLink && got != CodeHTMLNotAllowed {
			t.Errorf("%q: want UNSAFE_LINK, got %q (%v)", c, got, err)
		}
	}
	// the plain ones must be UNSAFE_LINK specifically
	for _, c := range []string{`Visit javascript:alert(1) now please`, `see data:text/html;base64,PHNjcmlwdD4= here`} {
		if _, err := Validate("body", c, true, bodyLim); func() string { s, _ := code(err); return s }() != CodeUnsafeLink {
			t.Errorf("%q: want UNSAFE_LINK, got %v", c, err)
		}
	}
}

func TestValidateAcceptsOrdinaryText(t *testing.T) {
	cases := map[string]string{
		"Thank you all! We reached the clinic today.":            "Thank you all! We reached the clinic today.",
		"Costs: 3 < 5 and 7 > 2, so we are fine.":                "Costs: 3 < 5 and 7 > 2, so we are fine.",
		"I <3 this community, ndatenda!":                         "I <3 this community, ndatenda!",
		"Our data: 12 families helped, see https://example.org.": "Our data: 12 families helped, see https://example.org.",
		"Mazvita 👩\u200d👩\u200d👧 family photo 🇿🇼":                "Mazvita 👩\u200d👩\u200d👧 family photo 🇿🇼",
		"Tom & Jerry's school fees are paid":                     "Tom & Jerry's school fees are paid",
	}
	for in, want := range cases {
		got, err := Validate("body", in, true, bodyLim)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
}

func TestCleanStripsControlAndFormatCharacters(t *testing.T) {
	in := "Hel\x00lo\x07 wor\u202eld\u200b\ufeff\r\nline\ttwo\u0085\x1b[31m red\n\n\n\nend  "
	got := Clean(in, true)
	want := "Hello world\nline two[31m red\n\nend"
	if got != want {
		t.Fatalf("Clean multi-line: %q, want %q", got, want)
	}
	if got := Clean("  a\nb\r\nc\u2028d\t e  ", false); got != "a b c d e" {
		t.Fatalf("Clean single-line: %q", got)
	}
	// NFC: e + combining acute becomes one code point
	if got := Clean("Cafe\u0301 visit", false); got != "Caf\u00e9 visit" {
		t.Fatalf("NFC: %q", got)
	}
	// invalid UTF-8 is dropped
	if got := Clean("ok\xff\xfe fine", false); got != "ok fine" {
		t.Fatalf("invalid utf-8: %q", got)
	}
	// the result never contains a control or bidi character
	for _, r := range Clean(strings.Repeat("\x01\u202a\u2066\u200e", 3)+"x", true) {
		if r != 'x' {
			t.Fatalf("leftover %U", r)
		}
	}
}

func TestValidateLengthsUseRunesAfterCleaning(t *testing.T) {
	if _, err := Validate("title", "  \u200b\x00 ", false, lim); func() string { _, d := code(err); return d[0].Code }() != "REQUIRED" {
		t.Fatalf("blank: %v", err)
	}
	if _, err := Validate("title", "ab", false, lim); func() string { _, d := code(err); return d[0].Code }() != "TOO_SHORT" {
		t.Fatalf("short: %v", err)
	}
	if _, err := Validate("title", strings.Repeat("ü", 121), false, lim); func() string { _, d := code(err); return d[0].Code }() != "TOO_LONG" {
		t.Fatalf("long: %v", err)
	}
	if v, err := Validate("title", strings.Repeat("ü", 120), false, lim); err != nil || len([]rune(v)) != 120 {
		t.Fatalf("120 runes must pass: %v", err)
	}
	// padding with stripped characters cannot satisfy the minimum
	if _, err := Validate("title", "a\u200b\u200b\u200bb", false, lim); err == nil {
		t.Fatal("zero-width padding counted towards the minimum")
	}
}

func TestParsePolicyFailsClosed(t *testing.T) {
	p, err := parsePolicy([]byte(`{"content": {"update_title": [3, 120], "update_body": [10, 10000]},
		"tiers": {"HIGH": {"second_approval": true, "pre_moderate_updates": true}, "STANDARD": {"second_approval": false}}}`))
	if err != nil || p.title != (Limits{3, 120}) || p.body != (Limits{10, 10000}) || !p.tierPreMod["HIGH"] || p.tierPreMod["STANDARD"] {
		t.Fatalf("parse: %+v %v", p, err)
	}
	for _, bad := range []string{`{}`, `{"content": {"update_title": [3]}}`, `{"content": {"update_title": [3,120], "update_body": [0, 5]}}`,
		`{"content": {"update_title": [30,12], "update_body": [1, 5]}}`, `not json`} {
		if _, err := parsePolicy([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", bad)
		} else if e := errs.As(err); e.Code != "CAMPAIGN_POLICY_UNAVAILABLE" {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// the seeded campaign-v1 policy has the update limits
	raw, err := os.ReadFile("../../../migrations/20261009171000_campaigns.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"update_title": [3, 120], "update_body": [10, 10000]`) {
		t.Fatal("campaign-v1 policy no longer carries update_title/update_body")
	}
}

func TestModerationReasons(t *testing.T) {
	if r := ModerationReasons("STANDARD", false, campaigns.RestrictionNone); len(r) != 0 {
		t.Fatalf("standard: %v", r)
	}
	if r := ModerationReasons("PRE_MODERATE_UPDATES", false, ""); !slices.Equal(r, []string{ReasonCategoryPolicy}) {
		t.Fatalf("category: %v", r)
	}
	if r := ModerationReasons("STANDARD", true, ""); !slices.Equal(r, []string{ReasonTierPolicy}) {
		t.Fatalf("tier: %v", r)
	}
	if r := ModerationReasons("STANDARD", false, campaigns.RestrictionRestricted); !slices.Equal(r, []string{ReasonOwnerRestricted}) {
		t.Fatalf("restricted: %v", r)
	}
}

func TestCleanMediaIDs(t *testing.T) {
	id := "0192f000-0000-7000-8000-000000000601"
	if _, err := cleanMediaIDs([]string{id, id}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := cleanMediaIDs([]string{"javascript:alert(1)"}); err == nil {
		t.Fatal("non-uuid accepted")
	}
	if _, err := cleanMediaIDs(make([]string, MaxMedia+1)); err == nil {
		t.Fatal("too many accepted")
	}
	if out, err := cleanMediaIDs(nil); err != nil || out == nil || len(out) != 0 {
		t.Fatalf("nil: %v %v", out, err)
	}
}

func TestDecisionValidation(t *testing.T) {
	if _, err := validDecision(Decision{ReasonCode: "spam", Note: "ok note"}); err == nil {
		t.Fatal("lower-case reason accepted")
	}
	if _, err := validDecision(Decision{ReasonCode: "SPAM", Note: " \x00 "}); err == nil {
		t.Fatal("blank note accepted")
	}
	d, err := validDecision(Decision{ReasonCode: "CONTENT_OK", Note: "Looks\x07 fine"})
	if err != nil || d.Note != "Looks fine" {
		t.Fatalf("%+v %v", d, err)
	}
}

// The Go edge list must equal the campaign_update rows of the migration.
func TestTransitionsMatchMigration(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/20261009171200_campaign_updates.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	re := regexp.MustCompile(`\('campaign_update',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	var sqlEdges, goEdges []string
	for _, m := range re.FindAllStringSubmatch(up, -1) {
		sqlEdges = append(sqlEdges, m[1]+"->"+m[2])
	}
	for from, tos := range Transitions {
		for _, to := range tos {
			goEdges = append(goEdges, from+"->"+to)
		}
	}
	sort.Strings(sqlEdges)
	sort.Strings(goEdges)
	if len(sqlEdges) == 0 || !slices.Equal(sqlEdges, goEdges) {
		t.Fatalf("edge lists differ:\nsql %v\ngo  %v", sqlEdges, goEdges)
	}
}

func TestRoutesHavePolicies(t *testing.T) {
	s := New(Deps{})
	seen := map[string]bool{}
	for _, r := range s.Routes() {
		if r.Policy.Kind == "" || r.Handler == nil || seen[r.Pattern] {
			t.Errorf("route %s: policy %v", r.Pattern, r.Policy)
		}
		seen[r.Pattern] = true
		if strings.Contains(r.Pattern, "/admin/") && r.Policy.Permission != PermModerate {
			t.Errorf("staff route %s without content.moderate", r.Pattern)
		}
	}
}
