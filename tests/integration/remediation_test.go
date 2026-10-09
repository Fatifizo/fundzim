//go:build integration

// Stage 6 stream R (ADR-037) against the REAL local stack: age attestation and BASIC_VERIFIED, compliance
// restriction enforcement, staff <-> personal account links and the database self-decision guard over linked
// identities. The API runs in-process; the outbox consumers (registration attestation, BASIC re-evaluation on
// identity events, staff-link emails) run in the worker container, which must run current code.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/compliance"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// ---- helpers -------------------------------------------------------------------------------------------------

func statusLevel(t *testing.T, b *browser) string {
	t.Helper()
	r := b.do("GET", "/kyc/status", nil)
	b.expect(r, 200, "")
	return r.field(t, "level")
}

type ageResp struct {
	AdultAge         int    `json:"adult_age"`
	StatementVersion string `json:"statement_version"`
	Assurance        string `json:"assurance"`
	Level            string `json:"level"`
	Attestation      *struct {
		Outcome string `json:"outcome"`
		Source  string `json:"source"`
	} `json:"attestation"`
	VerifiedDOBBelow bool     `json:"verified_dob_below_adult_age"`
	BasicUnmet       []string `json:"basic_unmet"`
}

func decodeAge(t *testing.T, r apiResp) ageResp {
	t.Helper()
	var a ageResp
	if err := json.Unmarshal(r.Data, &a); err != nil {
		t.Fatalf("age response: %v %s", err, r.Data)
	}
	return a
}

func attest(t *testing.T, b *browser, outcome string) ageResp {
	t.Helper()
	r := b.do("POST", "/me/age-attestation", map[string]string{"outcome": outcome, "statement_version": "age-statement-v1"})
	b.expect(r, 201, "")
	return decodeAge(t, r)
}

func countRows(t *testing.T, p interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// ---- 1. age attestation and BASIC_VERIFIED -------------------------------------------------------------------

func TestAgeAttestationGrantsAndWithdrawsBasic(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	k := s.d.Verification.KYC

	u := verifiedUser(t, s, "age-basic") // email and phone verified, no attestation yet
	a := decodeAge(t, u.do("GET", "/me/age-attestation", nil))
	if a.Attestation != nil || a.Assurance != kyc.AssuranceNone || a.AdultAge != 18 || a.StatementVersion != "age-statement-v1" {
		t.Fatalf("initial age status: %+v", a)
	}
	if lvl := statusLevel(t, u.browser); lvl != kyc.LevelUnverified {
		t.Fatalf("no attestation: level %s", lvl)
	}
	// only the current statement version is accepted; staff accounts cannot attest
	u.expect(u.do("POST", "/me/age-attestation", map[string]string{"outcome": "ATTESTED", "statement_version": "age-statement-v0"}),
		422, "STATEMENT_VERSION_NOT_CURRENT")
	u.expect(u.do("POST", "/me/age-attestation", map[string]string{"outcome": "MAYBE", "statement_version": "age-statement-v1"}), 422, "")
	staff := s.staff(t, f.support)
	staff.expect(staff.do("POST", "/me/age-attestation", map[string]string{"outcome": "ATTESTED", "statement_version": "age-statement-v1"}), 403, "")

	// ATTESTED with every other condition holding: BASIC_VERIFIED, self-attested assurance (never documentary)
	a = attest(t, u.browser, kyc.AgeAttested)
	if a.Level != kyc.LevelBasic || a.Assurance != kyc.AssuranceSelfAttested || len(a.BasicUnmet) != 0 || a.Attestation.Source != kyc.AgeSourceDashboard {
		t.Fatalf("after attesting: %+v", a)
	}
	if lvl := statusLevel(t, u.browser); lvl != kyc.LevelBasic {
		t.Fatalf("kyc status must show BASIC_VERIFIED: %s", lvl)
	}
	if n := countRows(t, mig, `SELECT count(*) FROM kyc.profile_events e JOIN kyc.verification_profiles p ON p.id = e.profile_id
		WHERE p.user_id = $1 AND e.to_level = 'BASIC_VERIFIED' AND e.reason_code = 'BASIC_CONDITIONS_MET'`, u.id); n != 1 {
		t.Fatalf("profile_events for the grant: %d", n)
	}
	if n := countRows(t, mig, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'kyc.level_changed' AND aggregate_id = $1
		AND payload->>'level' = 'BASIC_VERIFIED'`, u.id); n != 1 {
		t.Fatalf("kyc.level_changed outbox events: %d", n)
	}
	if n := countRows(t, mig, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'kyc.age_attested' AND aggregate_id = $1`, u.id); n != 1 {
		t.Fatalf("kyc.age_attested events: %d", n)
	}
	if n := countAudit(t, "audit.security_audit_events", "kyc.level.changed", u.id); n != 1 {
		t.Fatalf("level change audit events: %d", n)
	}
	if n := countAudit(t, "audit.security_audit_events", "kyc.age_attestation.recorded", u.id); n != 1 {
		t.Fatalf("attestation audit events: %d", n)
	}
	// evaluation is idempotent
	if res, err := k.EvaluateBasic(ctx(t), u.id); err != nil || res.Changed || !res.Met || res.Level != kyc.LevelBasic {
		t.Fatalf("re-evaluation: %+v %v", res, err)
	}

	// append-only: the kyc role has no UPDATE/DELETE, and even the table owner is stopped by the trigger
	kp := s.d.Verification.KYCPool
	if _, err := kp.Exec(ctx(t), `UPDATE kyc.age_attestations SET outcome = 'DECLINED' WHERE user_id = $1`, u.id); sqlState(err) != "42501" {
		t.Fatalf("kyc role UPDATE: %v", err)
	}
	if _, err := kp.Exec(ctx(t), `DELETE FROM kyc.age_attestations WHERE user_id = $1`, u.id); sqlState(err) != "42501" {
		t.Fatalf("kyc role DELETE: %v", err)
	}
	if _, err := mig.Exec(ctx(t), `UPDATE kyc.age_attestations SET outcome = 'DECLINED' WHERE user_id = $1`, u.id); err == nil {
		t.Fatal("owner UPDATE of an attestation succeeded")
	}
	if _, err := s.d.DB.Exec(ctx(t), `SELECT 1 FROM kyc.age_attestations`); sqlState(err) != "42501" {
		t.Fatalf("the app role must not read kyc.age_attestations: %v", err)
	}

	// DECLINED withdraws BASIC_VERIFIED (a later DECLINED overrides the attestation); the change is a new row
	a = attest(t, u.browser, kyc.AgeDeclined)
	if a.Level != kyc.LevelUnverified || a.Assurance != kyc.AssuranceNone || !containsStr(a.BasicUnmet, kyc.BasicUnmetAge) {
		t.Fatalf("after declining: %+v", a)
	}
	if n := countRows(t, mig, `SELECT count(*) FROM kyc.age_attestations WHERE user_id = $1`, u.id); n != 2 {
		t.Fatalf("attestation rows: %d", n)
	}
	a = attest(t, u.browser, kyc.AgeAttested)
	if a.Level != kyc.LevelBasic {
		t.Fatalf("re-attesting: %+v", a)
	}

	// suspension withdraws BASIC_VERIFIED and reactivation restores it, through the worker's consumer of the
	// identity.account_suspended / identity.account_reactivated events
	comp := s.freshStaff(t, f.compliance)
	comp.expect(comp.do("POST", "/admin/users/"+u.id+"/suspend", map[string]string{"reason": "remediation integration test"}), 204, "")
	waitBasicLevel(t, k, u.id, kyc.LevelUnverified)
	comp.expect(comp.do("POST", "/admin/users/"+u.id+"/reactivate", map[string]string{"reason": "remediation integration test"}), 204, "")
	waitBasicLevel(t, k, u.id, kyc.LevelBasic)
	var events []string
	rows, err := mig.Query(ctx(t), `SELECT e.reason_code FROM kyc.profile_events e JOIN kyc.verification_profiles p ON p.id = e.profile_id
		WHERE p.user_id = $1 ORDER BY e.aggregate_version`, u.id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		events = append(events, r)
	}
	rows.Close()
	want := "PROFILE_CREATED,BASIC_CONDITIONS_MET,BASIC_CONDITION_NOT_MET,BASIC_CONDITIONS_MET,BASIC_CONDITION_NOT_MET,BASIC_CONDITIONS_MET"
	if strings.Join(events, ",") != want {
		t.Fatalf("profile event history:\n got %s\nwant %s", strings.Join(events, ","), want)
	}
}

func waitBasicLevel(t *testing.T, k *kyc.Service, userID, want string) {
	t.Helper()
	waitFor(t, 30*time.Second, "level "+want+" (worker consumer)", func() bool {
		st, err := k.Level(ctx(t), userID)
		return err == nil && st.Level == want
	})
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestBasicNeedsEveryCondition(t *testing.T) {
	s := newITServer(t, nil)
	k := s.d.Verification.KYC

	// registration with the age box ticked: the worker records a REGISTRATION attestation; BASIC_VERIFIED
	// follows only once email and phone are verified (re-evaluated by the worker on identity.* events)
	email := uniqueEmail("age-reg")
	b := s.browser(t)
	b.expect(b.do("POST", "/auth/register", map[string]any{"email": email, "password": itPassword, "display_name": "Rudo Chari",
		"accept_terms": true, "age_attestation": true}), 202, "")
	var uid string
	if err := s.d.DB.QueryRow(ctx(t), `SELECT user_id FROM app.user_emails WHERE email_normalized = $1`, email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "registration attestation", func() bool {
		st, err := k.AgeStatus(ctx(t), uid)
		return err == nil && st.Attestation != nil && st.Attestation.Source == kyc.AgeSourceRegistration
	})
	if res, err := k.EvaluateBasic(ctx(t), uid); err != nil || res.Met || !containsStr(res.Unmet, kyc.BasicUnmetEmail) ||
		!containsStr(res.Unmet, kyc.BasicUnmetPhone) || res.Level != kyc.LevelUnverified {
		t.Fatalf("unverified contacts: %+v %v", res, err)
	}
	tok := tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
	b.mustLogin(email, itPassword)
	if res, err := k.EvaluateBasic(ctx(t), uid); err != nil || res.Met || len(res.Unmet) != 1 || res.Unmet[0] != kyc.BasicUnmetPhone {
		t.Fatalf("phone missing: %+v %v", res, err)
	}
	verifyPhone(t, b)
	waitBasicLevel(t, k, uid, kyc.LevelBasic) // granted by the worker after identity.phone_verified
	// a duplicate delivery of the registration event records nothing more
	var evID string
	if err := s.d.DB.QueryRow(ctx(t), `SELECT id FROM app.outbox_events WHERE event_type = 'identity.registration_age_attested' AND aggregate_id = $1`,
		uid).Scan(&evID); err != nil {
		t.Fatal(err)
	}
	if err := k.RecordRegistrationAttestation(ctx(t), evID, uid, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool(t, os.Getenv("DATABASE_MIGRATION_URL")), `SELECT count(*) FROM kyc.age_attestations WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("duplicate registration attestation: %d rows", n)
	}

	// DECLINED from the start blocks BASIC_VERIFIED even with every other condition
	d := verifiedUser(t, s, "age-declined")
	a := attest(t, d.browser, kyc.AgeDeclined)
	if a.Level != kyc.LevelUnverified || !containsStr(a.BasicUnmet, kyc.BasicUnmetAge) {
		t.Fatalf("declined: %+v", a)
	}
	if lvl := statusLevel(t, d.browser); lvl != kyc.LevelUnverified {
		t.Fatalf("declined user level: %s", lvl)
	}
	// registering without the box records nothing
	none := uniqueEmail("age-none")
	b2 := s.browser(t)
	b2.register(none, itPassword, "Tendai")
	var noneID string
	_ = s.d.DB.QueryRow(ctx(t), `SELECT user_id FROM app.user_emails WHERE email_normalized = $1`, none).Scan(&noneID)
	if n := countRows(t, s.d.DB, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'identity.registration_age_attested' AND aggregate_id = $1`,
		noneID); n != 0 {
		t.Fatalf("attestation event without the box: %d", n)
	}
}

func verifyPhone(t *testing.T, b *browser) {
	t.Helper()
	suffix := uniquePhoneSuffix()
	b.expect(b.do("POST", "/me/phone/verify-request", map[string]string{"phone": "077" + suffix}), 202, "")
	text := mailTo(t, "26377"+suffix+"@sms.dev.invalid", "")
	code := sixDigits.FindStringSubmatch(text)
	if code == nil {
		t.Fatalf("no code in dev SMS: %q", text)
	}
	b.expect(b.do("POST", "/me/phone/verify-confirm", map[string]string{"phone": "+26377" + suffix, "code": code[1]}), 200, "")
}

// ---- 2. compliance restrictions --------------------------------------------------------------------------------

type restrictionEvent struct {
	Type    string
	Payload map[string]string
}

func restrictionEvents(t *testing.T, e *crEnv, caseID string) []restrictionEvent {
	t.Helper()
	rows, err := e.app.Query(ctx(t), `SELECT event_type, payload FROM app.outbox_events WHERE aggregate_id = $1
		AND event_type IN ('compliance.restriction_applied', 'compliance.restriction_lifted') ORDER BY occurred_at, id`, caseID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []restrictionEvent
	for rows.Next() {
		var ev restrictionEvent
		var raw []byte
		if err := rows.Scan(&ev.Type, &raw); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		keys := make([]string, 0, len(m))
		ev.Payload = map[string]string{}
		for k, v := range m {
			keys = append(keys, k)
			s, _ := v.(string)
			ev.Payload[k] = s
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "case_id,level,subject_id,subject_type" {
			t.Fatalf("restriction event carries more than ids and levels: %v", keys)
		}
		out = append(out, ev)
	}
	return out
}

func (e *crEnv) level(sub compliance.Subject) compliance.Level {
	e.t.Helper()
	m, err := e.comp.Restrictions(ctx(e.t), []compliance.Subject{sub})
	if err != nil {
		e.t.Fatal(err)
	}
	return m[sub]
}

func TestComplianceRestrictionsAppliedAndLifted(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	user := e.personalUser()
	sub := compliance.Subject{Type: "USER", ID: user}
	a, b := e.staff(""), e.staff("")
	op := func(actor, caseID string) compliance.Op {
		return compliance.Op{Actor: compliance.Staff(actor), CaseID: caseID}
	}
	resolve := func(actor, caseID, decision string) compliance.Case {
		o := op(actor, caseID)
		o.Decision, o.ReasonCode, o.Note = decision, "INTEGRATION_TEST", "restriction integration test"
		cs, err := e.comp.Resolve(c, o)
		if err != nil {
			t.Fatalf("resolve %s: %v", decision, err)
		}
		return cs
	}

	// RESTRICT: proposed (nothing applies yet), the decider cannot approve, another officer can
	cs1 := e.openReviewed(a, a, user)
	resolve(a, cs1.ID, "RESTRICT")
	if lvl := e.level(sub); lvl != compliance.LevelNone {
		t.Fatalf("a PROPOSED restriction must not apply: %q", lvl)
	}
	if _, err := e.comp.ApproveResolution(c, op(a, cs1.ID)); errCode(err) != "SELF_APPROVAL_FORBIDDEN" {
		t.Fatalf("maker approving own restriction: %v", err)
	}
	if _, err := e.comp.ApproveResolution(c, op(b, cs1.ID)); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level(sub); lvl != compliance.LevelRestricted {
		t.Fatalf("after approval: %q", lvl)
	}
	ev := restrictionEvents(t, e, cs1.ID)
	if len(ev) != 1 || ev[0].Type != compliance.EvRestrictionApplied || ev[0].Payload["level"] != "RESTRICTED" ||
		ev[0].Payload["subject_id"] != user || ev[0].Payload["case_id"] != cs1.ID {
		t.Fatalf("applied event: %+v", ev)
	}

	// a second case SUSPENDs: the highest level wins
	cs2 := e.openReviewed(a, a, user)
	resolve(a, cs2.ID, "SUSPEND")
	if _, err := e.comp.ApproveResolution(c, op(b, cs2.ID)); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level(sub); lvl != compliance.LevelSuspended {
		t.Fatalf("highest level must win: %q", lvl)
	}

	// reopening keeps the restriction; re-resolving CLEARED lifts case 1's (case 2 still applies)
	if _, err := e.comp.Reopen(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs1.ID, ReasonCode: "NEW_INFORMATION", Note: "reconsider"}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, e.cmp, `SELECT count(*) FROM compliance.subject_restrictions WHERE case_id = $1 AND lifted_at IS NULL`, cs1.ID); n != 1 {
		t.Fatalf("reopen must not lift: %d active", n)
	}
	resolve(a, cs1.ID, "CLEARED")
	ev = restrictionEvents(t, e, cs1.ID)
	if len(ev) != 2 || ev[1].Type != compliance.EvRestrictionLifted || ev[1].Payload["level"] != "RESTRICTED" {
		t.Fatalf("lifted event: %+v", ev)
	}
	if lvl := e.level(sub); lvl != compliance.LevelSuspended {
		t.Fatalf("case 2 still applies: %q", lvl)
	}

	// case 2 re-resolved to OFFBOARD supersedes SUSPENDED; EDD_CONDITIONS then lifts everything
	if _, err := e.comp.Reopen(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs2.ID, ReasonCode: "NEW_INFORMATION", Note: "escalate"}); err != nil {
		t.Fatal(err)
	}
	resolve(a, cs2.ID, "OFFBOARD")
	if lvl := e.level(sub); lvl != compliance.LevelSuspended {
		t.Fatalf("the proposed OFFBOARD must not apply before approval: %q", lvl)
	}
	if _, err := e.comp.ApproveResolution(c, op(b, cs2.ID)); err != nil {
		t.Fatal(err)
	}
	if lvl := e.level(sub); lvl != compliance.LevelOffboarded {
		t.Fatalf("offboarded: %q", lvl)
	}
	if _, err := e.comp.Reopen(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs2.ID, ReasonCode: "NEW_INFORMATION", Note: "resolved"}); err != nil {
		t.Fatal(err)
	}
	resolve(a, cs2.ID, "EDD_CONDITIONS")
	if lvl := e.level(sub); lvl != compliance.LevelNone {
		t.Fatalf("everything lifted: %q", lvl)
	}
	var types []string
	for _, x := range restrictionEvents(t, e, cs2.ID) {
		types = append(types, x.Type+":"+x.Payload["level"])
	}
	if got := strings.Join(types, ","); got != "compliance.restriction_applied:SUSPENDED,compliance.restriction_lifted:SUSPENDED,"+
		"compliance.restriction_applied:OFFBOARDED,compliance.restriction_lifted:OFFBOARDED" {
		t.Fatalf("case 2 restriction events: %s", got)
	}
	// the projection is never rewritten: lifted rows stay, a lifted row cannot be changed again
	if n := countRows(t, e.cmp, `SELECT count(*) FROM compliance.subject_restrictions WHERE subject_id = $1`, user); n != 3 {
		t.Fatalf("restriction history rows: %d", n)
	}
	if _, err := e.cmp.Exec(c, `UPDATE compliance.subject_restrictions SET lifted_at = now() WHERE subject_id = $1`, user); err == nil {
		t.Fatal("a lifted restriction was changed")
	}
	if _, err := e.cmp.Exec(c, `DELETE FROM compliance.subject_restrictions WHERE subject_id = $1`, user); err == nil {
		t.Fatal("a restriction row was deleted")
	}
	// unknown or malformed subjects are simply unrestricted
	m, err := e.comp.Restrictions(c, []compliance.Subject{{Type: "USER", ID: ids.New()}, {Type: "NOPE", ID: "x"}})
	if err != nil || len(m) != 2 {
		t.Fatalf("unknown subjects: %v %v", m, err)
	}
}

func TestComplianceRestrictionFromSTRCaseAppliesWithoutDisclosure(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	user := e.personalUser()
	a, b := e.staff(""), e.staff("")
	cs, err := e.comp.Open(c, compliance.Staff(a), compliance.OpenInput{CaseType: "AML_MONITORING", Severity: "S1", OpeningReasonCode: "STAFF_REFERRAL",
		Confidentiality: compliance.ConfRestrictedSTR, Links: []compliance.LinkInput{{SubjectType: "USER", SubjectID: user, Role: "PRIMARY_SUBJECT"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	str := func(actor string) compliance.Op {
		return compliance.Op{Actor: compliance.Staff(actor), CaseID: cs.ID, STRAccess: true}
	}
	if _, err := e.comp.Assign(c, str(a)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.Start(c, str(a)); err != nil {
		t.Fatal(err)
	}
	o := str(a)
	o.Decision, o.ReasonCode, o.Note = "RESTRICT", "SUSPICIOUS_PATTERN", "str memo"
	if _, err := e.comp.Resolve(c, o); err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.ApproveResolution(c, str(b)); err != nil {
		t.Fatal(err)
	}
	// the restriction applies to readers without STR access, and nothing they can read names the case's reason
	if lvl := e.level(compliance.Subject{Type: "USER", ID: user}); lvl != compliance.LevelRestricted {
		t.Fatalf("STR restriction: %q", lvl)
	}
	var cols string
	if err := e.cmp.QueryRow(c, `SELECT string_agg(column_name, ',' ORDER BY column_name) FROM information_schema.columns
		WHERE table_schema = 'compliance' AND table_name = 'subject_restrictions'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"confidential", "reason_code", "note", "severity", "case_type"} {
		if strings.Contains(cols, bad) {
			t.Fatalf("subject_restrictions exposes %s: %s", bad, cols)
		}
	}
	for _, ev := range restrictionEvents(t, e, cs.ID) { // also checks the payload keys
		if ev.Payload["level"] != "RESTRICTED" {
			t.Fatalf("STR event: %+v", ev)
		}
	}
}

func TestCampaignReviewEscalationOpensCaseAndRestrictsCampaign(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	owner := e.personalUser()
	campaign, review := ids.New(), ids.New()
	d := escalation(compliance.EvCampaignReviewEscalated, map[string]any{"campaign_id": campaign, "owner_user_id": owner, "review_id": review,
		"reason_code": "POSSIBLE_MISREPRESENTATION"})
	res, err := e.comp.OpenFromEvent(c, d)
	if err != nil || res.Outcome != "OPENED" {
		t.Fatalf("open: %+v %v", res, err)
	}
	if again, err := e.comp.OpenFromEvent(c, d); err != nil || !again.Duplicate {
		t.Fatalf("duplicate delivery: %+v %v", again, err)
	}
	a, b := e.staff(""), e.staff("")
	cs, err := e.comp.Get(c, compliance.Staff(a), res.CaseID, false)
	if err != nil {
		t.Fatal(err)
	}
	if cs.CaseType != "CAMPAIGN_REVIEW" || cs.Source != "CAMPAIGN_ESCALATION" || cs.OpeningReasonCode != "POSSIBLE_MISREPRESENTATION" {
		t.Fatalf("case: %+v", cs.Case)
	}
	roles := map[string]string{}
	for _, l := range cs.Links {
		roles[l.SubjectType+":"+l.SubjectID] = l.Role
	}
	if roles["CAMPAIGN:"+campaign] != "PRIMARY_SUBJECT" || roles["USER:"+owner] != "RELATED_SUBJECT" || roles["CAMPAIGN_REVIEW:"+review] != "RELATED_OBJECT" {
		t.Fatalf("links: %v", roles)
	}
	// a second escalation of the same campaign links to the open case
	d2 := escalation(compliance.EvCampaignReviewEscalated, map[string]any{"campaign_id": campaign, "owner_user_id": owner, "review_id": ids.New()})
	if r2, err := e.comp.OpenFromEvent(c, d2); err != nil || r2.Outcome != "LINKED" || r2.CaseID != res.CaseID {
		t.Fatalf("second escalation: %+v %v", r2, err)
	}
	// SUSPEND approved: the campaign (primary subject) is suspended; the owner (related subject) is not
	if _, err := e.comp.Assign(c, compliance.Op{Actor: compliance.Staff(a), CaseID: res.CaseID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.Start(c, compliance.Op{Actor: compliance.Staff(a), CaseID: res.CaseID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.Resolve(c, compliance.Op{Actor: compliance.Staff(a), CaseID: res.CaseID, Decision: "SUSPEND", ReasonCode: "MISREPRESENTATION",
		Note: "campaign misrepresents the beneficiary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.ApproveResolution(c, compliance.Op{Actor: compliance.Staff(b), CaseID: res.CaseID}); err != nil {
		t.Fatal(err)
	}
	m, err := e.comp.Restrictions(c, []compliance.Subject{{Type: "CAMPAIGN", ID: campaign}, {Type: "USER", ID: owner}})
	if err != nil {
		t.Fatal(err)
	}
	if m[compliance.Subject{Type: "CAMPAIGN", ID: campaign}] != compliance.LevelSuspended || m[compliance.Subject{Type: "USER", ID: owner}] != compliance.LevelNone {
		t.Fatalf("restrictions: %v", m)
	}
}

// ---- 3. staff <-> personal links ----------------------------------------------------------------------------

const staffLinkSubject = "Confirm the link to your FundZim staff account"

func staffLinkOf(t *testing.T, staffID string) string {
	t.Helper()
	var p *string
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT staff_personal_user_id FROM app.users WHERE id = $1`, staffID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		return ""
	}
	return *p
}

func TestStaffPersonalLink(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	adminA, adminB := s.freshStaff(t, f.adminA), s.freshStaff(t, f.adminB)
	x := inviteStaff(t, s, adminA, adminB, "Link Staff X", "")
	y := inviteStaff(t, s, adminA, adminB, "Link Staff Y", "")
	sx, sy := s.freshStaff(t, x), s.freshStaff(t, y)
	p := verifiedUser(t, s, "link-p")
	q := verifiedUser(t, s, "link-q")

	// non-staff callers do not even see the routes; staff cannot confirm (personal session only)
	p.expect(p.do("POST", "/admin/me/personal-account-link", map[string]string{"email": p.email}), 404, "")
	p.expect(p.do("GET", "/admin/me/personal-account-link", nil), 404, "")
	sx.expect(sx.do("POST", "/me/staff-link/confirm", map[string]string{"token": strings.Repeat("A", 43)}), 403, "PERMISSION_DENIED")

	// request (fresh step-up): pending, masked
	sx.expect(sx.do("POST", "/admin/me/personal-account-link", map[string]string{"email": "not-an-email"}), 422, "")
	sx.expect(sx.do("POST", "/admin/me/personal-account-link", map[string]string{"email": strings.ToUpper(p.email)}), 202, "")
	r := sx.do("GET", "/admin/me/personal-account-link", nil)
	sx.expect(r, 200, "")
	if !strings.Contains(string(r.Data), `"linked":false`) || !strings.Contains(string(r.Data), "***@example.test") || strings.Contains(string(r.Data), p.email) {
		t.Fatalf("pending status: %s", r.Data)
	}
	tok := tokenFrom(t, mailTo(t, p.email, staffLinkSubject))
	// only the hash is stored
	if n := countRows(t, s.d.DB, `SELECT count(*) FROM app.staff_link_requests WHERE staff_user_id = $1 AND token_hash IS NOT NULL
		AND position(convert_to($2, 'UTF8') in token_hash) = 0`, x.id, tok); n != 1 {
		t.Fatalf("token hash rows: %d", n)
	}

	// wrong account: refused, token stays usable by the right account
	q.expect(q.do("POST", "/me/staff-link/confirm", map[string]string{"token": tok}), 403, "STAFF_LINK_EMAIL_MISMATCH")
	if staffLinkOf(t, x.id) != "" {
		t.Fatal("linked by the wrong account")
	}
	// happy path
	r = p.do("POST", "/me/staff-link/confirm", map[string]string{"token": tok})
	p.expect(r, 200, "")
	if staffLinkOf(t, x.id) != p.id {
		t.Fatal("link not recorded")
	}
	for _, target := range []string{x.id, p.id} {
		if n := countAudit(t, "audit.security_audit_events", "auth.staff_link.confirmed", target); n != 1 {
			t.Fatalf("link audit on %s: %d", target, n)
		}
	}
	mailTo(t, p.email, "Your FundZim account is now linked to a staff account")
	mailTo(t, x.email, "Your FundZim staff account is now linked")
	r = sx.do("GET", "/admin/me/personal-account-link", nil)
	if !strings.Contains(string(r.Data), `"linked":true`) {
		t.Fatalf("linked status: %s", r.Data)
	}
	// reused token
	p.expect(p.do("POST", "/me/staff-link/confirm", map[string]string{"token": tok}), 400, "TOKEN_INVALID")
	// a staff member already linked cannot request again (no unlink, no relink)
	sx.expect(sx.do("POST", "/admin/me/personal-account-link", map[string]string{"email": q.email}), 409, "STAFF_ALREADY_LINKED")
	// the database refuses removing or changing the link from the application role
	if _, err := s.d.DB.Exec(ctx(t), `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, x.id); err == nil ||
		!strings.Contains(err.Error(), "cannot be changed or removed") {
		t.Fatalf("app role removed a link: %v", err)
	}
	// app.actor_identities resolves both directions
	var idents []string
	if err := s.d.DB.QueryRow(ctx(t), `SELECT app.actor_identities($1)::text[]`, p.id).Scan(&idents); err != nil {
		t.Fatal(err)
	}
	sort.Strings(idents)
	want := []string{p.id, x.id}
	sort.Strings(want)
	if strings.Join(idents, ",") != strings.Join(want, ",") {
		t.Fatalf("actor_identities(personal): %v", idents)
	}

	// a personal account already linked to another staff member: no email is sent, and a token issued before
	// the other link is refused at confirmation
	sy.expect(sy.do("POST", "/admin/me/personal-account-link", map[string]string{"email": p.email}), 202, "")
	if n := countRows(t, s.d.DB, `SELECT count(*) FROM app.staff_link_requests WHERE staff_user_id = $1 AND consumed_at IS NULL
		AND invalidated_at IS NULL AND target_user_id IS NULL`, y.id); n != 1 {
		t.Fatalf("request for an already linked account must have no target: %d", n)
	}
	sy.expect(sy.do("POST", "/admin/me/personal-account-link", map[string]string{"email": q.email}), 202, "")
	tokQ := tokenFrom(t, mailTo(t, q.email, staffLinkSubject))
	w := ids.New() // another staff account links q first (as the migrator, standing in for a concurrent confirmation)
	migratorExec(t, `INSERT INTO app.users (id, account_kind, status, staff_personal_user_id) VALUES ($1, 'STAFF', 'ACTIVE', $2)`, w, q.id)
	q.expect(q.do("POST", "/me/staff-link/confirm", map[string]string{"token": tokQ}), 409, "PERSONAL_ACCOUNT_ALREADY_LINKED")
	if staffLinkOf(t, y.id) != "" {
		t.Fatal("double link recorded")
	}

	// expired token
	o := verifiedUser(t, s, "link-o")
	sy.expect(sy.do("POST", "/admin/me/personal-account-link", map[string]string{"email": o.email}), 202, "")
	tokO := tokenFrom(t, mailTo(t, o.email, staffLinkSubject))
	err := db.WithTx(ctx(t), pool(t, os.Getenv("DATABASE_MIGRATION_URL")), db.TxOptions{}, func(c context.Context, tx pgx.Tx) error {
		// the guard keeps requests immutable; the migrator lifts it for this test only to age the request
		if _, err := tx.Exec(c, `ALTER TABLE app.staff_link_requests DISABLE TRIGGER trg_staff_link_requests_guard`); err != nil {
			return err
		}
		if _, err := tx.Exec(c, `UPDATE app.staff_link_requests SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day'
			WHERE staff_user_id = $1 AND target_user_id = $2 AND consumed_at IS NULL AND invalidated_at IS NULL`, y.id, o.id); err != nil {
			return err
		}
		_, err := tx.Exec(c, `ALTER TABLE app.staff_link_requests ENABLE TRIGGER trg_staff_link_requests_guard`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	o.expect(o.do("POST", "/me/staff-link/confirm", map[string]string{"token": tokO}), 400, "TOKEN_INVALID")
	if staffLinkOf(t, y.id) != "" {
		t.Fatal("expired token linked")
	}
}

// ---- 4. database self-decision guard over linked identities ------------------------------------------------

func TestKYCDecisionTriggerRefusesLinkedIdentity(t *testing.T) {
	s := newITServer(t, nil)
	staffFixture(t, s)
	owner := verifiedUser(t, s, "kyc-link-db")
	caseID := submittedKYC(t, owner, uniqueIDNumber())
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	linked, other := ids.New(), ids.New()
	migratorExec(t, `INSERT INTO app.users (id, account_kind, status, staff_personal_user_id) VALUES ($1, 'STAFF', 'ACTIVE', $2)`, linked, owner.id)
	migratorExec(t, `INSERT INTO app.users (id, account_kind, status) VALUES ($1, 'STAFF', 'ACTIVE')`, other)
	var evidence string
	if err := mig.QueryRow(ctx(t), `SELECT id FROM audit.evidence_records ORDER BY collected_at DESC LIMIT 1`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	insert := func(decider string, second any) error {
		// always rolled back: the point is whether the trigger lets the row in
		return db.WithTx(ctx(t), s.d.Verification.KYCPool, db.TxOptions{}, func(c context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(c, `INSERT INTO kyc.kyc_decisions (id, kyc_case_id, outcome, from_status, resulting_status, reason_code, conditions,
				evidence_record_id, decided_by, second_approval_required, second_approver_id, policy_version, audit_event_id)
				VALUES ($1, $2, 'REJECT', 'SUBMITTED', 'REJECTED', 'LINKED_IDENTITY_TEST', '{}', $3, $4, $5, $6, 'v2', $7)`,
				ids.New(), caseID, evidence, decider, second != nil, second, ids.New())
			if err != nil {
				return err
			}
			return errRollback
		})
	}
	// the staff account linked to the subject is refused by the database (the Go service is bypassed here)
	if err := insert(linked, nil); sqlState(err) != "23514" || !strings.Contains(err.Error(), "cannot decide their own verification") {
		t.Fatalf("linked decider: %v", err)
	}
	// ... also as second approver
	if err := insert(other, linked); sqlState(err) != "23514" {
		t.Fatalf("linked second approver: %v", err)
	}
	// control: an unrelated reviewer passes the trigger
	if err := insert(other, nil); err != errRollback {
		t.Fatalf("unrelated reviewer: %v", err)
	}
}

var errRollback = errorString("rollback")

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

var phoneSeq atomic.Uint32

func uniquePhoneSuffix() string {
	return fmt.Sprintf("%07d", (time.Now().UnixNano()/1000+int64(phoneSeq.Add(7919)))%10_000_000)
}

type errorString string

func (e errorString) Error() string { return string(e) }
