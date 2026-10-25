//go:build integration

// Stage 16 discovery (ADR-048) against the real stack: the outbox indexer in the compose worker, search with filters,
// sorts, cursors, highlights and typo tolerance; location and category browsing; serve-time eligibility (compliance
// restriction, risk hold, suspension, a deliberately stale projection row); featured placements (maker-checker in Go and
// in the database); trending from deduplicated, bot-filtered engagement; rule-based recommendations; referral codes;
// owner analytics and staff RBAC; failure injection (eligibility registry down → fail closed); concurrency.
//
// The tests share the development database with the compose worker and other tests: they find their own campaigns by a
// unique token and assert on them only.
package integration

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/discovery"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

const browserUA = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36"

func init() {
	for _, k := range []string{"location", "province_code", "province_name", "province_slug", "town_slug", "highlights", "match", "text", "reason",
		"query", "sort", "rule", "policy_version", "window_days", "personalised", "rules", "provinces", "towns", "country", "unspecified_campaigns",
		"note", "discoverable_campaigns", "accepted", "expires_at", "channel"} {
		publicAllowedKeys[k] = true
	}
}

// discoWord is a random lower-case word: a unique, letters-only search token (fuzzy matching needs letters).
func discoWord() string {
	const letters = "bcdfghjkmnpqrstvwxz"
	b := make([]byte, 11)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return "zq" + string(b)
}

// discoCampaign publishes a campaign whose title carries tok, with an optional location.
func discoCampaign(t *testing.T, s *itServer, f *verifStaff, prefix, tok, location string) (vUser, string, string) {
	t.Helper()
	u, id := readyCampaign(t, s, f, prefix, "EDUCATION")
	r := u.do("GET", "/campaigns/"+id, nil)
	body := map[string]any{"title": "Help Tariro go to school " + tok}
	if location != "" {
		body["location_code"] = location
	}
	u.expect(u.do("PATCH", "/campaigns/"+id, body, "If-Match", r.Header.Get("ETag")), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u, id)
	return u, id, u.do("GET", "/campaigns/"+id, nil).field(t, "slug")
}

type discoPage struct {
	Items []struct {
		Slug       string `json:"slug"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		Location   *struct{ Code, Name, ProvinceSlug string }
		Highlights *struct {
			Title []struct {
				Text  string `json:"text"`
				Match bool   `json:"match"`
			} `json:"title"`
		} `json:"highlights"`
		Reason *struct {
			Code string `json:"code"`
			Text string `json:"text"`
		} `json:"reason"`
		Selection string `json:"selection"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
	Match      string  `json:"match"`
	Sort       string  `json:"sort"`
	Total      struct {
		Value int `json:"value"`
	} `json:"total"`
	Personalised *bool `json:"personalised"`
}

// discoClientIP is this run's client address for discovery's public reads: like every other suite, the tests present
// their own address (loopback is a trusted proxy) so they never share per-IP limits (global.ip, discovery.search.ip)
// with the rest of the integration run. Callers that test per-address behaviour pass their own X-Forwarded-For.
var discoClientIP = fmt.Sprintf("198.18.%d.%d", rand.Intn(250)+1, rand.Intn(250)+1)

func discoReq(t *testing.T, s *itServer, method, path string, body any, hdr ...string) (int, http.Header, []byte) {
	t.Helper()
	for i := 0; i+1 < len(hdr); i += 2 {
		if strings.EqualFold(hdr[i], "X-Forwarded-For") {
			return publicReq(t, s, method, path, body, hdr...)
		}
	}
	// The run's own address shares global.ip (40 per 2 s) across the polling loops of one test: a 429 there is the
	// protective limit, not the behaviour under test (TestDiscoveryFailureInjectionAndAbuse tests it on its own address), so wait it out.
	for attempt := 0; ; attempt++ {
		code, h, raw := publicReq(t, s, method, path, body, append([]string{"X-Forwarded-For", discoClientIP}, hdr...)...)
		if code != http.StatusTooManyRequests || attempt == 3 {
			return code, h, raw
		}
		wait, err := strconv.Atoi(h.Get("Retry-After"))
		if err != nil || wait < 1 || wait > 5 {
			wait = 2
		}
		time.Sleep(time.Duration(wait) * time.Second)
	}
}

func discoPublicGet(t *testing.T, s *itServer, path string) (int, []byte) {
	t.Helper()
	c, _, b := discoReq(t, s, "GET", path, nil)
	return c, b
}

func discoGet(t *testing.T, s *itServer, path string, hdr ...string) (int, http.Header, discoPage, []byte) {
	t.Helper()
	code, h, raw := discoReq(t, s, "GET", path, nil, hdr...)
	var env struct {
		Data discoPage `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	return code, h, env.Data, raw
}

func (p discoPage) has(slug string) bool {
	for _, it := range p.Items {
		if it.Slug == slug {
			return true
		}
	}
	return false
}

// view posts engagement events from one simulated visitor (address + browser user agent).
func view(t *testing.T, s *itServer, ip string, events ...map[string]string) int {
	t.Helper()
	code, _, _ := discoReq(t, s, "POST", "/discovery/events", map[string]any{"events": events}, "X-Forwarded-For", ip, "User-Agent", browserUA)
	return code
}

func flushDiscovery(t *testing.T, s *itServer) {
	t.Helper()
	s.d.Discovery.Recorder.Flush(ctx(t))
}

func TestDiscoveryEndToEnd(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	tok := discoWord()
	for tok[4] == tok[5] { // the typo below swaps letters 5 and 6: equal letters would leave the token unchanged (KI-R1-08)
		tok = discoWord()
	}
	uA, idA, slugA := discoCampaign(t, s, f, "disco-a", tok, "TOWN_MUTARE")
	uB, idB, slugB := discoCampaign(t, s, f, "disco-b", tok+" bursary", "PROVINCE_HARARE")

	// ---- the compose worker's outbox consumer indexes both publications
	waitFor(t, 90*time.Second, "both campaigns indexed by the worker", func() bool {
		var n int
		_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.discovery_campaign_index WHERE campaign_id IN ($1, $2) AND discoverable`, idA, idB).Scan(&n)
		return n == 2
	})
	var doc string
	_ = mig.QueryRow(ctx(t), `SELECT search_doc::text FROM app.discovery_campaign_index WHERE campaign_id = $1`, idA).Scan(&doc)
	if !strings.Contains(doc, tok) || !strings.Contains(doc, "mutare") || !strings.Contains(doc, "manicaland") {
		t.Fatalf("search document lacks title/location terms: %s", doc)
	}

	// ---- search: relevance, highlights, cache headers, conditional GET, allow-listed keys
	code, hdr, page, raw := discoGet(t, s, "/discovery/search?q="+tok)
	if code != 200 || hdr.Get("Cache-Control") != "public, no-cache" || hdr.Get("ETag") == "" {
		t.Fatalf("search: %d %v", code, hdr)
	}
	assertPublicKeys(t, "discovery search", raw)
	if !page.has(slugA) || !page.has(slugB) || page.Match != "EXACT" || page.Sort != "relevance" || page.Total.Value != 2 {
		t.Fatalf("search page: %+v", page)
	}
	marked := false
	for _, it := range page.Items {
		for _, sg := range it.Highlights.Title {
			marked = marked || (sg.Match && sg.Text == tok)
		}
	}
	if !marked {
		t.Fatalf("highlight missing: %s", raw)
	}
	if c, _, _ := discoReq(t, s, "GET", "/discovery/search?q="+tok, nil, "If-None-Match", hdr.Get("ETag")); c != 304 {
		t.Fatalf("conditional search: %d", c)
	}
	// relevance: the extra term ranks B first for "tok bursary"
	_, _, page, _ = discoGet(t, s, "/discovery/search?q="+tok+"+bursary")
	if len(page.Items) != 1 || page.Items[0].Slug != slugB {
		t.Fatalf("all terms must match: %+v", page.Items)
	}
	// prefix match on the last term and case-insensitivity
	_, _, page, _ = discoGet(t, s, "/discovery/search?q="+strings.ToUpper(tok[:8]))
	if !page.has(slugA) {
		t.Fatal("prefix/case-insensitive match failed")
	}
	// typo tolerance: two letters swapped → no exact match → fuzzy title match
	typo := tok[:4] + string(tok[5]) + string(tok[4]) + tok[6:]
	_, _, page, raw = discoGet(t, s, "/discovery/search?q="+typo)
	if page.Match != "FUZZY" || !page.has(slugA) || page.NextCursor != nil {
		t.Fatalf("fuzzy: %s", raw)
	}

	// ---- filters
	for path, want := range map[string][2]bool{
		"/discovery/campaigns?q=" + tok + "&province=manicaland":    {true, false},
		"/discovery/campaigns?q=" + tok + "&town=mutare":            {true, false},
		"/discovery/campaigns?q=" + tok + "&province=harare":        {false, true},
		"/discovery/campaigns?q=" + tok + "&category=education":     {true, true},
		"/discovery/campaigns?q=" + tok + "&category=medical":       {false, false},
		"/discovery/campaigns?q=" + tok + "&status=ended":           {false, false},
		"/discovery/campaigns?q=" + tok + "&organiser=individual":   {true, true},
		"/discovery/campaigns?q=" + tok + "&organiser=organisation": {false, false},
		"/discovery/campaigns?q=" + tok + "&published=7d":           {true, true},
		"/discovery/campaigns?q=" + tok + "&province=no-such-place": {false, false},
	} {
		_, _, p, raw := discoGet(t, s, path)
		if p.has(slugA) != want[0] || p.has(slugB) != want[1] {
			t.Fatalf("%s: A=%v B=%v (%s)", path, p.has(slugA), p.has(slugB), raw)
		}
	}
	// location detail on cards
	_, _, page, _ = discoGet(t, s, "/discovery/campaigns?q="+tok+"&town=mutare")
	if l := page.Items[0].Location; l == nil || l.Name != "Mutare" {
		t.Fatalf("card location: %+v", page.Items[0])
	}
	// ---- keyset pagination: newest first, stable, cursors bound to their sort
	_, _, p1, _ := discoGet(t, s, "/discovery/campaigns?q="+tok+"&sort=newest&limit=1")
	if len(p1.Items) != 1 || p1.Items[0].Slug != slugB || p1.NextCursor == nil {
		t.Fatalf("page 1: %+v", p1)
	}
	_, _, p2, _ := discoGet(t, s, "/discovery/campaigns?q="+tok+"&sort=newest&limit=1&cursor="+*p1.NextCursor)
	if len(p2.Items) != 1 || p2.Items[0].Slug != slugA {
		t.Fatalf("page 2: %+v", p2)
	}
	_, _, p3, _ := discoGet(t, s, "/discovery/campaigns?q="+tok+"&sort=oldest&limit=1")
	if p3.Items[0].Slug != slugA {
		t.Fatalf("oldest: %+v", p3)
	}
	if c, _, _ := discoReq(t, s, "GET", "/discovery/campaigns?q="+tok+"&sort=oldest&cursor="+*p1.NextCursor, nil); c != 422 {
		t.Fatalf("cursor of another sort accepted: %d", c)
	}
	// ---- validation and injection
	for _, bad := range []string{"?q=" + strings.Repeat("x", 101), "?sort=popular", "?status=DRAFT", "?category=x'--", "?cursor=bm9wZQ"} {
		if c, h, _ := discoReq(t, s, "GET", "/discovery/campaigns"+bad, nil); c != 422 || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", bad, c, h.Get("Cache-Control"))
		}
	}
	if c, _, p, _ := discoGet(t, s, "/discovery/search?q=%27%3B+DROP+TABLE+app.campaigns%3B--+%3A*+%26+%7C"); c != 200 || p.Match == "" {
		t.Fatalf("injection-like query: %d", c)
	}

	// ---- categories and locations with counts; the location reference list
	_, _, _, raw = discoGet(t, s, "/discovery/locations")
	assertPublicKeys(t, "locations", raw)
	if !strings.Contains(string(raw), `"slug":"manicaland"`) || !strings.Contains(string(raw), `"country":"ZW"`) {
		t.Fatalf("locations: %.400s", raw)
	}
	_, _, _, raw = discoGet(t, s, "/discovery/categories")
	assertPublicKeys(t, "discovery categories", raw)
	if c, h, raw := discoReq(t, s, "GET", "/campaign-locations", nil); c != 200 || h.Get("Cache-Control") != "public, max-age=300" ||
		!strings.Contains(string(raw), "TOWN_MUTARE") {
		t.Fatalf("location reference: %d %v", c, h)
	}
	// owner input validation: unknown location refused
	r := uB.do("GET", "/campaigns/"+idB, nil)
	uB.expect(uB.do("PATCH", "/campaigns/"+idB, map[string]any{"location_code": "TOWN_ATLANTIS"}, "If-Match", r.Header.Get("ETag")), 422, "VALIDATION_FAILED")
	// a live location edit is a material edit: the public page keeps the approved location until re-review
	r = uA.do("GET", "/campaigns/"+idA, nil)
	uA.expect(uA.do("PATCH", "/campaigns/"+idA, map[string]any{"location_code": "TOWN_GWERU"}, "If-Match", r.Header.Get("ETag")), 200, "")
	var kinds []string
	_ = mig.QueryRow(ctx(t), `SELECT material_change_types FROM app.campaign_versions WHERE campaign_id = $1 ORDER BY version_number DESC LIMIT 1`, idA).Scan(&kinds)
	if strings.Join(kinds, ",") != "LOCATION" {
		t.Fatalf("material change kinds: %v", kinds)
	}
	if _, raw := discoPublicGet(t, s, "/public/campaigns/"+slugA); !strings.Contains(string(raw), `"name":"Mutare"`) {
		t.Fatalf("approved location must stay until re-review: %.300s", raw)
	}

	// ---- engagement: dedupe, bots, caps, referral codes, owner analytics
	ref := struct {
		Code string `json:"code"`
	}{}
	c, _, raw := discoReq(t, s, "POST", "/discovery/referrals", map[string]string{"slug": slugA, "channel": "WHATSAPP"}, "User-Agent", browserUA)
	if c != 201 {
		t.Fatalf("referral: %d %s", c, raw)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	_ = json.Unmarshal(env.Data, &ref)
	if c, _, _ := discoReq(t, s, "POST", "/discovery/referrals", map[string]string{"slug": "no-such-campaign-0000000000", "channel": "WHATSAPP"}); c != 404 {
		t.Fatalf("referral for unknown campaign: %d", c)
	}
	ipBase := fmt.Sprintf("198.19.%d.", rand.Intn(200)+20)
	for i := 1; i <= 4; i++ { // four distinct visitors, each viewing twice; visitor 1 arrives through the referral link
		ev := map[string]string{"type": "CAMPAIGN_VIEW", "slug": slugA, "source": "SEARCH"}
		if i == 1 {
			ev["ref"] = ref.Code
			ev["source"] = "DIRECT"
		}
		if code := view(t, s, ipBase+fmt.Sprint(i), ev, ev); code != 204 {
			t.Fatalf("events: %d", code)
		}
	}
	view(t, s, ipBase+"1", map[string]string{"type": "SHARE_CLICK", "slug": slugA, "channel": "WHATSAPP"},
		map[string]string{"type": "RESULT_CLICK", "slug": slugA, "source": "SEARCH"})
	view(t, s, ipBase+"2", map[string]string{"type": "SHARE_CLICK", "slug": slugA, "channel": "COPY_LINK"})
	// automated clients and invalid referral codes: accepted silently, never counted as engagement
	discoReq(t, s, "POST", "/discovery/events", map[string]any{"events": []map[string]string{{"type": "CAMPAIGN_VIEW", "slug": slugA}}},
		"X-Forwarded-For", ipBase+"9", "User-Agent", "WhatsApp/2.23.20.0 A")
	view(t, s, ipBase+"3", map[string]string{"type": "CAMPAIGN_VIEW", "slug": slugA, "ref": "0000000000ab"})
	// a visitor over the daily cap
	burst := make([]map[string]string, 20)
	for i := range burst {
		burst[i] = map[string]string{"type": "CAMPAIGN_VIEW", "slug": slugA, "source": "DIRECT"}
	}
	view(t, s, ipBase+"7", burst...)
	view(t, s, ipBase+"7", burst[:5]...)
	flushDiscovery(t, s)

	r = uA.do("GET", "/campaigns/"+idA+"/analytics?days=7", nil)
	uA.expect(r, 200, "")
	var an struct {
		Totals    map[string]int64 `json:"totals"`
		Shares    map[string]int64 `json:"shares_by_channel"`
		Referrals map[string]int64 `json:"referral_visits_by_channel"`
		Sources   map[string]int64 `json:"traffic_sources"`
		Nature    string           `json:"nature"`
		Donations map[string]any   `json:"donations"`
	}
	_ = json.Unmarshal(r.Data, &an)
	// views: 4 visitors × 2 + visitor 3's invalid-referral view + 20 (capped) for visitor 7 = 29; unique: 5
	if an.Totals["views"] != 29 || an.Totals["unique_visitors"] != 5 || an.Totals["bot_views_excluded"] != 1 {
		t.Fatalf("view totals: %+v", an.Totals)
	}
	if an.Shares["WHATSAPP"] != 1 || an.Shares["COPY_LINK"] != 1 || an.Referrals["WHATSAPP"] != 2 || an.Totals["clicks"] != 1 {
		t.Fatalf("shares/referrals/clicks: %+v %+v %+v", an.Shares, an.Referrals, an.Totals)
	}
	if an.Nature != "MEASURED_NON_PRODUCTION" || an.Donations["available"] != false {
		t.Fatalf("labels: %s %v", an.Nature, an.Donations)
	}
	var capHits, invalid int
	_ = mig.QueryRow(ctx(t), `SELECT coalesce(sum(count) FILTER (WHERE kind = 'VISITOR_CAP'), 0), coalesce(sum(count) FILTER (WHERE kind = 'INVALID_REFERRAL'), 0)
		FROM app.discovery_abuse_daily WHERE campaign_id = $1`, idA).Scan(&capHits, &invalid)
	if capHits != 5 || invalid != 1 {
		t.Fatalf("abuse indicators: cap %d invalid %d", capHits, invalid)
	}
	// nothing identifying is stored: no address, user agent or referral code in the aggregates; keys are 32-byte hashes
	var leaks int
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.discovery_campaign_daily WHERE campaign_id = $1 AND (channel LIKE '198.%' OR channel ILIKE '%mozilla%')`,
		idA).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("identifying data in aggregates")
	}
	// other people cannot read the analytics (existence not revealed)
	uB.expect(uB.do("GET", "/campaigns/"+idA+"/analytics", nil), 404, "CAMPAIGN_NOT_FOUND")
	s.browser(t).expect(s.browser(t).do("GET", "/campaigns/"+idA+"/analytics", nil), 401, "")
	// revoking the referral code: later visits are not counted as referrals
	admin := s.staff(t, f.ledgerAdmin) // ADMIN
	admin.expect(admin.do("POST", "/admin/discovery/referrals/"+ref.Code+"/revoke", map[string]string{"note": "IT: abuse response"}), 200, "")
	view(t, s, ipBase+"5", map[string]string{"type": "CAMPAIGN_VIEW", "slug": slugA, "ref": ref.Code})
	flushDiscovery(t, s)
	_ = json.Unmarshal(uA.do("GET", "/campaigns/"+idA+"/analytics?days=7", nil).Data, &an)
	if an.Referrals["WHATSAPP"] != 2 {
		t.Fatalf("revoked code still counted: %+v", an.Referrals)
	}

	// ---- trending: ≥ 3 unique visitors → ranked (computed in-process; the worker job does the same every 15 min)
	if _, err := s.d.Discovery.ComputeTrending(ctx(t)); err != nil {
		t.Fatal(err)
	}
	var eligible bool
	var comps string
	_ = mig.QueryRow(ctx(t), `SELECT rank_eligible, components::text FROM app.discovery_trending_scores WHERE campaign_id = $1`, idA).Scan(&eligible, &comps)
	if !eligible || !strings.Contains(comps, `"unique_visitors": 6`) && !strings.Contains(comps, `"unique_visitors":6`) {
		t.Fatalf("trending A: %v %s", eligible, comps)
	}
	_ = mig.QueryRow(ctx(t), `SELECT rank_eligible FROM app.discovery_trending_scores WHERE campaign_id = $1`, idB).Scan(&eligible)
	if eligible {
		t.Fatal("B has no engagement but is ranked")
	}
	_, _, page, raw = discoGet(t, s, "/discovery/trending?limit=24")
	assertPublicKeys(t, "trending", raw)
	if !page.has(slugA) || !strings.Contains(string(raw), "Money is never part of the ranking") {
		t.Fatalf("trending list: %.500s", raw)
	}
	_, _, page, _ = discoGet(t, s, "/discovery/campaigns?q="+tok+"&sort=trending")
	if page.Items[0].Slug != slugA {
		t.Fatalf("trending sort: %+v", page.Items)
	}

	// ---- recommendations: rule-based, never personalised, the viewed campaign excluded
	_, _, page, raw = discoGet(t, s, "/discovery/recommendations?campaign="+slugB+"&limit=12")
	assertPublicKeys(t, "recommendations", raw)
	if page.Personalised == nil || *page.Personalised || page.has(slugB) || len(page.Items) == 0 {
		t.Fatalf("recommendations: %.500s", raw)
	}
	for _, it := range page.Items {
		if it.Reason == nil || it.Reason.Text == "" {
			t.Fatalf("recommendation without a reason: %+v", it)
		}
	}

	// ---- featured placements: maker-checker
	rev := s.staff(t, f.campRev1)
	comp, comp2 := s.staff(t, f.compliance), s.staff(t, f.compliance2)
	now := time.Now().UTC()
	r = rev.do("POST", "/admin/discovery/featured", map[string]any{"campaign_id": idA, "position": 1, "reason": "IT: editorial pick for the week",
		"ends_at": now.Add(48 * time.Hour)}, "Idempotency-Key", ids.New())
	rev.expect(r, 201, "")
	pid := r.field(t, "id")
	rev.expect(rev.do("POST", "/admin/discovery/featured", map[string]any{"campaign_id": idA, "position": 2, "reason": "IT: second open request",
		"ends_at": now.Add(48 * time.Hour)}, "Idempotency-Key", ids.New()), 409, "PLACEMENT_EXISTS")
	rev.expect(rev.do("POST", "/admin/discovery/featured/"+pid+"/approve", map[string]string{"note": "self"}), 403, "PERMISSION_DENIED")
	if _, _, p, _ := discoGet(t, s, "/discovery/featured?fill=false&limit=12"); p.has(slugA) {
		t.Fatal("requested placement shown before approval")
	}
	// the database refuses a decision by the requester even if Go were bypassed
	if _, err := mig.Exec(ctx(t), `UPDATE app.discovery_featured_placements SET status = 'APPROVED', decided_by = requested_by, decided_at = now(),
		decision_note = 'bypass' WHERE id = $1`, pid); err == nil || !strings.Contains(err.Error(), "check") {
		t.Fatalf("database maker-checker guard: %v", err)
	}
	comp.expect(comp.do("POST", "/admin/discovery/featured/"+pid+"/approve", map[string]string{"note": "IT: checked the campaign and the reason"}), 200, "")
	_, _, page, raw = discoGet(t, s, "/discovery/featured?fill=false&limit=12")
	if !page.has(slugA) || !strings.Contains(string(raw), `"selection":"FEATURED"`) || !strings.Contains(string(raw), "Featured by FundZim") {
		t.Fatalf("featured: %.500s", raw)
	}
	if _, _, p, _ := discoGet(t, s, "/public/campaigns/featured?limit=12"); !p.has(slugA) {
		t.Fatal("Stage 7 featured alias lacks the placement")
	}

	// ---- risk hold: not promotable (featured, trending, recommendations) but still searchable
	h := comp.do("POST", "/admin/risk/holds", map[string]any{"hold_type": "DONATION_HOLD", "subject_type": "CAMPAIGN", "subject_id": idA,
		"reason_code": "IT_REVIEW", "description": "IT: discovery promotion check"}, "Idempotency-Key", ids.New())
	comp.expect(h, 201, "")
	comp2.expect(comp2.do("POST", "/admin/risk/holds/"+h.field(t, "id")+"/activate", map[string]any{"reason": "IT: confirmed"}, "Idempotency-Key", ids.New()), 200, "")
	if _, _, p, _ := discoGet(t, s, "/discovery/featured?fill=false&limit=12"); p.has(slugA) {
		t.Fatal("held campaign still featured")
	}
	if _, _, p, _ := discoGet(t, s, "/discovery/trending?limit=24"); p.has(slugA) {
		t.Fatal("held campaign still trending")
	}
	if _, _, p, _ := discoGet(t, s, "/discovery/recommendations?campaign="+slugB+"&limit=12"); p.has(slugA) {
		t.Fatal("held campaign still recommended")
	}
	if _, _, p, _ := discoGet(t, s, "/discovery/search?q="+tok); !p.has(slugA) {
		t.Fatal("a hold must not remove the campaign from search")
	}

	// ---- compliance RESTRICT on B's owner: B leaves discovery at once (serve time), its page stays (ADR-036 §6)
	c1, c2 := comp, comp2
	cr := c1.do("POST", "/admin/compliance/cases", map[string]any{"case_type": "OTHER", "severity": "S3", "note": "IT: discovery restriction check",
		"subjects": []map[string]string{{"subject_type": "USER", "subject_id": uB.id, "role": "PRIMARY_SUBJECT"}}, "reason_code": "MANUAL_REFERRAL"})
	c1.expect(cr, 201, "")
	caseID := cr.field(t, "id")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/assign", map[string]string{}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/start", map[string]string{}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/resolve", map[string]string{"decision": "RESTRICT", "reason_code": "POLICY_BREACH",
		"note": "IT: restrict while reviewing"}), 202, "")
	c2.expect(c2.do("POST", "/admin/compliance/cases/"+caseID+"/approve-resolution", map[string]string{"note": "IT: second officer"}), 200, "")
	// immediately (before the index consumer runs): no search result, no sitemap entry, no legacy listing
	for _, path := range []string{"/discovery/search?q=" + tok, "/discovery/sitemap", "/public/campaigns?q=" + tok, "/public/sitemap",
		"/discovery/recommendations?campaign=" + slugA + "&limit=12"} {
		if _, _, body := discoReq(t, s, "GET", path, nil); strings.Contains(string(body), slugB) {
			t.Fatalf("%s lists a restricted campaign", path)
		}
	}
	if c, _ := discoPublicGet(t, s, "/public/campaigns/"+slugB); c != 200 {
		t.Fatalf("RESTRICTED (not suspended) campaign page: %d", c)
	}
	waitFor(t, 60*time.Second, "restricted campaign removed from the index", func() bool {
		var d bool
		_ = mig.QueryRow(ctx(t), `SELECT discoverable FROM app.discovery_campaign_index WHERE campaign_id = $1`, idB).Scan(&d)
		return !d
	})

	// ---- suspension of A: gone from every surface; then a deliberately stale row must not leak
	rev.expect(rev.do("POST", "/admin/campaigns/"+idA+"/suspend", map[string]string{"reason_code": "IT_REVIEW", "note": "IT: suspended for the discovery test"}), 200, "")
	for _, path := range []string{"/discovery/search?q=" + tok, "/discovery/featured?limit=12", "/discovery/trending?limit=24", "/discovery/sitemap"} {
		if _, _, body := discoReq(t, s, "GET", path, nil); strings.Contains(string(body), slugA) {
			t.Fatalf("%s lists a suspended campaign", path)
		}
	}
	waitFor(t, 60*time.Second, "suspended campaign removed from the index", func() bool {
		var d bool
		_ = mig.QueryRow(ctx(t), `SELECT discoverable FROM app.discovery_campaign_index WHERE campaign_id = $1`, idA).Scan(&d)
		return !d
	})
	if _, err := mig.Exec(ctx(t), `UPDATE app.discovery_campaign_index SET discoverable = true, slug = $2, title = $3, status = 'ACTIVE', category_code = 'EDUCATION',
		published_at = now(), source_version_id = (SELECT approved_version_id FROM app.campaigns WHERE id = $1), organiser_type = 'INDIVIDUAL',
		search_doc = to_tsvector('simple', $3), title_norm = lower($3) WHERE campaign_id = $1`, idA, slugA, "stale "+tok); err != nil {
		t.Fatal(err)
	}
	if _, _, p, raw := discoGet(t, s, "/discovery/search?q="+tok); p.has(slugA) {
		t.Fatalf("a stale index row exposed a suspended campaign: %.300s", raw)
	}
	res, err := s.d.Discovery.Reconcile(ctx(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	var stillOn bool
	_ = mig.QueryRow(ctx(t), `SELECT discoverable FROM app.discovery_campaign_index WHERE campaign_id = $1`, idA).Scan(&stillOn)
	if stillOn || res.Reindexed < 1 {
		t.Fatalf("reconciliation did not repair the stale row (%+v)", res)
	}
	// staff see the placement as not promotable; removal ends it
	r = comp.do("GET", "/admin/discovery/featured?status=APPROVED", nil)
	if !strings.Contains(string(r.Data), pid) || !strings.Contains(string(r.Data), `"promotable":false`) {
		t.Fatalf("staff placement view: %.500s", r.Data)
	}
	rev.expect(rev.do("POST", "/admin/discovery/featured/"+pid+"/remove", map[string]string{"note": "IT: campaign suspended"}), 200, "")
}

// TestDiscoveryGovernanceAndRBAC: ranking policy maker-checker, staff permissions, overview, and that nothing in the
// staff views carries personal data.
func TestDiscoveryGovernanceAndRBAC(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	admin, comp, rev, support := s.staff(t, f.ledgerAdmin), s.staff(t, f.compliance), s.staff(t, f.campRev1), s.staff(t, f.support) // ledgerAdmin: ADMIN
	user := verifiedUser(t, s, "disco-gov")

	r := admin.do("GET", "/admin/discovery/overview", nil)
	admin.expect(r, 200, "")
	for _, k := range []string{`"index"`, `"search_7d_by_outcome"`, `"abuse_7d_by_kind"`, `"ranking"`, `"definitions"`} {
		if !strings.Contains(string(r.Data), k) {
			t.Fatalf("overview lacks %s", k)
		}
	}
	if r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("overview cache header")
	}
	support.expect(support.do("GET", "/admin/discovery/overview", nil), 403, "PERMISSION_DENIED")
	user.expect(user.do("GET", "/admin/discovery/overview", nil), 404, "")
	rev.expect(rev.do("POST", "/admin/discovery/ranking", map[string]any{"weights": discovery.DefaultWeights, "reason": "IT: reviewer cannot propose"},
		"Idempotency-Key", ids.New()), 403, "PERMISSION_DENIED")

	// propose (weights equal to the engineering default, so other tests see no behavioural change)
	r = admin.do("POST", "/admin/discovery/ranking", map[string]any{"weights": discovery.DefaultWeights, "reason": "IT: formalise the engineering default"},
		"Idempotency-Key", ids.New())
	admin.expect(r, 201, "")
	pid := r.field(t, "id")
	if r.field(t, "status") != "SUBMITTED" || len(r.field(t, "content_sha256")) != 64 {
		t.Fatalf("proposal: %s", r.Data)
	}
	admin.expect(admin.do("POST", "/admin/discovery/ranking", map[string]any{"weights": map[string]int{"view_weight": 500, "share_weight": 1,
		"freshness_half_life_hours": 24, "window_days": 7, "min_unique_visitors": 3}, "reason": "IT: out of bounds"}, "Idempotency-Key", ids.New()), 422, "VALIDATION_FAILED")
	admin.expect(admin.do("POST", "/admin/discovery/ranking/"+pid+"/approve", map[string]string{"note": "self"}), 403, "PERMISSION_DENIED")
	// weights are immutable once submitted (database)
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	if _, err := mig.Exec(ctx(t), `UPDATE app.discovery_ranking_policies SET weights = jsonb_set(weights, '{view_weight}', '99') WHERE id = $1`, pid); err == nil {
		t.Fatal("submitted weights were changed")
	}
	comp.expect(comp.do("POST", "/admin/discovery/ranking/"+pid+"/approve", map[string]string{"note": "IT: checked against the default"}), 200, "")
	r = comp.do("GET", "/admin/discovery/ranking", nil)
	if !strings.Contains(string(r.Data), `"in_force_source":"APPROVED_POLICY"`) {
		t.Fatalf("ranking in force: %.300s", r.Data)
	}
	comp.expect(comp.do("POST", "/admin/discovery/ranking/"+pid+"/reject", map[string]string{"note": "too late"}), 409, "INVALID_STATE")
	// the operations portal shows discovery as a source
	r = admin.do("GET", "/admin/operations/metrics", nil)
	if r.Status == 200 && !strings.Contains(string(r.Data), "discovery.index.backlog") {
		t.Fatalf("operations metrics lack discovery: %.300s", r.Data)
	}
}

// TestDiscoveryFailureInjectionAndAbuse: the eligibility registry down → discovery and the public page fail closed (503,
// never cached, never unfiltered); scraping is rate limited with an accurate Retry-After.
func TestDiscoveryFailureInjectionAndAbuse(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	tok := discoWord()
	_, id, slug := discoCampaign(t, s, f, "disco-fail", tok, "")
	if _, err := s.d.Discovery.Reindex(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	orig := s.d.Verification.Campaigns.Restrictions
	s.d.Verification.Campaigns.Restrictions = failingRestrictions{}
	code, hdr, _, raw := discoGet(t, s, "/discovery/search?q="+tok)
	if code != 503 || hdr.Get("Cache-Control") != "no-store" || !strings.Contains(string(raw), "DISCOVERY_UNAVAILABLE") || strings.Contains(string(raw), slug) {
		t.Fatalf("search with the restriction registry down: %d %v %s", code, hdr, raw)
	}
	if c, _ := discoPublicGet(t, s, "/public/campaigns/"+slug); c != 503 {
		t.Fatalf("public page with the registry down: %d", c)
	}
	// indexing fails closed too: the row is marked dirty, never made discoverable without a check
	if _, err := s.d.Discovery.Reindex(ctx(t), id); err == nil {
		t.Fatal("reindex succeeded without an eligibility check")
	}
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	var dirty *time.Time
	var lastErr *string
	_ = mig.QueryRow(ctx(t), `SELECT dirty_since, last_error FROM app.discovery_campaign_index WHERE campaign_id = $1`, id).Scan(&dirty, &lastErr)
	if dirty == nil || lastErr == nil || *lastErr != "ELIGIBILITY_UNAVAILABLE" {
		t.Fatalf("dirty marker: %v %v", dirty, lastErr)
	}
	s.d.Verification.Campaigns.Restrictions = orig
	if _, _, p, _ := discoGet(t, s, "/discovery/search?q="+tok); !p.has(slug) {
		t.Fatal("search did not recover")
	}
	if _, err := s.d.Discovery.Reindex(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	_ = mig.QueryRow(ctx(t), `SELECT dirty_since FROM app.discovery_campaign_index WHERE campaign_id = $1`, id).Scan(&dirty)
	if dirty != nil {
		t.Fatal("dirty marker not cleared after recovery")
	}
	// scraping: one address is limited (120/min) with the limiter's real wait
	got429 := false
	for i := 0; i < 140 && !got429; i++ {
		c, h, _ := discoReq(t, s, "GET", "/discovery/campaigns?limit=1", nil, "X-Forwarded-For", "198.51.100.77")
		if c == 429 {
			got429 = true
			if h.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	if !got429 {
		t.Fatal("discovery reads are not rate limited")
	}
}

// TestDiscoveryConcurrency: concurrent reindexes of one campaign converge; concurrent events from one visitor count once.
func TestDiscoveryConcurrency(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	tok := discoWord()
	owner, id, slug := discoCampaign(t, s, f, "disco-conc", tok, "TOWN_GWERU")
	var wg sync.WaitGroup
	errsCh := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.d.Discovery.Reindex(ctx(t), id); err != nil {
				errsCh <- err
			}
		}()
	}
	// a suspension racing with the reindexes: the final state must be "not discoverable"
	rev := s.staff(t, f.campRev1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		if r := rev.do("POST", "/admin/campaigns/"+id+"/suspend", map[string]string{"reason_code": "IT_RACE", "note": "IT: suspend during reindexing"}); r.Status != 200 {
			errsCh <- fmt.Errorf("suspend: %d", r.Status)
		}
	}()
	wg.Wait()
	close(errsCh)
	for err := range errsCh {
		t.Fatal(err)
	}
	if _, err := s.d.Discovery.Reindex(ctx(t), id); err != nil { // the event consumer would do the same
		t.Fatal(err)
	}
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	var d bool
	_ = mig.QueryRow(ctx(t), `SELECT discoverable FROM app.discovery_campaign_index WHERE campaign_id = $1`, id).Scan(&d)
	if d {
		t.Fatal("suspended campaign discoverable after concurrent reindexing")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/reactivate", map[string]string{"reason_code": "IT_RACE", "note": "IT: reactivated"}), 200, "")
	waitFor(t, 60*time.Second, "reactivated campaign back in search", func() bool {
		_, _, p, _ := discoGet(t, s, "/discovery/search?q="+tok)
		return p.has(slug)
	})

	// 30 concurrent identical views from one visitor: one unique visitor, raw capped at 20
	ip := fmt.Sprintf("198.18.250.%d", rand.Intn(200)+20)
	var wg2 sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			view(t, s, ip, map[string]string{"type": "CAMPAIGN_VIEW", "slug": slug, "source": "DIRECT"})
		}()
	}
	wg2.Wait()
	flushDiscovery(t, s)
	var an struct {
		Totals map[string]int64 `json:"totals"`
	}
	_ = json.Unmarshal(owner.do("GET", "/campaigns/"+id+"/analytics?days=1", nil).Data, &an)
	if an.Totals["unique_visitors"] != 1 || an.Totals["views"] != 20 {
		t.Fatalf("concurrent views: %+v", an.Totals)
	}
	// concurrent placement requests: one open placement
	codes := parallelDo(5, func(int) int {
		return rev.do("POST", "/admin/discovery/featured", map[string]any{"campaign_id": id, "position": 5, "reason": "IT: concurrent requests",
			"ends_at": time.Now().UTC().Add(24 * time.Hour)}, "Idempotency-Key", ids.New()).Status
	})
	if codes[201] != 1 || codes[409] != 4 {
		t.Fatalf("concurrent placement requests: %v", codes)
	}
	var pid string
	_ = mig.QueryRow(ctx(t), `SELECT id FROM app.discovery_featured_placements WHERE campaign_id = $1 AND status = 'REQUESTED'`, id).Scan(&pid)
	rev.expect(rev.do("POST", "/admin/discovery/featured/"+pid+"/cancel", map[string]string{"note": "IT: cleanup"}), 200, "")
}

var _ = campaigns.ErrNotFound
