package campaigns

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Go edge list and the database registry must be identical (CLAUDE.md engineering rules).
func TestEdgesMatchMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/20261009171000_campaigns.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('campaign',\s*'([A-Z_]*)',\s*'([A-Z_]+)'\)`)
	sqlEdges := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		sqlEdges[m[1]+"->"+m[2]] = true
	}
	goEdges := map[string]bool{}
	for from, tos := range Edges {
		for _, to := range tos {
			goEdges[from+"->"+to] = true
		}
	}
	var missing, extra []string
	for e := range sqlEdges {
		if !goEdges[e] {
			missing = append(missing, e)
		}
	}
	for e := range goEdges {
		if !sqlEdges[e] {
			extra = append(extra, e)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra) > 0 || len(sqlEdges) == 0 {
		t.Fatalf("edge lists differ: only in SQL %v, only in Go %v (sql=%d)", missing, extra, len(sqlEdges))
	}
	// FROZEN is reserved (ADR-036 §1); ARCHIVED is terminal; nothing returns to PUBLISHED from ARCHIVED.
	if len(Edges["FROZEN"]) != 0 || len(Edges[StatusArchived]) != 0 || Can(StatusArchived, StatusActive) || Can(StatusDraft, StatusActive) {
		t.Fatal("reserved or terminal states have edges")
	}
}

func TestCleanTextRefusesMarkupAndUnsafeContent(t *testing.T) {
	bad := map[string]string{
		`<script>alert(1)</script>`:          "HTML_NOT_ALLOWED",
		`hello <img src=x onerror=alert(1)>`: "HTML_NOT_ALLOWED",
		`a </b> b`:                           "HTML_NOT_ALLOWED",
		`see &lt;b&gt; here`:                 "HTML_NOT_ALLOWED",
		`click javascript:alert(1) now`:      "UNSAFE_LINK",
		`JaVaScRiPt :alert(1) padded`:        "UNSAFE_LINK",
		`data:text/html;base64,PHNjcmlwdD4=`: "UNSAFE_LINK",
		"zero​width padded text":             "CONTROL_CHARACTERS",
		"bidi‮override padded text":          "CONTROL_CHARACTERS",
		"nul\x00byte padded text":            "CONTROL_CHARACTERS",
	}
	for in, want := range bad {
		if _, code := CleanText(in, 1, 1000, true); code != want {
			t.Errorf("%q: got %q, want %q", in, code, want)
		}
	}
	for _, ok := range []string{"Help Tariro get to school in Harare", "Costs are 5 < 10 and 3 > 2", "Visit https://example.org for details",
		"Ndatenda! Thank you — 100% of funds", "Line one\n\nLine two", "Our data: 12 families helped"} {
		if _, code := CleanText(ok, 1, 1000, true); code != "" {
			t.Errorf("%q refused: %s", ok, code)
		}
	}
	if v, _ := CleanText("  a   b \r\n c ", 1, 100, false); v != "a b c" {
		t.Fatalf("single-line normalisation: %q", v)
	}
	if v, _ := CleanText("p1  \r\n\r\np2\t ", 1, 100, true); v != "p1\n\np2" {
		t.Fatalf("multi-line normalisation: %q", v)
	}
	if _, code := CleanText("short", 10, 100, false); code != "INVALID_LENGTH" {
		t.Fatal("length not enforced")
	}
	if _, code := CleanText(string([]byte{0xff, 0xfe}), 0, 10, false); code != "INVALID_ENCODING" {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParseAmountMinorIsExactAndStrict(t *testing.T) {
	good := map[string]int64{"1": 1, "1000": 1000, "999999999999999999": 999999999999999999}
	for in, want := range good {
		if got, ok := ParseAmountMinor(in); !ok || got != want {
			t.Errorf("%q: %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "0", "-5", "+5", "1.5", "1e3", "01", " 5", "5 ", "1_000", "9999999999999999999", "0x10", "１２"} {
		if _, ok := ParseAmountMinor(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSlugsAndPublicCodes(t *testing.T) {
	cases := map[string]string{
		"Help Tariro go to school!":       "help-tariro-go-to-school",
		"Ñandú — café & crème":            "nandu-cafe-creme",
		"<script>alert(1)</script>":       "script-alert-1-script",
		"!!!":                             "campaign",
		strings.Repeat("abcdefghij ", 20): strings.TrimRight(strings.Repeat("abcdefghij-", 7), "-"),
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	seen := map[string]bool{}
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{10}$`)
	for i := 0; i < 2000; i++ {
		c, err := newPublicCode()
		if err != nil || !re.MatchString(c) || seen[c] {
			t.Fatalf("public code %q (dup=%v): %v", c, seen[c], err)
		}
		seen[c] = true
	}
	s := slugFor("Clinic roof", "AB12CD34EF")
	if !validSlug(s) || s != "clinic-roof-ab12cd34ef" {
		t.Fatalf("slugFor: %q", s)
	}
	for _, bad := range []string{"", "UPPER", "a--b", "-a", "a b", "a/b", "../x", strings.Repeat("a", 97)} {
		if validSlug(bad) {
			t.Errorf("slug %q accepted", bad)
		}
	}
}

func TestRestrictionMatrix(t *testing.T) {
	for _, tc := range []struct {
		level, action string
		refused       bool
	}{
		{RestrictionRestricted, ActCreateDraft, true}, {RestrictionRestricted, ActEditDraft, false}, {RestrictionRestricted, ActSubmit, true},
		{RestrictionRestricted, ActPublish, true}, {RestrictionRestricted, ActUpdatePublished, false}, {RestrictionRestricted, ActReactivate, true},
		{RestrictionSuspended, ActEditDraft, true}, {RestrictionOffboarded, ActUpdatePublished, true}, {RestrictionNone, ActPublish, false},
	} {
		e := &evalCtx{}
		e.applyRestriction(tc.level, tc.action)
		if (len(e.reasons) > 0) != tc.refused {
			t.Errorf("%s/%s: refused=%v", tc.level, tc.action, len(e.reasons) > 0)
		}
		for _, r := range e.reasons {
			if r.Code != "ACCOUNT_RESTRICTED" || strings.Contains(strings.ToLower(r.Message), "compliance") {
				t.Errorf("restriction reason discloses too much: %+v", r)
			}
		}
	}
	if highest(map[Subject]string{{"USER", "a"}: RestrictionRestricted, {"CAMPAIGN", "b"}: RestrictionOffboarded}) != RestrictionOffboarded {
		t.Fatal("highest level")
	}
}

func TestTierNeverSilentlyDowngraded(t *testing.T) {
	if upgradeTier("HIGH", "STANDARD", true) != "HIGH" || upgradeTier("STANDARD", "HIGH", true) != "HIGH" || upgradeTier("HIGH", "STANDARD", false) != "STANDARD" {
		t.Fatal("tier rule")
	}
}
