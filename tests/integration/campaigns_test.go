//go:build integration

// Stage 6 campaign engine end to end against the real local stack: age attestation → BASIC_VERIFIED → draft;
// identity verification → submission; review, four eyes, publication, public page and search, re-review of
// live edits, pause/suspend/reactivate/complete/archive, compliance restriction enforcement, self-review through
// linked accounts, concurrency, failure injection and a performance sample. Media are scanned by the running
// worker (ClamAV) and processed by the campaigns media consumer.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

const storyText = "Tariro is a bright student in Harare who needs help with school fees for the coming year. " +
	"The funds raised will cover tuition, uniforms and books, paid directly against the school's fee statement."

// basicUser is verified email + phone + age attestation (BASIC_VERIFIED).
func basicUser(t *testing.T, s *itServer, prefix string) vUser {
	t.Helper()
	u := verifiedUser(t, s, prefix)
	attest(t, u.browser, "ATTESTED")
	waitFor(t, 30*time.Second, "BASIC_VERIFIED", func() bool { lvl, _ := kycLevel(t, u); return lvl == "BASIC_VERIFIED" })
	return u
}

func createCampaign(t *testing.T, u vUser, title, category string, extra map[string]any) apiResp {
	t.Helper()
	body := map[string]any{"title": title, "summary": "Help with school fees for the coming school year in Harare.",
		"category": category, "goal": map[string]string{"amount_minor": "150000", "currency": "USD"}}
	for k, v := range extra {
		body[k] = v
	}
	return u.do("POST", "/campaigns", body)
}

func campaignField(t *testing.T, r apiResp, path ...string) any {
	t.Helper()
	var m any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, r.Data)
	}
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

// uploadCover uploads a PNG cover and waits until the media pipeline approves it.
func uploadCover(t *testing.T, u vUser, campaignID string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", "COVER")
	_ = mw.WriteField("alt_text", "Students outside a school building")
	_ = mw.WriteField("depicts_minor", "false")
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="../../cover.png.exe"`)
	h.Set("Content-Type", "image/png")
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(itPNG(t))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", u.s.srv.URL+"/api/v1/campaigns/"+campaignID+"/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Forwarded-For", u.ip)
	req.Header.Set("X-CSRF-Token", u.cookie("fz_csrf"))
	resp, err := u.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("cover upload: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	waitFor(t, 90*time.Second, "cover approved", func() bool {
		r := u.do("GET", "/campaigns/"+campaignID+"/media", nil)
		return strings.Contains(string(r.Data), `"`+env.Data.ID+`"`) && strings.Contains(string(r.Data), `"APPROVED"`)
	})
	return env.Data.ID
}

// readyCampaign creates an identity-verified owner with a complete, submittable EDUCATION campaign.
func readyCampaign(t *testing.T, s *itServer, f *verifStaff, prefix, category string) (vUser, string) {
	t.Helper()
	u := basicUser(t, s, prefix)
	approvedKYC(t, u, s.staff(t, f.rev1))
	r := createCampaign(t, u, "Help Tariro go to school "+prefix, category, nil)
	u.expect(r, 201, "")
	id := r.field(t, "id")
	r = u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText}, "If-Match", r.Header.Get("ETag"))
	u.expect(r, 200, "")
	r = u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "SELF"})
	u.expect(r, 201, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": r.field(t, "id"), "disclosure": "NONE"}), 200, "")
	uploadCover(t, u, id)
	return u, id
}

func decision(code string) map[string]string {
	return map[string]string{"reason_code": code, "note": "checked story, beneficiary and media against the checklist"}
}

// approveAndPublish runs the review with the campaign reviewer and publishes as the owner.
func approveAndPublish(t *testing.T, s *itServer, f *verifStaff, u vUser, id string) {
	t.Helper()
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE")), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/publish", nil), 200, "")
}

func publicGet(t *testing.T, s *itServer, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(s.srv.URL + "/api/v1" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// ===== tests ==============================================================================================

func TestCampaignLifecycleEndToEnd(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)

	// draft gate: BASIC_VERIFIED needs an age attestation (ADR-037)
	nb := verifiedUser(t, s, "camp-noattest")
	r := createCampaign(t, nb, "Help Tariro go to school now", "EDUCATION", nil)
	nb.expect(r, 422, "NOT_ELIGIBLE")
	if !strings.Contains(fmt.Sprint(r.Error.Details), "AGE_ATTESTATION_REQUIRED") {
		t.Fatalf("missing age reason: %v", r.Error.Details)
	}

	u := basicUser(t, s, "camp-e2e")
	other := basicUser(t, s, "camp-other")
	// money is exact: digit strings only, no floats, no unverified currency (ZWG minor units: LR-043)
	u.expect(createCampaign(t, u, "Help Tariro go to school", "EDUCATION", map[string]any{"goal": map[string]any{"amount_minor": 1500.5, "currency": "USD"}}), 400, "")
	u.expect(createCampaign(t, u, "Help Tariro go to school", "EDUCATION", map[string]any{"goal": map[string]string{"amount_minor": "15.00", "currency": "USD"}}), 422, "VALIDATION_FAILED")
	u.expect(createCampaign(t, u, "Help Tariro go to school", "EDUCATION", map[string]any{"goal": map[string]string{"amount_minor": "150000", "currency": "ZWG"}}), 422, "VALIDATION_FAILED")
	u.expect(createCampaign(t, u, "<b>Help</b> Tariro go to school", "EDUCATION", nil), 422, "VALIDATION_FAILED")
	u.expect(createCampaign(t, u, "Help a registered charity cause", "CHARITY", nil), 422, "NOT_ELIGIBLE") // organisation-only category
	r = createCampaign(t, u, "Help Tariro go to school", "EDUCATION", nil)
	u.expect(r, 201, "")
	id := r.field(t, "id")
	etag := r.Header.Get("ETag")
	if v := campaignField(t, r, "donations", "available"); v != false {
		t.Fatalf("donations must be unavailable: %v", v)
	}
	for _, k := range []string{"raised", "total", "balance", "amount_raised"} {
		if strings.Contains(string(r.Data), `"`+k) {
			t.Fatalf("campaign exposes %s: %s", k, r.Data)
		}
	}

	// optimistic concurrency and content safety on edits
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText}), 422, "IF_MATCH_REQUIRED")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText}, "If-Match", `"99"`), 409, "CAMPAIGN_STATE_CHANGED")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText + " <script>alert(1)</script>"}, "If-Match", etag), 422, "VALIDATION_FAILED")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText + " javascript:alert(1)"}, "If-Match", etag), 422, "VALIDATION_FAILED")
	r = u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText}, "If-Match", etag)
	u.expect(r, 200, "")

	// IDOR: other users, anonymous and staff without the right permission see nothing
	other.expect(other.do("GET", "/campaigns/"+id, nil), 404, "CAMPAIGN_NOT_FOUND")
	other.expect(other.do("PATCH", "/campaigns/"+id, map[string]any{"title": "Hijacked campaign title"}, "If-Match", r.Header.Get("ETag")), 404, "")
	other.expect(other.do("POST", "/campaigns/"+id+"/submit", nil), 404, "")
	if strings.Contains(string(other.do("GET", "/campaigns/mine", nil).Data), id) {
		t.Fatal("another user's campaign listed")
	}

	// submission gates are explained, never silently passed
	r = u.do("POST", "/campaigns/"+id+"/submit", nil)
	u.expect(r, 422, "NOT_ELIGIBLE")
	for _, want := range []string{"IDENTITY_VERIFICATION_REQUIRED", "BENEFICIARY_REQUIRED", "COVER_IMAGE_REQUIRED"} {
		if !strings.Contains(fmt.Sprint(r.Error.Details), want) {
			t.Fatalf("submit reasons missing %s: %v", want, r.Error.Details)
		}
	}
	var refusals int
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.campaign_eligibility_evaluations WHERE campaign_id = $1 AND NOT allowed`, id).Scan(&refusals)
	if refusals == 0 {
		t.Fatal("refused evaluation not recorded")
	}
	approvedKYC(t, u, s.staff(t, f.rev1))
	br := u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "SELF"})
	u.expect(br, 201, "")
	// another user's beneficiary cannot be attached
	ob := other.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "SELF"})
	other.expect(ob, 201, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": ob.field(t, "id")}), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": br.field(t, "id"), "disclosure": "DISPLAY_NAME"}), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": br.field(t, "id"), "disclosure": "NONE"}), 200, "")
	uploadCover(t, u, id)
	r = u.do("GET", "/campaigns/"+id+"/eligibility?action=SUBMIT_FOR_REVIEW", nil)
	if campaignField(t, r, "allowed") != true {
		t.Fatalf("should be eligible now: %s", r.Data)
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "") // idempotent
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"title": "Changed while in review"}, "If-Match", u.do("GET", "/campaigns/"+id, nil).Header.Get("ETag")), 409, "INVALID_STATUS")
	if code, _ := publicGet(t, s, "/public/campaigns/"+u.do("GET", "/campaigns/"+id, nil).field(t, "slug")); code != 404 {
		t.Fatalf("unpublished campaign is public: %d", code)
	}

	// staff boundaries: KYC reviewers and support cannot review campaigns; users cannot see admin routes
	kycRev := s.staff(t, f.rev1)
	kycRev.expect(kycRev.do("GET", "/admin/campaigns/review", nil), 403, "PERMISSION_DENIED")
	sup := s.staff(t, f.support)
	sup.expect(sup.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE")), 403, "PERMISSION_DENIED")
	u.expect(u.do("GET", "/admin/campaigns/review", nil), 404, "")
	rev, rev2 := s.staff(t, f.campRev1), s.staff(t, f.campRev2)
	r = rev.do("GET", "/admin/campaigns/review?assigned=unassigned", nil)
	rev.expect(r, 200, "")
	if !strings.Contains(string(r.Data), id) {
		t.Fatalf("queue lacks the campaign: %.300s", r.Data)
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", map[string]string{"assignee_id": f.support.id}), 422, "ASSIGNEE_NOT_ELIGIBLE")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE")), 409, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev2.expect(rev2.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 409, "NOT_ASSIGNED")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/request-changes", map[string]string{"reason_code": "STORY_UNCLEAR", "note": "x"}), 422, "VALIDATION_FAILED")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/request-changes", map[string]string{"reason_code": "STORY_UNCLEAR",
		"note": "the story should name the school", "user_message": "Please name the school in your story."}), 200, "")
	r = u.do("GET", "/campaigns/"+id, nil)
	if r.field(t, "status") != "CHANGES_REQUESTED" || !strings.Contains(string(r.Data), "Please name the school") || strings.Contains(string(r.Data), "should name the school") {
		t.Fatalf("owner feedback must show the user message only: %s", r.Data)
	}
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText + " The school is Harare High."}, "If-Match", r.Header.Get("ETag")), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u, id)

	// public page: approved version only, no private data, donations clearly unavailable
	slug := u.do("GET", "/campaigns/"+id, nil).field(t, "slug")
	code, body := publicGet(t, s, "/public/campaigns/"+slug)
	if code != 200 || !strings.Contains(string(body), "Harare High") || !strings.Contains(string(body), "Donations are not yet available") {
		t.Fatalf("public page: %d %s", code, body)
	}
	for _, leak := range []string{u.id, br.field(t, "id"), f.campRev1.id, "kyc", "risk", "reviewer", "note", u.email} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("public page leaks %q: %s", leak, body)
		}
	}
	code, body = publicGet(t, s, "/public/campaigns?q=Tariro&category=EDUCATION")
	if code != 200 || !strings.Contains(string(body), slug) {
		t.Fatalf("search: %d %.300s", code, body)
	}
	if code, _ := publicGet(t, s, "/public/campaigns/"+slug+"x"); code != 404 {
		t.Fatal("slug enumeration should 404")
	}

	// live edit → re-review; the public page keeps the approved version until approved
	r = u.do("GET", "/campaigns/"+id, nil)
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText + " Updated: books delivered."}, "If-Match", r.Header.Get("ETag")), 200, "")
	if _, body = publicGet(t, s, "/public/campaigns/"+slug); strings.Contains(string(body), "books delivered") {
		t.Fatal("unreviewed edit is public")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("RE_REVIEW_OK")), 200, "")
	if _, body = publicGet(t, s, "/public/campaigns/"+slug); !strings.Contains(string(body), "books delivered") {
		t.Fatal("approved edit not public")
	}

	// pause / resume / suspend / reactivate / complete / archive
	u.expect(u.do("POST", "/campaigns/"+id+"/pause", nil), 200, "")
	if code, body = publicGet(t, s, "/public/campaigns/"+slug); code != 200 || !strings.Contains(string(body), `"PAUSED"`) {
		t.Fatalf("paused page: %d", code)
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/resume", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/suspend", map[string]string{"reason_code": "COMPLAINT_REVIEW", "note": "credible complaint received"}), 200, "")
	if code, _ = publicGet(t, s, "/public/campaigns/"+slug); code != 404 {
		t.Fatal("suspended campaign is public")
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/resume", nil), 409, "INVALID_STATUS") // owners cannot lift a suspension
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/reactivate", map[string]string{"reason_code": "COMPLAINT_CLEARED", "note": "complaint investigated, no issue"}), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/complete", map[string]string{"reason": "GOAL_REACHED"}), 422, "VALIDATION_FAILED") // no totals exist
	u.expect(u.do("POST", "/campaigns/"+id+"/complete", map[string]string{"reason": "ORGANISER_COMPLETED"}), 200, "")
	if code, body = publicGet(t, s, "/public/campaigns/"+slug); code != 200 || !strings.Contains(string(body), `"COMPLETED"`) {
		t.Fatalf("completed page: %d", code)
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/archive", nil), 200, "")
	if code, _ = publicGet(t, s, "/public/campaigns/"+slug); code != 404 {
		t.Fatal("archived campaign is public")
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/resume", nil), 409, "INVALID_STATUS")

	// history: one row per version, every transition audited, notifications delivered
	var versions, hist int
	_ = mig.QueryRow(ctx(t), `SELECT (SELECT version FROM app.campaigns WHERE id = $1), (SELECT count(*) FROM app.campaign_status_history WHERE campaign_id = $1)`, id).Scan(&versions, &hist)
	if versions != hist {
		t.Fatalf("history rows %d for %d versions", hist, versions)
	}
	for _, a := range []string{"campaign.draft_created", "campaign.submitted", "campaign.review_assigned", "campaign.approved", "campaign.published",
		"campaign.paused", "campaign.suspended", "campaign.reactivated", "campaign.completed", "campaign.archived", "campaign.beneficiary_changed"} {
		if countAudit(t, "audit.audit_events", a, id) == 0 {
			t.Errorf("no audit event %s", a)
		}
	}
	mailTo(t, u.email, "Your campaign is live")
}

func TestCampaignSelfReviewFourEyesAndPrivacy(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	u, id := readyCampaign(t, s, f, "camp-medical", "MEDICAL") // HIGH tier: four eyes
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")

	// a reviewer linked to the owner's personal account is refused in Go and by the database
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1 AND staff_personal_user_id IS NULL`, f.campRev2.id, u.id)
	linked := s.staff(t, f.campRev2)
	linked.expect(linked.do("POST", "/admin/campaigns/"+id+"/assign", nil), 403, "SELF_DECISION_FORBIDDEN")
	app := pool(t, os.Getenv("DATABASE_URL"))
	if _, err := app.Exec(ctx(t), `UPDATE app.campaign_reviews SET status = 'ASSIGNED', assigned_to = $2, assigned_at = now() WHERE campaign_id = $1 AND status = 'QUEUED'`,
		id, f.campRev2.id); err == nil || !strings.Contains(err.Error(), "may not review") {
		t.Fatalf("database accepted a linked reviewer: %v", err)
	}
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	r := rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE"))
	rev.expect(r, 202, "")
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "UNDER_REVIEW" {
		t.Fatal("HIGH tier approved by one person")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/second-approval", map[string]string{"note": "my own second approval"}), 403, "PERMISSION_DENIED")
	comp := s.staff(t, f.compliance)
	comp.expect(comp.do("POST", "/admin/campaigns/"+id+"/second-approval", map[string]string{"note": "compliance sign-off for HIGH tier"}), 200, "")
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "APPROVED" {
		t.Fatal("second approval did not approve")
	}
	// the owner can never decide (no admin routes) and publication is a backend gate
	u.expect(u.do("POST", "/admin/campaigns/"+id+"/approve", decision("SELF")), 404, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/publish", nil), 200, "")
	slug := u.do("GET", "/campaigns/"+id, nil).field(t, "slug")
	_, body := publicGet(t, s, "/public/campaigns/"+slug)
	if !strings.Contains(string(body), `"disclosed":false`) {
		t.Fatalf("beneficiary disclosed without consent: %s", body)
	}
	var reviews int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.campaign_reviews WHERE campaign_id = $1 AND outcome = 'APPROVE'
		AND second_approver IS NOT NULL AND second_approver <> decided_by`, id).Scan(&reviews)
	if reviews != 1 {
		t.Fatalf("four-eyes approval record: %d", reviews)
	}
}

func TestCampaignComplianceRestrictionEnforcement(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	u, id := readyCampaign(t, s, f, "camp-restrict", "EDUCATION")
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u, id)

	// a SUSPEND resolution on the owner (maker-checker) suspends the live campaign through the worker
	c1, c2 := s.staff(t, f.compliance), s.staff(t, f.compliance2)
	r := c1.do("POST", "/admin/compliance/cases", map[string]any{"case_type": "OTHER", "severity": "S2", "note": "Owner under investigation for complaints",
		"subjects": []map[string]string{{"subject_type": "USER", "subject_id": u.id, "role": "PRIMARY_SUBJECT"}}, "reason_code": "MANUAL_REFERRAL"})
	c1.expect(r, 201, "")
	caseID := r.field(t, "id")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/assign", map[string]string{}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/start", map[string]string{}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/resolve", map[string]string{"decision": "SUSPEND", "reason_code": "POLICY_BREACH",
		"note": "suspend pending investigation of complaints"}), 202, "") // proposed: maker-checker
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/approve-resolution", map[string]string{"note": "self approval attempt"}), 403, "")
	c2.expect(c2.do("POST", "/admin/compliance/cases/"+caseID+"/approve-resolution", map[string]string{"note": "second officer confirms"}), 200, "")
	waitFor(t, 60*time.Second, "campaign suspended by restriction", func() bool {
		return u.do("GET", "/campaigns/"+id, nil).field(t, "status") == "SUSPENDED"
	})
	// every gated action is refused, without disclosing the reason
	r = createCampaign(t, u, "Another campaign while restricted", "EDUCATION", nil)
	u.expect(r, 422, "NOT_ELIGIBLE")
	if !strings.Contains(fmt.Sprint(r.Error.Details), "ACCOUNT_RESTRICTED") || strings.Contains(string(r.Data), "SUSPEND") {
		t.Fatalf("restriction reason: %v %s", r.Error.Details, r.Data)
	}
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/reactivate", map[string]string{"reason_code": "TRY", "note": "attempt while restricted"}), 422, "NOT_ELIGIBLE")

	// lifting the restriction never reactivates the campaign by itself
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/reopen", map[string]string{"reason_code": "NEW_INFORMATION", "note": "complaint withdrawn by the reporter"}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/resolve", map[string]string{"decision": "CLEARED", "reason_code": "NO_ISSUE",
		"note": "complaints were not substantiated"}), 200, "")
	time.Sleep(3 * time.Second)
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "SUSPENDED" {
		t.Fatal("campaign reactivated automatically")
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/reactivate", map[string]string{"reason_code": "RESTRICTION_LIFTED", "note": "restriction lifted, rechecked"}), 200, "")
}

func TestCampaignConcurrency(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	u := basicUser(t, s, "camp-conc")

	// duplicate creation with one Idempotency-Key → one campaign
	key := ids.New()
	codes := parallelDo(6, func(int) int {
		return u.do("POST", "/campaigns", map[string]any{"title": "Concurrent creation test campaign", "summary": "Testing idempotent creation of drafts.",
			"category": "EDUCATION", "goal": map[string]string{"amount_minor": "5000", "currency": "USD"}}, "Idempotency-Key", key).Status
	})
	var n int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.campaigns WHERE created_by = $1 AND title = 'Concurrent creation test campaign'`, u.id).Scan(&n)
	if n != 1 {
		t.Fatalf("idempotent create produced %d campaigns (%v)", n, codes)
	}

	// concurrent edits with the same If-Match: exactly one wins
	r := createCampaign(t, u, "Concurrent edits test campaign", "EDUCATION", nil)
	id, etag := r.field(t, "id"), r.Header.Get("ETag")
	codes = parallelDo(5, func(i int) int {
		return u.do("PATCH", "/campaigns/"+id, map[string]any{"summary": fmt.Sprintf("Edited concurrently by writer number %d", i)}, "If-Match", etag).Status
	})
	if codes[200] != 1 || codes[409] != 4 {
		t.Fatalf("concurrent edits: %v", codes)
	}

	// approve vs reject; publish vs suspend
	o, cid := readyCampaign(t, s, f, "camp-race", "EDUCATION")
	o.expect(o.do("POST", "/campaigns/"+cid+"/submit", nil), 200, "")
	codes = parallelDo(4, func(int) int { return o.do("POST", "/campaigns/"+cid+"/submit", nil).Status })
	var submits int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.campaign_status_history WHERE campaign_id = $1 AND to_status = 'SUBMITTED'`, cid).Scan(&submits)
	if submits != 1 {
		t.Fatalf("duplicate submissions recorded: %d (%v)", submits, codes)
	}
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+cid+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+cid+"/start-review", nil), 200, "")
	codes = parallelDo(2, func(i int) int {
		if i == 0 {
			return rev.do("POST", "/admin/campaigns/"+cid+"/approve", decision("CHECKLIST_COMPLETE")).Status
		}
		return rev.do("POST", "/admin/campaigns/"+cid+"/reject", decision("PROHIBITED_PURPOSE")).Status
	})
	if codes[200] != 1 {
		t.Fatalf("approve vs reject: %v", codes)
	}
	var decided int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.campaign_reviews WHERE campaign_id = $1 AND status = 'DECIDED'`, cid).Scan(&decided)
	if decided != 1 {
		t.Fatalf("decisions: %d", decided)
	}
	if o.do("GET", "/campaigns/"+cid, nil).field(t, "status") == "APPROVED" {
		codes = parallelDo(2, func(i int) int {
			if i == 0 {
				return o.do("POST", "/campaigns/"+cid+"/publish", nil).Status
			}
			return rev.do("POST", "/admin/campaigns/"+cid+"/suspend", map[string]string{"reason_code": "RACE_TEST", "note": "suspend during publication"}).Status
		})
		st := o.do("GET", "/campaigns/"+cid, nil).field(t, "status")
		if st != "SUSPENDED" && st != "ACTIVE" || codes[200] < 1 {
			t.Fatalf("publish vs suspend ended in %s (%v)", st, codes)
		}
		if st == "ACTIVE" { // publish won; the suspend then applies to the live campaign
			rev.expect(rev.do("POST", "/admin/campaigns/"+cid+"/suspend", map[string]string{"reason_code": "RACE_TEST", "note": "suspend after publication"}), 200, "")
		}
		if code, _ := publicGet(t, s, "/public/campaigns/"+o.do("GET", "/campaigns/"+cid, nil).field(t, "slug")); code != 404 {
			t.Fatal("suspended campaign public after race")
		}
	}
}

type failingRestrictions struct{}

func (failingRestrictions) Levels(context.Context, []campaigns.Subject) (map[campaigns.Subject]string, error) {
	return nil, errors.New("injected: compliance unavailable")
}

func TestCampaignFailureInjection(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	u, id := readyCampaign(t, s, f, "camp-fail", "EDUCATION")
	// the restrictions reader fails: every gated action fails closed, nothing transitions
	orig := s.d.Verification.Campaigns.Restrictions
	s.d.Verification.Campaigns.Restrictions = failingRestrictions{}
	r := u.do("POST", "/campaigns/"+id+"/submit", nil)
	if r.Status < 500 {
		t.Fatalf("submit with compliance unavailable: %d %s", r.Status, r.Error.Code)
	}
	s.d.Verification.Campaigns.Restrictions = orig
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "DRAFT" {
		t.Fatal("campaign moved although the restriction check failed")
	}
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")

	// session revoked during the flow
	u2 := s.browser(t)
	u2.mustLogin(u.email, itPassword)
	u2.expect(u2.do("POST", "/auth/logout", nil), 204, "")
	u2.expect(u2.do("POST", "/campaigns/"+id+"/withdraw", nil), 401, "")

	// a reviewer whose session is gone cannot act; the campaign stays in the queue
	rev := s.freshStaff(t, f.campRev2)
	rev.expect(rev.do("POST", "/auth/logout", nil), 204, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 404, "")
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "SUBMITTED" {
		t.Fatal("status changed by a failed action")
	}
}

func TestCampaignPerformanceSample(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	s := newITServer(t, func(c *config.Config) { c.RateLimit.Enabled = false })
	f := staffFixture(t, s)
	u := basicUser(t, s, "camp-perf")
	measure := func(name string, n int, fn func()) {
		d := make([]time.Duration, n)
		for i := range d {
			st := time.Now()
			fn()
			d[i] = time.Since(st)
		}
		sortDur(d)
		t.Logf("PERF %-40s n=%d p50=%v p95=%v max=%v", name, n, d[n/2], d[n*95/100], d[n-1])
		if d[n*95/100] > 2*time.Second {
			t.Errorf("%s p95 %v exceeds the 2s ceiling", name, d[n*95/100])
		}
	}
	var lastID, etag string
	measure("POST /campaigns (create draft)", 30, func() {
		r := createCampaign(t, u, "Performance sample campaign title", "EDUCATION", nil)
		u.expect(r, 201, "")
		lastID, etag = r.field(t, "id"), r.Header.Get("ETag")
	})
	i := 0
	measure("PATCH /campaigns/{id} (edit draft)", 30, func() {
		i++
		r := u.do("PATCH", "/campaigns/"+lastID, map[string]any{"summary": fmt.Sprintf("Performance edit number %03d of the summary", i)}, "If-Match", etag)
		u.expect(r, 200, "")
		etag = r.Header.Get("ETag")
	})
	measure("GET /campaigns/mine", 50, func() { u.expect(u.do("GET", "/campaigns/mine", nil), 200, "") })
	measure("GET /campaigns/{id}/eligibility", 50, func() {
		u.expect(u.do("GET", "/campaigns/"+lastID+"/eligibility?action=SUBMIT_FOR_REVIEW", nil), 200, "")
	})
	measure("GET /public/campaigns?q=", 50, func() {
		if code, _ := publicGet(t, s, "/public/campaigns?q=school"); code != 200 {
			t.Fatal(code)
		}
	})
	rev := s.staff(t, f.campRev1)
	measure("GET /admin/campaigns/review", 50, func() { rev.expect(rev.do("GET", "/admin/campaigns/review", nil), 200, "") })
	var total, live int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*), count(*) FILTER (WHERE status IN ('ACTIVE','PAUSED','COMPLETED')) FROM app.campaigns`).Scan(&total, &live)
	t.Logf("PERF dataset: %d campaigns (%d live) in the shared development database; sequential requests, concurrency 1", total, live)
}

func TestCampaignSecurityRechecks(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	u, id := readyCampaign(t, s, f, "camp-sec", "EDUCATION")

	// CSRF: an unsafe request without the token is refused and changes nothing
	r := u.do("POST", "/campaigns/"+id+"/submit", nil, "X-CSRF-Token", "")
	if r.Status != 403 {
		t.Fatalf("submit without CSRF token: %d %s", r.Status, r.Error.Code)
	}
	if u.do("GET", "/campaigns/"+id, nil).field(t, "status") != "DRAFT" {
		t.Fatal("CSRF-less request changed the campaign")
	}

	// a beneficiary cannot be swapped while the campaign is in review
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	b2 := u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "INDIVIDUAL", "display_name": "Rudo", "full_name": "Rudo Moyo",
		"relationship": map[string]string{"type": "FAMILY_MEMBER"}, "authority_basis": "BENEFICIARY_CONSENT"})
	u.expect(b2, 201, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": b2.field(t, "id"), "reason": "swap during review"}), 409, "INVALID_STATUS")

	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE")), 200, "")

	// identity verification revoked after approval: publication re-checks and refuses
	var kycCase string
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT c.id FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
		WHERE p.user_id = $1 AND c.status = 'APPROVED'`, u.id).Scan(&kycCase); err != nil {
		t.Fatal(err)
	}
	kr := s.staff(t, f.rev1)
	kr.expect(adminAct(kr, kycCase, "revoke", map[string]string{"reason_code": "DOCUMENT_FRAUD_SUSPECTED", "note": "document flagged after approval"}), 200, "")
	r = u.do("POST", "/campaigns/"+id+"/publish", nil)
	u.expect(r, 422, "NOT_ELIGIBLE")
	if !strings.Contains(fmt.Sprint(r.Error.Details), "IDENTITY_VERIFICATION_REQUIRED") {
		t.Fatalf("publication reasons: %v", r.Error.Details)
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/publish", decision("STAFF_PUBLISH")), 422, "NOT_ELIGIBLE") // staff cannot bypass it either
	var refused int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.campaign_eligibility_evaluations WHERE campaign_id = $1
		AND action = 'PUBLISH' AND NOT allowed`, id).Scan(&refused)
	if refused < 2 {
		t.Fatalf("refused publications recorded: %d", refused)
	}

	// restricted campaigns never appear in public search
	u2, id2 := readyCampaign(t, s, f, "camp-sec2", "EDUCATION")
	u2.expect(u2.do("POST", "/campaigns/"+id2+"/submit", nil), 200, "")
	approveAndPublish(t, s, f, u2, id2)
	slug := u2.do("GET", "/campaigns/"+id2, nil).field(t, "slug")
	if _, body := publicGet(t, s, "/public/campaigns?q=camp-sec2"); !strings.Contains(string(body), slug) {
		t.Fatalf("live campaign missing from search: %s", body)
	}
	rev.expect(rev.do("POST", "/admin/campaigns/"+id2+"/suspend", map[string]string{"reason_code": "COMPLAINT_REVIEW", "note": "complaint under review"}), 200, "")
	if _, body := publicGet(t, s, "/public/campaigns?q=camp-sec2"); strings.Contains(string(body), slug) {
		t.Fatal("suspended campaign exposed through search")
	}
	// unlisted campaigns are reachable by link but not listed
	rev.expect(rev.do("POST", "/admin/campaigns/"+id2+"/reactivate", map[string]string{"reason_code": "COMPLAINT_CLEARED", "note": "no issue found"}), 200, "")
	r = u2.do("GET", "/campaigns/"+id2, nil)
	u2.expect(u2.do("PATCH", "/campaigns/"+id2, map[string]any{"visibility": "UNLISTED"}, "If-Match", r.Header.Get("ETag")), 200, "")
	if code, _ := publicGet(t, s, "/public/campaigns/"+slug); code != 200 {
		t.Fatal("unlisted campaign not reachable by link")
	}
	if _, body := publicGet(t, s, "/public/campaigns?q=camp-sec2"); strings.Contains(string(body), slug) {
		t.Fatal("unlisted campaign listed in search")
	}
	// media removed after approval disappears from the public view (no dangling references)
	var mediaID string
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT id FROM app.campaign_media WHERE campaign_id = $1 AND status = 'APPROVED'`,
		id2).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	_, body := publicGet(t, s, "/public/campaigns/"+slug)
	if !strings.Contains(string(body), mediaID) {
		t.Fatalf("approved cover missing from public view: %s", body)
	}
	mr := u2.do("GET", "/campaigns/"+id2+"/media", nil)
	var ml struct {
		Media []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"media"`
	}
	_ = json.Unmarshal(mr.Data, &ml)
	etag := ""
	for _, m := range ml.Media {
		if m.ID == mediaID {
			etag = fmt.Sprintf("%q", fmt.Sprint(m.Version))
		}
	}
	u2.expect(u2.do("DELETE", "/campaigns/"+id2+"/media/"+mediaID, nil, "If-Match", etag), 204, "")
	if _, body = publicGet(t, s, "/public/campaigns/"+slug); strings.Contains(string(body), mediaID) {
		t.Fatalf("removed media still referenced publicly: %s", body)
	}
}
