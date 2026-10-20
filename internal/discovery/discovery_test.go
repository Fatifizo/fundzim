package discovery

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

func TestNormalizeQuery(t *testing.T) {
	cases := []struct {
		in    string
		terms string
		code  string
	}{
		{"  School   FEES  Harare ", "school fees harare", ""},
		{"Mbare's clinic!!", "mbare clinic", ""},                                         // punctuation splits; single letters dropped
		{"a b c", "", ""},                                                                // nothing searchable: valid and empty
		{"Grade 7 fees", "grade 7 fees", ""},                                             // single digits are kept
		{"x' OR 1=1; DROP TABLE app.campaigns; --", "or 1 drop table app campaigns", ""}, // operators are just separators (digits kept, deduplicated)
		{"fees & | ! : * ( ) <-> \\ ' \"", "fees", ""},                                   // tsquery syntax cannot survive
		{"<script>alert(1)</script>", "script alert 1", ""},                              // markup is text to match, deduplicated
		{"Ndebele ÉCOLE café", "ndebele école café", ""},                                 // Unicode letters, NFC, lower case
		{strings.Repeat("a", 101), "", "INVALID_LENGTH"},
		{"\xff\xfe", "", "INVALID_ENCODING"},
		{"one two three four five six seven eight nine ten", "one two three four five six seven eight", ""}, // at most 8 terms
		{"fees fees FEES", "fees", ""},
	}
	for _, c := range cases {
		q, code := NormalizeQuery(c.in)
		if code != c.code {
			t.Fatalf("%q: code %q, want %q", c.in, code, c.code)
		}
		if code == "" && q.Text != c.terms {
			t.Fatalf("%q: terms %q, want %q", c.in, q.Text, c.terms)
		}
	}
	long, _ := NormalizeQuery(strings.Repeat("b", 60))
	if len([]rune(long.Terms[0])) != MaxTermRunes {
		t.Fatalf("term not capped: %d", len(long.Terms[0]))
	}
}

func TestTSQueryHasNoOperatorsFromInput(t *testing.T) {
	q, _ := NormalizeQuery(`fees:* & !(harare | "bulawayo") <-> x`)
	got := q.TSQuery()
	if got != "fees & harare & bulawayo:*" {
		t.Fatalf("tsquery %q", got)
	}
	short, _ := NormalizeQuery("school ab")
	if short.TSQuery() != "school & ab" { // a short last term is not a prefix (too broad)
		t.Fatalf("tsquery %q", short.TSQuery())
	}
	if (Query{}).TSQuery() != "" {
		t.Fatal("empty query must give an empty tsquery")
	}
	for _, in := range []string{"a:b", "x&y", "1|2", "'quoted'", "c!d"} {
		q, _ := NormalizeQuery(in)
		for _, term := range q.Terms {
			if strings.ContainsAny(term, ":&|!'()<>*\\\"") {
				t.Fatalf("%q produced an operator-bearing term %q", in, term)
			}
		}
	}
}

func TestHighlightRoundTripsAndMarksPrefixes(t *testing.T) {
	text := "Help Tendai finish school in Harare — school fees, books & uniforms."
	segs := Highlight(text, []string{"school", "har", "fee"})
	var b strings.Builder
	var marked []string
	for _, s := range segs {
		b.WriteString(s.Text)
		if s.Match {
			marked = append(marked, s.Text)
		}
	}
	if b.String() != text {
		t.Fatalf("segments do not rebuild the text: %q", b.String())
	}
	want := []string{"school", "Harare", "school", "fees"}
	if strings.Join(marked, ",") != strings.Join(want, ",") {
		t.Fatalf("marked %v, want %v", marked, want)
	}
	if got := Highlight("Café ÉCOLE", []string{"école"}); len(got) != 2 || !got[1].Match {
		t.Fatalf("unicode prefix match: %+v", got)
	}
	if got := Highlight("plain", nil); len(got) != 1 || got[0].Match {
		t.Fatalf("no terms: %+v", got)
	}
	if got := Highlight("<b>x</b>", []string{"b"}); got[0].Text != "<" { // markup stays plain text (rendered escaped)
		t.Fatalf("markup: %+v", got)
	}
}

func TestScoreIsBoundedDeterministicAndExplainable(t *testing.T) {
	w := DefaultWeights
	s1, c1 := Score(Signals{UniqueVisitors: 10, UniqueSharers: 2, AgeHours: 24}, w)
	s2, _ := Score(Signals{UniqueVisitors: 10, UniqueSharers: 2, AgeHours: 24}, w)
	if s1 != s2 || s1 <= 0 {
		t.Fatalf("not deterministic or zero: %v %v", s1, s2)
	}
	if c1.UniqueVisitors != 10 || c1.UniqueSharers != 2 || c1.AgeHours != 24 || c1.BelowMinimum {
		t.Fatalf("components: %+v", c1)
	}
	// log scaling: 100x the views yields far less than 100x the score
	big, _ := Score(Signals{UniqueVisitors: 1000, UniqueSharers: 2, AgeHours: 24}, w)
	if big >= 10*s1 {
		t.Fatalf("inflation not damped: %v vs %v", big, s1)
	}
	// freshness: older scores less, but never below a quarter of the engagement
	old, oc := Score(Signals{UniqueVisitors: 10, UniqueSharers: 2, AgeHours: 24 * 365}, w)
	if old >= s1 || old < 0.25*oc.Engagement-1e-6 {
		t.Fatalf("freshness: %v vs %v (engagement %v)", old, s1, oc.Engagement)
	}
	// below the minimum sample: never ranked
	if s, c := Score(Signals{UniqueVisitors: 2, UniqueSharers: 50}, w); s != 0 || !c.BelowMinimum {
		t.Fatalf("below minimum ranked: %v %+v", s, c)
	}
	// negative inputs (never produced, but never trusted)
	if s, _ := Score(Signals{UniqueVisitors: -5, UniqueSharers: -5, AgeHours: -10}, Weights{ViewWeight: 1, ShareWeight: 1, FreshnessHalfLifeHours: 6, WindowDays: 1}); s != 0 {
		t.Fatalf("negative inputs: %v", s)
	}
	if v := round6(1.23456789); v != 1.234568 || math.IsNaN(v) {
		t.Fatalf("round6 %v", v)
	}
}

func TestWeightsValidate(t *testing.T) {
	if err := DefaultWeights.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Weights{
		{ViewWeight: 101, ShareWeight: 1, FreshnessHalfLifeHours: 24, WindowDays: 7},
		{ViewWeight: 1, ShareWeight: -1, FreshnessHalfLifeHours: 24, WindowDays: 7},
		{ViewWeight: 1, ShareWeight: 1, FreshnessHalfLifeHours: 5, WindowDays: 7},
		{ViewWeight: 1, ShareWeight: 1, FreshnessHalfLifeHours: 24, WindowDays: 31},
		{ViewWeight: 1, ShareWeight: 1, FreshnessHalfLifeHours: 24, WindowDays: 7, MinUniqueVisitors: 1001},
		{ViewWeight: 0, ShareWeight: 0, FreshnessHalfLifeHours: 24, WindowDays: 7},
	}
	for _, b := range bad {
		var e *errs.Error
		if err := b.Validate(); !errors.As(err, &e) || e.Code != "VALIDATION_FAILED" {
			t.Fatalf("%+v accepted (%v)", b, err)
		}
	}
}

func TestCursorRoundTripAndTampering(t *testing.T) {
	ts := time.Date(2026, 10, 19, 10, 0, 0, 123456789, time.UTC).Format(time.RFC3339Nano)
	id := "0192f000-0000-7000-8000-000000000601"
	for sort, parts := range map[string][]string{SortNewest: {ts, id}, SortOldest: {ts, id}, SortRelevance: {"0.0607927", ts, id}, SortTrending: {"12.345678", ts, id}} {
		c := encodeCursor(sort, parts...)
		got, err := decodeCursor(c, sort)
		if err != nil || strings.Join(got, "|") != strings.Join(parts, "|") {
			t.Fatalf("%s: %v %v", sort, got, err)
		}
		other := SortNewest
		if sort == SortNewest {
			other = SortOldest
		}
		if _, err := decodeCursor(c, other); err == nil {
			t.Fatalf("%s cursor accepted for %s", sort, other)
		}
	}
	for _, bad := range []string{"", "%%%", encodeCursor(SortNewest, "yesterday", id), encodeCursor(SortNewest, ts, "1; DROP"),
		encodeCursor(SortTrending, "1e9", ts, id), encodeCursor(SortRelevance, "NaNx", ts, id), strings.Repeat("A", 300)} {
		if _, err := decodeCursor(bad, SortNewest); err == nil && bad != "" {
			if _, err2 := decodeCursor(bad, SortTrending); err2 == nil {
				t.Fatalf("bad cursor accepted: %q", bad)
			}
		}
	}
}

func TestIsAutomated(t *testing.T) {
	for _, ua := range []string{"", "  ", "WhatsApp/2.23.20.0", "facebookexternalhit/1.1", "Mozilla/5.0 (compatible; Googlebot/2.1)", "curl/8.4.0",
		"python-requests/2.31", "Mozilla/5.0 HeadlessChrome/120", "TelegramBot (like TwitterBot)", "Go-http-client/1.1", "Chrome-Lighthouse"} {
		if !IsAutomated(ua) {
			t.Fatalf("%q not treated as automated", ua)
		}
	}
	for _, ua := range []string{"Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
		"Opera/9.80 (Android; Opera Mini/36.2.2254/119.132; U; id) Presto/2.12.423 Version/12.16"} {
		if IsAutomated(ua) {
			t.Fatalf("%q treated as automated", ua)
		}
	}
}

func TestReferralCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		c, err := newCode()
		if err != nil || !refCodeRe.MatchString(c) {
			t.Fatalf("bad code %q (%v)", c, err)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestRatioBP(t *testing.T) {
	if ratioBP(1, 0) != nil {
		t.Fatal("no impressions must be null, never 0")
	}
	if v := ratioBP(3, 7); *v != 4285 { // 42.85 %, rounded down, integer arithmetic
		t.Fatalf("ratio %d", *v)
	}
}

// fakeCampaigns implements Campaigns for validation-only tests (no database).
type fakeCampaigns struct {
	Campaigns
	promotable map[string]bool
	err        error
}

func (f fakeCampaigns) Discoverable(_ context.Context, ids []string, _ bool) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = f.promotable[id]
	}
	return out, f.err
}

func (f fakeCampaigns) PublicCategories(context.Context, string) ([]campaigns.PublicCategory, error) {
	return []campaigns.PublicCategory{{Code: "MEDICAL", Slug: "medical"}}, nil
}

func (f fakeCampaigns) Locations(context.Context) ([]campaigns.Location, error) {
	return []campaigns.Location{{Code: "PROVINCE_HARARE", Kind: "PROVINCE", Slug: "harare"}}, nil
}

func newTestService(f fakeCampaigns) *Service {
	return &Service{Campaigns: f, Clock: clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))}
}

func TestPlacementRequestValidationAndPromotability(t *testing.T) {
	id := "0192f000-0000-7000-8000-000000000601"
	s := newTestService(fakeCampaigns{promotable: map[string]bool{}})
	now := s.now()
	bad := []PlacementInput{
		{CampaignID: "x", Position: 1, Reason: "Editorial pick for the week", EndsAt: now.Add(time.Hour)},
		{CampaignID: id, Position: 0, Reason: "Editorial pick for the week", EndsAt: now.Add(time.Hour)},
		{CampaignID: id, Position: 1, Reason: "short", EndsAt: now.Add(time.Hour)},
		{CampaignID: id, Position: 1, Reason: "Editorial pick for the week", EndsAt: now.Add(32 * 24 * time.Hour)},
		{CampaignID: id, Position: 1, Reason: "Editorial pick for the week", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)},
		{CampaignID: id, Position: 1, Reason: "Editorial pick for the week", StartsAt: now.Add(91 * 24 * time.Hour), EndsAt: now.Add(92 * 24 * time.Hour)},
		{CampaignID: id, Position: 1, Reason: "Editorial pick <b>week</b>", EndsAt: now.Add(time.Hour)},
		{CampaignID: id, Position: 1, Reason: "Editorial pick for the week", EndsAt: now.Add(-time.Minute)},
	}
	for i, in := range bad {
		var e *errs.Error
		if _, err := s.RequestPlacement(context.Background(), "staff", in); !errors.As(err, &e) || e.Code != "VALIDATION_FAILED" {
			t.Fatalf("case %d accepted: %v", i, err)
		}
	}
	// valid input for a campaign that is not promotable now
	if _, err := s.RequestPlacement(context.Background(), "staff", PlacementInput{CampaignID: id, Position: 1, Reason: "Editorial pick for the week",
		EndsAt: now.Add(7 * 24 * time.Hour)}); !errors.Is(err, ErrNotPromotable) {
		t.Fatalf("not promotable: %v", err)
	}
	// eligibility unavailable fails closed
	s2 := newTestService(fakeCampaigns{err: campaigns.ErrEligibilityUnavailable})
	if _, err := s2.RequestPlacement(context.Background(), "staff", PlacementInput{CampaignID: id, Position: 1, Reason: "Editorial pick for the week",
		EndsAt: now.Add(7 * 24 * time.Hour)}); !errors.Is(err, campaigns.ErrEligibilityUnavailable) {
		t.Fatalf("registry error must propagate: %v", err)
	}
}

func TestSearchPlanValidation(t *testing.T) {
	s := newTestService(fakeCampaigns{})
	ctx := context.Background()
	for _, in := range []SearchInput{{Sort: "popular"}, {Status: "draft"}, {Published: "2d"}, {Organiser: "company"}, {Category: "x'--"},
		{Province: "Harare Province"}, {Town: strings.Repeat("a", 41)}, {Q: strings.Repeat("q", 101)}, {Cursor: "!!!"}} {
		var e *errs.Error
		if _, err := s.planSearch(ctx, in); !errors.As(err, &e) || e.Code != "VALIDATION_FAILED" {
			t.Fatalf("%+v accepted: %v", in, err)
		}
	}
	p, err := s.planSearch(ctx, SearchInput{Q: "clinic", Category: "medical", Province: "harare", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if p.sort != SortRelevance || p.limit != MaxPageSize || !p.hasFilters {
		t.Fatalf("plan: %+v", p)
	}
	joined := strings.Join(p.where, " AND ")
	if !strings.Contains(joined, "i.discoverable") || strings.Contains(joined, "clinic") || strings.Contains(joined, "medical") {
		t.Fatalf("values must be parameters, never SQL text: %s", joined)
	}
	if p.args[1] != "MEDICAL" || p.args[2] != "PROVINCE_HARARE" {
		t.Fatalf("args: %v", p.args)
	}
	// unknown (but well-formed) filter values match nothing instead of erroring
	p2, err := s.planSearch(ctx, SearchInput{Category: "no-such", Province: "atlantis"})
	if err != nil || p2.args[1] != noMatch || p2.args[2] != noMatch {
		t.Fatalf("unknown filters: %v %v", p2, err)
	}
	// relevance without terms falls back to newest
	p3, _ := s.planSearch(ctx, SearchInput{Sort: SortRelevance})
	if p3.sort != SortNewest {
		t.Fatalf("sort %s", p3.sort)
	}
}

func TestRecommendationsAreNeverPersonalised(t *testing.T) {
	s := newTestService(fakeCampaigns{})
	out, err := s.Recommend(context.Background(), RecommendInput{Campaign: "Bad Slug"})
	if err == nil {
		t.Fatal("bad slug accepted")
	}
	if out.Personalised {
		t.Fatal("recommendations must never claim personalisation")
	}
	for code, text := range reasonText {
		if text == "" || strings.Contains(strings.ToLower(text), "you") && code != ReasonRelatedSearch {
			t.Fatalf("reason %s wording %q", code, text)
		}
	}
}

func TestReferralsBlockedInProduction(t *testing.T) {
	s := newTestService(fakeCampaigns{})
	s.Production = true
	s.Flags = flagsOn{}
	on, err := s.referralsEnabled(context.Background())
	if err != nil || on {
		t.Fatalf("production referrals must stay off while LR-023 is open (ProductionReferralsEnabled=%v): %v %v", ProductionReferralsEnabled, on, err)
	}
	s.Production = false
	if on, _ := s.referralsEnabled(context.Background()); !on {
		t.Fatal("development with the flag on should allow referrals")
	}
	s.Flags = nil
	if on, _ := s.referralsEnabled(context.Background()); on {
		t.Fatal("no flag reader must mean off")
	}
	if _, err := s.IssueReferral(context.Background(), ReferralInput{Slug: "a-b", Channel: "SMS"}); err == nil {
		t.Fatal("unknown channel accepted")
	}
}

type flagsOn struct{}

func (flagsOn) Enabled(context.Context, string) (bool, error) { return true, nil }

func TestIngestValidation(t *testing.T) {
	s := newTestService(fakeCampaigns{})
	ctx := context.Background()
	bad := []EventsInput{
		{},
		{Events: make([]EventInput, MaxEventsPerBatch+1)},
		{Events: []EventInput{{Type: "PURCHASE", Slug: "a-b"}}},
		{Events: []EventInput{{Type: EvView, Slug: "A B"}}},
		{Events: []EventInput{{Type: EvView, Slug: "a-b", Source: "GOOGLE_ADS"}}},
		{Events: []EventInput{{Type: EvView, Slug: "a-b", Ref: "../../etc"}}},
		{Events: []EventInput{{Type: EvShare, Slug: "a-b", Channel: "TELEGRAM"}}},
		{Events: []EventInput{{Type: EvSearchClick, Slug: "a-b", Source: "DIRECT"}}},
	}
	for i, in := range bad {
		if _, err := s.Ingest(ctx, in, "Mozilla/5.0"); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	n, err := s.Ingest(ctx, EventsInput{Events: []EventInput{{Type: EvView, Slug: "a-b", Source: "SEARCH", Ref: "0123456789ab"},
		{Type: EvShare, Slug: "a-b", Channel: "WHATSAPP"}}}, "Mozilla/5.0")
	if err != nil || n != 2 {
		t.Fatalf("valid batch: %d %v", n, err)
	}
}
