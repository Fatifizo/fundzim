//go:build integration

// Stage 7 public experience (ADR-038) against the real stack: API consistency (428/412, {items, next_cursor}),
// search counts and validation, featured selection, canonical slugs and short codes, categories, cache headers
// and immediate disappearance, reports (honeypot, dedupe, rate limit, triage, escalation), first-party event
// counters, sitemap data and a recursive allow-list of every public response.
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func publicReq(t *testing.T, s *itServer, method, path string, body any, hdr ...string) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, s.srv.URL+"/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// publicAllowedKeys is every JSON key a public endpoint may return (ADR-038; contract §3). Anything else —
// owner/beneficiary/reviewer ids, emails, KYC, risk, compliance, notes, documents — fails the test.
var publicAllowedKeys = map[string]bool{
	"data": true, "meta": true, "request_id": true, "items": true, "next_cursor": true, "total": true, "value": true, "capped": true,
	"slug": true, "canonical_slug": true, "public_code": true, "title": true, "summary": true, "story": true, "category": true,
	"category_name": true, "goal": true, "amount_minor": true, "currency": true, "status": true, "organiser": true, "type": true,
	"display_name": true, "beneficiary": true, "disclosed": true, "cover_media_id": true, "media_ids": true, "media": true, "id": true,
	"kind": true, "alt_text": true, "published_at": true, "completed_at": true, "created_at": true, "donations": true, "available": true,
	"message": true, "trust": true, "organiser_identity_verified": true, "organisation_verified": true, "beneficiary_verified": true,
	"campaign_reviewed": true, "selection": true, "code": true, "name": true, "description": true, "display_order": true, "sensitive": true,
	"live_campaigns": true, "campaigns": true, "categories": true, "updated_at": true, "body": true, "organiser_name": true,
	"requires_organisation": true, "risk_tier": false, "variants": true, "width": true, "height": true,
}

func assertPublicKeys(t *testing.T, where string, raw []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	var bad []string
	var walk func(any)
	walk = func(x any) {
		switch y := x.(type) {
		case map[string]any:
			for k, val := range y {
				if !publicAllowedKeys[k] {
					bad = append(bad, k)
				}
				walk(val)
			}
		case []any:
			for _, e := range y {
				walk(e)
			}
		}
	}
	walk(v)
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("%s returns non-allow-listed keys %v: %.500s", where, bad, raw)
	}
}

func TestPublicExperienceEndpoints(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	tok := fmt.Sprintf("pubx%d", time.Now().UnixNano()%1_000_000_000) // unique search token in the shared database
	u, id := readyCampaign(t, s, f, tok, "EDUCATION")
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u, id)
	slug := u.do("GET", "/campaigns/"+id, nil).field(t, "slug")
	code := slug[strings.LastIndex(slug, "-")+1:]

	// ---- API consistency (ADR-038 §7)
	r := u.do("GET", "/campaigns/mine?limit=1", nil)
	u.expect(r, 200, "")
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(r.Data, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("mine envelope: %v %s", err, r.Data)
	}
	r = u.do("GET", "/campaigns/"+id, nil)
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"visibility": "PUBLIC"}), 428, "PRECONDITION_REQUIRED")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"visibility": "PUBLIC"}, "If-Match", `"abc"`), 428, "PRECONDITION_REQUIRED")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"visibility": "PUBLIC"}, "If-Match", `"1"`), 412, "PRECONDITION_FAILED")
	r = u.do("GET", "/campaigns/"+id+"/media", nil)
	if !strings.Contains(string(r.Data), `"items"`) || !strings.Contains(string(r.Data), `"next_cursor":null`) {
		t.Fatalf("media list envelope: %s", r.Data)
	}

	// ---- detail: canonical slug, trust, cache headers, conditional GET, allow-list
	code1, hdr, body := publicReq(t, s, "GET", "/public/campaigns/"+slug, nil)
	if code1 != 200 || hdr.Get("Cache-Control") != "public, no-cache" || hdr.Get("ETag") == "" {
		t.Fatalf("detail: %d %v", code1, hdr)
	}
	assertPublicKeys(t, "detail", body)
	for _, want := range []string{`"canonical_slug":"` + slug + `"`, `"campaign_reviewed":true`, `"organiser_identity_verified":true`,
		`"beneficiary_verified":true`, `"public_code":"` + code + `"`, `"category_name":"Education"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("detail lacks %s: %s", want, body)
		}
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/campaigns/"+slug, nil, "If-None-Match", hdr.Get("ETag")); c != 304 {
		t.Fatalf("conditional GET: %d", c)
	}
	// a wrong title part with the right public code resolves to the canonical slug; codes resolve
	_, _, body = publicReq(t, s, "GET", "/public/campaigns/old-title-"+code, nil)
	if !strings.Contains(string(body), `"canonical_slug":"`+slug+`"`) {
		t.Fatalf("canonical fallback: %s", body)
	}
	_, _, body = publicReq(t, s, "GET", "/public/campaign-codes/"+strings.ToUpper(code), nil)
	if !strings.Contains(string(body), slug) {
		t.Fatalf("short code: %s", body)
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/campaign-codes/0000000000", nil); c != 404 {
		t.Fatal("unknown code should 404")
	}

	// ---- search: totals, category slug, validation, allow-list
	c2, hdr, body := publicReq(t, s, "GET", "/public/campaigns?q="+tok+"&category=education", nil)
	if c2 != 200 || !strings.Contains(string(body), slug) || !strings.Contains(string(body), `"total":{"value":1,"capped":false}`) {
		t.Fatalf("search by category slug: %d %s", c2, body)
	}
	assertPublicKeys(t, "search", body)
	if hdr.Get("Cache-Control") != "public, no-cache" {
		t.Fatal("search cache header")
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/campaigns?q=a%00b", nil); c != 422 {
		t.Fatalf("control characters in q: %d", c)
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/campaigns?q="+strings.Repeat("x", 101), nil); c != 422 {
		t.Fatalf("overlong q: %d", c)
	}
	if c, _, body := publicReq(t, s, "GET", "/public/campaigns?q=%27%3B+DROP+TABLE+app.campaigns%3B--", nil); c != 200 || !strings.Contains(string(body), `"items":[]`) {
		t.Fatalf("injection-like query: %d %s", c, body)
	}

	// ---- categories and featured
	_, hdr, body = publicReq(t, s, "GET", "/public/categories", nil)
	assertPublicKeys(t, "categories", body)
	if !strings.Contains(string(body), `"slug":"education"`) || hdr.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("categories: %v %s", hdr, body)
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/categories/no-such-category", nil); c != 404 {
		t.Fatal("unknown category should 404")
	}
	comp := s.staff(t, f.compliance)
	comp.expect(comp.do("POST", "/admin/campaigns/"+id+"/feature", map[string]any{"position": 1, "note": "integration test pick"}), 200, "")
	comp.expect(comp.do("POST", "/admin/campaigns/"+id+"/feature", map[string]any{"position": 1, "note": "integration test pick"}), 409, "ALREADY_FEATURED")
	_, _, body = publicReq(t, s, "GET", "/public/campaigns/featured?limit=12", nil)
	assertPublicKeys(t, "featured", body)
	if !strings.Contains(string(body), `"slug":"`+slug+`","title"`) || !strings.Contains(string(body), "STAFF_PICK") {
		t.Fatalf("featured: %s", body)
	}
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/feature", map[string]any{"position": 1, "note": "no permission"}), 403, "PERMISSION_DENIED")

	// ---- events: aggregate counters only
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	count := func(typ, subject string) int {
		var n int
		_ = mig.QueryRow(ctx(t), `SELECT coalesce(sum(count),0) FROM app.public_event_counters WHERE event_type = $1 AND subject = $2`, typ, subject).Scan(&n)
		return n
	}
	before := count("CAMPAIGN_PAGE_VIEW", slug)
	if c, _, _ := publicReq(t, s, "POST", "/public/events", map[string]string{"type": "CAMPAIGN_PAGE_VIEW", "slug": slug}); c != 204 {
		t.Fatalf("event: %d", c)
	}
	if count("CAMPAIGN_PAGE_VIEW", slug) != before+1 {
		t.Fatal("page view not counted")
	}
	if c, _, _ := publicReq(t, s, "POST", "/public/events", map[string]string{"type": "CAMPAIGN_PAGE_VIEW", "slug": "unknown-slug-0000000000"}); c != 204 {
		t.Fatal("unknown slug must be ignored silently")
	}
	if c, _, _ := publicReq(t, s, "POST", "/public/events", map[string]string{"type": "CAMPAIGN_SHARE_CLICK", "slug": slug, "channel": "CARRIER_PIGEON"}); c != 422 {
		t.Fatal("invalid channel accepted")
	}
	publicReq(t, s, "POST", "/public/events", map[string]string{"type": "CAMPAIGN_SEARCH", "category": "education", "slug": tok})
	var leaked int
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.public_event_counters WHERE subject ILIKE '%' || $1 || '%' AND event_type = 'CAMPAIGN_SEARCH'`, tok).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("search text stored")
	}

	// ---- reports: honeypot, dedupe, never changes status, triage and escalation
	anon := s.browser(t)
	r = anon.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "MISLEADING_INFORMATION", "details": "The story does not match the photos.",
		"contact_email": "Reporter@Example.test"})
	anon.expect(r, 202, "")
	anon.expect(anon.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "MISLEADING_INFORMATION"}), 202, "") // duplicate today: silent
	anon.expect(anon.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "OTHER", "details": "spam", "website": "http://spam"}), 422, "VALIDATION_FAILED")
	anon.expect(anon.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "OTHER", "details": "this is a bot submission", "website": "http://spam"}), 202, "")
	anon.expect(anon.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "<b>"}), 422, "VALIDATION_FAILED")
	other := s.browser(t) // separate client IP: every attempt counts toward the per-IP report limit
	other.expect(other.do("POST", "/public/campaigns/does-not-exist-0000000000/reports", map[string]any{"reason": "OTHER", "details": "unknown campaign here"}), 404, "")
	var reports, withContact int
	_ = mig.QueryRow(ctx(t), `SELECT count(*), count(contact_email_ct) FROM app.campaign_reports WHERE campaign_id = $1`, id).Scan(&reports, &withContact)
	if reports != 1 || withContact != 1 {
		t.Fatalf("reports stored: %d (contact %d) — honeypot and duplicates must not be stored", reports, withContact)
	}
	var ct []byte
	_ = mig.QueryRow(ctx(t), `SELECT contact_email_ct FROM app.campaign_reports WHERE campaign_id = $1`, id).Scan(&ct)
	if strings.Contains(string(ct), "example.test") {
		t.Fatal("contact email stored in clear text")
	}
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "ACTIVE" {
		t.Fatal("a report changed the campaign status")
	}
	r = rev.do("GET", "/admin/campaigns/reports?status=OPEN", nil)
	rev.expect(r, 200, "")
	if !strings.Contains(string(r.Data), id) || strings.Contains(strings.ToLower(string(r.Data)), "example.test") || !strings.Contains(string(r.Data), `"has_contact":true`) {
		t.Fatalf("staff report list: %.600s", r.Data)
	}
	var rep struct {
		Items []struct {
			ID         string `json:"id"`
			CampaignID string `json:"campaign_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(r.Data, &rep)
	reportID := ""
	for _, x := range rep.Items {
		if x.CampaignID == id {
			reportID = x.ID
		}
	}
	u.expect(u.do("GET", "/admin/campaigns/reports", nil), 404, "") // owners never see reports
	rev.expect(rev.do("POST", "/admin/campaigns/reports/"+reportID+"/escalate", map[string]string{"reason_code": "NEEDS_INVESTIGATION",
		"note": "the reporter's concern is specific enough to investigate"}), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/reports/"+reportID+"/dismiss", map[string]string{"reason_code": "DUPLICATE", "note": "already escalated"}), 409, "REPORT_ALREADY_TRIAGED")
	waitFor(t, 60*time.Second, "compliance case from the escalated report", func() bool {
		var n int
		_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM compliance.compliance_case_links l JOIN compliance.compliance_cases c ON c.id = l.case_id
			WHERE l.subject_type = 'CAMPAIGN' AND l.subject_id = $1 AND c.source = 'REPORT'`, id).Scan(&n)
		return n > 0
	})
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "ACTIVE" {
		t.Fatal("escalation changed the campaign status")
	}

	// ---- dismissal: a second reporter's report is dismissed; no escalation event, no status change, final
	other.expect(other.do("POST", "/public/campaigns/"+slug+"/reports", map[string]any{"reason": "SUSPICIOUS_ACTIVITY", "details": "The organiser asked me to pay outside FundZim."}), 202, "")
	dismissID := ""
	_ = mig.QueryRow(ctx(t), `SELECT id FROM app.campaign_reports WHERE campaign_id = $1 AND status = 'OPEN'`, id).Scan(&dismissID)
	if dismissID == "" {
		t.Fatal("second report not stored")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/reports/"+dismissID+"/dismiss", map[string]string{"reason_code": "NOT_ACTIONABLE",
		"note": "checked: payment instructions are not in the campaign text"}), 200, "")
	var dismissedStatus string
	var triagedBy *string
	_ = mig.QueryRow(ctx(t), `SELECT status, triaged_by::text FROM app.campaign_reports WHERE id = $1`, dismissID).Scan(&dismissedStatus, &triagedBy)
	if dismissedStatus != "DISMISSED" || triagedBy == nil {
		t.Fatalf("dismissed report: status %s, triaged_by set %v", dismissedStatus, triagedBy != nil)
	}
	var dismissEvents int
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE event_type = 'campaigns.report_escalated' AND payload->>'report_id' = $1`, dismissID).Scan(&dismissEvents)
	if dismissEvents != 0 {
		t.Fatal("a dismissed report emitted an escalation event")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/reports/"+dismissID+"/escalate", map[string]string{"reason_code": "NEEDS_INVESTIGATION", "note": "second look"}), 409, "REPORT_ALREADY_TRIAGED")
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "ACTIVE" {
		t.Fatal("dismissal changed the campaign status")
	}

	// ---- report rate limit per IP (5/hour)
	limited := s.browser(t)
	u2, id2 := readyCampaign(t, s, f, "pubx-b", "EDUCATION")
	u2.expect(u2.do("POST", "/campaigns/"+id2+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u2, id2)
	slug2 := u2.do("GET", "/campaigns/"+id2, nil).field(t, "slug")
	got429 := false
	for i := 0; i < 7; i++ {
		r := limited.do("POST", "/public/campaigns/"+slug2+"/reports", map[string]any{"reason": "OTHER", "details": fmt.Sprintf("report number %d about this", i)})
		if r.Status == 429 {
			got429 = true
			if ra, _ := strconv.Atoi(r.Header.Get("Retry-After")); ra < 60 {
				t.Fatalf("Retry-After %q for an hourly report limit", r.Header.Get("Retry-After"))
			}
			break
		}
	}
	if !got429 {
		t.Fatal("report rate limit not enforced")
	}

	// ---- sitemap, then suspension: gone everywhere immediately, errors are not cacheable
	_, hdr, body = publicReq(t, s, "GET", "/public/sitemap", nil)
	assertPublicKeys(t, "sitemap", body)
	if !strings.Contains(string(body), slug) {
		t.Fatalf("sitemap lacks live campaign: %.300s", body)
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/suspend", map[string]string{"reason_code": "REPORT_REVIEW", "note": "suspended while investigating"}), 200, "")
	c3, hdr, _ := publicReq(t, s, "GET", "/public/campaigns/"+slug, nil, "If-None-Match", `"anything"`)
	if c3 != 404 || hdr.Get("Cache-Control") != "no-store" || hdr.Get("ETag") != "" {
		t.Fatalf("suspended detail: %d %v", c3, hdr)
	}
	for _, path := range []string{"/public/sitemap", "/public/campaigns/featured?limit=12", "/public/campaigns?q=" + tok} {
		if _, _, body := publicReq(t, s, "GET", path, nil); strings.Contains(string(body), slug) {
			t.Fatalf("%s still lists the suspended campaign", path)
		}
	}
	if c, _, _ := publicReq(t, s, "GET", "/public/campaign-codes/"+code, nil); c != 404 {
		t.Fatal("short code resolves a suspended campaign")
	}
	r = comp.do("GET", "/admin/campaigns/featured", nil)
	if !strings.Contains(string(r.Data), `"shown":false`) {
		t.Fatalf("staff view should show the pick as hidden: %s", r.Data)
	}
	comp.expect(comp.do("DELETE", "/admin/campaigns/"+id+"/feature", map[string]string{"reason": "suspended campaign"}), 200, "")
}
