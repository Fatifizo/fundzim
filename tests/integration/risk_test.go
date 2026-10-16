//go:build integration

package integration

// Stage 13 risk (ADR-045) against the real stack. Isolation on the shared development database: every rule a test
// activates matches only signals carrying the test's own random reason code (fact_compare) and is retired at the end;
// holds are placed on the test's own campaign; screening uses synthetic TEST references. A score is never proof of
// fraud and nothing here is a screening of a real person.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/risk"
)

type riskIT struct {
	*payoutIT
	rs *risk.Service
}

func newRiskIT(t *testing.T) *riskIT {
	t.Helper()
	p := newPayoutIT(t)
	rs := p.s.d.Verification.Risk
	if rs == nil || rs.Screening == nil {
		t.Fatal("risk module or screening simulator not wired (RISK_SCREENING_SANDBOX_ENABLED)")
	}
	return &riskIT{payoutIT: p, rs: rs}
}

// act runs a staff request with an Idempotency-Key, stepping up with TOTP when asked.
func (r *riskIT) act(t *testing.T, c staffCred, method, path string, body any, want int, code string) apiResp {
	t.Helper()
	b := r.s.staff(t, c)
	resp := b.do(method, path, body, "Idempotency-Key", ids.New())
	if resp.Status == 403 && resp.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		resp = b.do(method, path, body, "Idempotency-Key", ids.New())
	}
	if want != 0 {
		b.expect(resp, want, code)
	}
	return resp
}

func itCode(prefix string) string {
	return prefix + "_" + strings.ToUpper(strings.ReplaceAll(ids.New()[24:], "-", ""))
}

// rule creates a rule and drives it to ACTIVE: maker compliance, checker compliance2 (retired at cleanup).
func (r *riskIT) rule(t *testing.T, subject, category string, def map[string]any, contribution int, action string, hold map[string]any, prio string) (string, string) {
	t.Helper()
	ver := map[string]any{"definition": def, "contribution": contribution, "severity": "HIGH", "recommended_action": action, "case_priority": prio}
	for k, v := range hold {
		ver[k] = v
	}
	body := map[string]any{"code": itCode("IT_RULE"), "name": "IT rule " + ids.New()[:8], "subject_type": subject, "category": category, "version": ver}
	resp := r.act(t, r.f.compliance, "POST", "/admin/risk/rules", body, 201, "")
	var rr risk.RiskRule
	if err := json.Unmarshal(resp.Data, &rr); err != nil {
		t.Fatal(err)
	}
	vid := rr.Versions[0].ID
	r.act(t, r.f.compliance, "POST", "/admin/risk/rule-versions/"+vid+"/submit", nil, 200, "")
	r.act(t, r.f.compliance2, "POST", "/admin/risk/rule-versions/"+vid+"/approve", map[string]any{"reason": "IT rule reviewed by a checker"}, 200, "")
	r.act(t, r.f.compliance, "POST", "/admin/risk/rule-versions/"+vid+"/activate", nil, 200, "")
	t.Cleanup(func() {
		_, _ = r.rs.RetireRuleVersion(ctx(t), risk.RuleOp{StaffID: r.f.compliance.id, StepUpAt: time.Now(), VersionID: vid, Reason: "IT cleanup: retire test rule"})
	})
	return rr.ID, vid
}

func (r *riskIT) signal(t *testing.T, typ, subjectType, subjectID string, facts map[string]any) risk.Outcome {
	t.Helper()
	out, err := r.rs.RecordSignal(ctx(t), risk.Signal{Type: typ, SubjectType: subjectType, SubjectID: subjectID, SourceModule: "campaigns",
		SourceEventID: ids.New(), SourceEventType: "campaigns.report_escalated", ObservedAt: time.Now().UTC(), Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *riskIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := r.mig.QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func reportsRule(code string, n int) map[string]any {
	return map[string]any{"when": map[string]any{"all": []any{
		map[string]any{"fact_compare": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "fact": "reason_code", "window_seconds": 3600, "op": "=", "value": code}},
		map[string]any{"signal_count": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "window_seconds": 3600, "op": ">=", "value": n}},
	}}}
}

// TestRiskRuleLifecycleAndSafety: declarative rules only, bounded, maker-checker (Go and database), immutable after
// submission, lifecycle with step-up, one active version per rule.
func TestRiskRuleLifecycleAndSafety(t *testing.T) {
	r := newRiskIT(t)
	c1, c2 := r.f.compliance, r.f.compliance2
	ver := func(def any) map[string]any {
		return map[string]any{"definition": def, "contribution": 10, "severity": "LOW", "recommended_action": "ALLOW_WITH_MONITORING", "case_priority": "P4"}
	}
	newRule := func(def any, name string) apiResp {
		return r.act(t, c1, "POST", "/admin/risk/rules", map[string]any{"code": itCode("IT_SAFE"), "name": name, "subject_type": "CAMPAIGN",
			"category": "CAMPAIGN_RISK", "version": ver(def)}, 0, "")
	}
	// rule safety: unknown signal, unknown field (no code), too deep, unbounded window, fraud wording, automated hold misuse
	deep := map[string]any{"signal_count": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "window_seconds": 60, "op": ">=", "value": 1}}
	for i := 0; i < 4; i++ {
		deep = map[string]any{"all": []any{deep}}
	}
	for name, def := range map[string]any{
		"unknown signal": map[string]any{"when": map[string]any{"signal_count": map[string]any{"signal_types": []string{"NOT_A_SIGNAL"}, "window_seconds": 60, "op": ">=", "value": 1}}},
		"code field":     map[string]any{"when": map[string]any{"script": "os.exit(1)"}},
		"too deep":       map[string]any{"when": deep},
		"window":         map[string]any{"when": map[string]any{"signal_count": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "window_seconds": 999999999, "op": ">=", "value": 1}}},
		"pii fact":       map[string]any{"when": map[string]any{"fact_compare": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"}, "fact": "ip_address", "window_seconds": 60, "op": "=", "value": "1.2.3.4"}}},
	} {
		if resp := newRule(def, "IT unsafe "+name); resp.Status != 422 {
			t.Fatalf("%s: rule accepted (%d %s)", name, resp.Status, resp.Error.Code)
		}
	}
	good := reportsRule(itCode("IT"), 1)
	r.s.staff(t, c1).expect(newRule(good, "IT fraud detector"), 422, "VALIDATION_FAILED")
	hold := ver(good)
	hold["recommended_action"], hold["hold_type"], hold["hold_duration_seconds"] = "TEMPORARY_HOLD", "CAMPAIGN_HOLD", 3600
	r.act(t, c1, "POST", "/admin/risk/rules", map[string]any{"code": itCode("IT_HOLD"), "name": "IT disruptive hold", "subject_type": "CAMPAIGN",
		"category": "CAMPAIGN_RISK", "version": hold}, 422, "VALIDATION_FAILED")
	hold["hold_type"], hold["hold_duration_seconds"] = "WITHDRAWAL_HOLD", 30*24*3600
	r.act(t, c1, "POST", "/admin/risk/rules", map[string]any{"code": itCode("IT_HOLD"), "name": "IT long hold", "subject_type": "CAMPAIGN",
		"category": "CAMPAIGN_RISK", "version": hold}, 422, "VALIDATION_FAILED")

	resp := newRule(good, "IT lifecycle rule")
	r.s.staff(t, c1).expect(resp, 201, "")
	var rr risk.RiskRule
	_ = json.Unmarshal(resp.Data, &rr)
	vid := rr.Versions[0].ID
	t.Cleanup(func() {
		_, _ = r.rs.RetireRuleVersion(ctx(t), risk.RuleOp{StaffID: c1.id, StepUpAt: time.Now(), VersionID: vid, Reason: "IT cleanup: retire"})
	})
	// a draft is editable; approval before submission is a state error; the maker cannot approve (route and service)
	r.act(t, c1, "PUT", "/admin/risk/rule-versions/"+vid, ver(reportsRule(itCode("IT"), 2)), 200, "")
	r.act(t, c2, "POST", "/admin/risk/rule-versions/"+vid+"/approve", map[string]any{"reason": "not yet submitted"}, 409, "RISK_RULE_STATE")
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/activate", nil, 409, "RISK_RULE_STATE") // activation bypass
	sub := r.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/submit", nil, 200, "")
	var v risk.RuleVersion
	_ = json.Unmarshal(sub.Data, &v)
	if v.DefinitionHash == nil || len(*v.DefinitionHash) != 64 {
		t.Fatalf("submitted version has no definition hash: %+v", v.DefinitionHash)
	}
	r.act(t, c1, "PUT", "/admin/risk/rule-versions/"+vid, ver(good), 409, "RISK_RULE_STATE")
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/approve", map[string]any{"reason": "maker approving own rule"}, 403, "SELF_APPROVAL_FORBIDDEN")
	if _, err := r.rs.DecideRuleVersion(ctx(t), risk.RuleOp{StaffID: c1.id, StepUpAt: time.Now(), VersionID: vid, Reason: "maker approving"}, true); !errors.Is(err, risk.ErrSelfApproval) {
		t.Fatalf("maker approval: %v", err)
	}
	// the database refuses a maker approval and any change to a submitted definition, whatever the caller
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_rule_versions SET contribution = 99 WHERE id = $1`, vid); err == nil {
		t.Fatal("submitted definition changed in the database")
	}
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_rule_versions SET status = 'APPROVED', approved_by = created_by, approved_at = now(),
		approved_step_up_at = now(), approval_reason = 'x' WHERE id = $1`, vid); err == nil {
		t.Fatal("database accepted a maker approval")
	}
	r.act(t, c2, "POST", "/admin/risk/rule-versions/"+vid+"/approve", map[string]any{"reason": "checked", "definition_hash": strings.Repeat("0", 64)}, 409, "RISK_RULE_CHANGED")
	r.act(t, c2, "POST", "/admin/risk/rule-versions/"+vid+"/approve", map[string]any{"reason": "IT rule checked", "definition_hash": *v.DefinitionHash}, 200, "")
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/activate", nil, 200, "")
	// FINANCE may view but not change rules; SUPPORT sees nothing
	r.act(t, r.f.finance1, "GET", "/admin/risk/rules/"+rr.ID, nil, 200, "")
	if resp := r.act(t, r.f.finance1, "POST", "/admin/risk/rule-versions/"+vid+"/suspend", map[string]any{"reason": "finance tries"}, 0, ""); resp.Status != 403 && resp.Status != 404 {
		t.Fatalf("finance suspended a rule: %d", resp.Status)
	}
	if resp := r.act(t, r.f.support, "GET", "/admin/risk/overview", nil, 0, ""); resp.Status != 403 && resp.Status != 404 {
		t.Fatalf("support saw the risk overview: %d", resp.Status)
	}
	// suspend; the suspender cannot reactivate; another checker can
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/suspend", map[string]any{"reason": "IT suspend"}, 200, "")
	if _, err := r.rs.ReactivateRuleVersion(ctx(t), risk.RuleOp{StaffID: c1.id, StepUpAt: time.Now(), VersionID: vid, Reason: "own suspension"}); !errors.Is(err, risk.ErrSelfApproval) {
		t.Fatalf("suspender reactivated: %v", err)
	}
	r.act(t, c2, "POST", "/admin/risk/rule-versions/"+vid+"/reactivate", map[string]any{"reason": "IT reactivate"}, 200, "")
	// a second version supersedes the first on activation (one active version per rule)
	v2 := r.act(t, c1, "POST", "/admin/risk/rules/"+rr.ID+"/versions", ver(reportsRule(itCode("IT"), 3)), 201, "").field(t, "id")
	t.Cleanup(func() {
		_, _ = r.rs.RetireRuleVersion(ctx(t), risk.RuleOp{StaffID: c1.id, StepUpAt: time.Now(), VersionID: v2, Reason: "IT cleanup: retire"})
	})
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+v2+"/submit", nil, 200, "")
	r.act(t, c2, "POST", "/admin/risk/rule-versions/"+v2+"/approve", map[string]any{"reason": "IT v2 checked"}, 200, "")
	r.act(t, c1, "POST", "/admin/risk/rule-versions/"+v2+"/activate", nil, 200, "")
	if n := r.count(t, `SELECT count(*) FROM risk.risk_rule_versions WHERE rule_id = $1 AND status = 'ACTIVE'`, rr.ID); n != 1 {
		t.Fatalf("%d active versions", n)
	}
	if st := r.count(t, `SELECT count(*) FROM risk.risk_rule_versions WHERE id = $1 AND status = 'RETIRED'`, vid); st != 1 {
		t.Fatal("superseded version not retired")
	}
	if n := r.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'risk.rule_version_changed' AND aggregate_id = $1`, v2); n < 3 {
		t.Fatalf("rule version outbox events = %d", n)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_events WHERE aggregate_id = $1`, vid); n < 6 {
		t.Fatalf("rule history = %d", n)
	}
}

// TestRiskEvaluationActionsAndHolds: the pipeline (signal → evaluation → explanation → case → automated temporary hold),
// idempotent per signal; the hold blocks a payout (EC-12) until the case decision (two people) releases it.
func TestRiskEvaluationActionsAndHolds(t *testing.T) {
	r := newRiskIT(t)
	camp := r.campaign(t, 50_000)
	code, monCode := itCode("IT"), itCode("ITM")
	r.rule(t, "CAMPAIGN", "CAMPAIGN_RISK", reportsRule(code, 2), 60, "TEMPORARY_HOLD",
		map[string]any{"hold_type": "WITHDRAWAL_HOLD", "hold_duration_seconds": 3600}, "P2")
	r.rule(t, "CAMPAIGN", "CAMPAIGN_RISK", map[string]any{"when": map[string]any{"fact_compare": map[string]any{"signal_types": []string{"CAMPAIGN_REPORTED"},
		"fact": "reason_code", "window_seconds": 3600, "op": "=", "value": monCode}}}, 10, "ALLOW_WITH_MONITORING", nil, "P4")

	// one matching signal: below the count, score 0, ALLOW
	o1 := r.signal(t, risk.SigCampaignReported, "CAMPAIGN", camp, map[string]any{"reason_code": code})
	if o1.EvaluationID == "" || o1.Score2 != 0 || o1.RecommendedAction != risk.ActAllow || o1.CaseID != "" {
		t.Fatalf("first signal: %+v", o1)
	}
	// monitoring rule (on another campaign, below the hold rule's count): ALLOW_WITH_MONITORING records a monitoring event, no case
	om := r.signal(t, risk.SigCampaignReported, "CAMPAIGN", r.campaign(t, 0), map[string]any{"reason_code": monCode})
	if om.RecommendedAction != risk.ActMonitor || om.CaseID != "" {
		t.Fatalf("monitoring signal: %+v", om)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_monitoring_events WHERE event_type = 'RULE_MONITORING_MATCH' AND detail->>'evaluation_id' = $1`, om.EvaluationID); n != 1 {
		t.Fatalf("monitoring events = %d", n)
	}
	// the second matching signal fires the hold rule: HIGH, TEMPORARY_HOLD, case + automated withdrawal hold on the campaign
	o2 := r.signal(t, risk.SigCampaignReported, "CAMPAIGN", camp, map[string]any{"reason_code": code})
	if o2.Score2 < 60 || o2.Classification != risk.ClassHigh || o2.RecommendedAction != risk.ActTemporaryHold || o2.CaseID == "" || o2.HoldID == "" {
		t.Fatalf("second signal: %+v", o2)
	}
	ev, err := r.rs.EvaluationDetail(ctx(t), o2.EvaluationID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ev.Explanation), "contribution") || len(ev.InputsHash) != 64 || !strings.Contains(ev.Notice, "not proof of fraud") {
		t.Fatalf("evaluation not explained: %s %s", ev.Explanation, ev.Notice)
	}
	h, err := r.rs.Hold(ctx(t), o2.HoldID)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Automated || h.Status != risk.HoldActive || h.HoldType != risk.HoldWithdrawal || h.SubjectID != camp || h.ExpiresAt == nil ||
		h.ExpiresAt.Sub(*h.ActivatedAt) > 7*24*time.Hour {
		t.Fatalf("automated hold: %+v", h)
	}
	// a third signal links to the same case and the existing hold (deduplication)
	o3 := r.signal(t, risk.SigCampaignReported, "CAMPAIGN", camp, map[string]any{"reason_code": code})
	if o3.CaseID != o2.CaseID || o3.HoldID != o2.HoldID {
		t.Fatalf("not deduplicated: %+v vs %+v", o3, o2)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_cases WHERE subject_id = $1 AND status NOT IN ('RESOLVED','CLOSED')`, camp); n != 1 {
		t.Fatalf("%d open cases", n)
	}
	// a redelivered event (same source event) changes nothing
	src := ids.New()
	sig := risk.Signal{Type: risk.SigCampaignReported, SubjectType: "CAMPAIGN", SubjectID: camp, SourceModule: "campaigns", SourceEventID: src,
		SourceEventType: "campaigns.report_escalated", ObservedAt: time.Now().UTC(), Facts: map[string]any{"reason_code": code}}
	a, err := r.rs.RecordSignal(ctx(t), sig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.rs.RecordSignal(ctx(t), sig)
	if err != nil || !b.Duplicate || a.Duplicate {
		t.Fatalf("redelivery: %+v %+v %v", a, b, err)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_evaluations e JOIN risk.risk_signals s ON s.id = e.trigger_signal_id WHERE s.source_event_id = $1`, src); n != 1 {
		t.Fatalf("%d evaluations for one event", n)
	}
	// enforcement: the payout is refused (EC-12, coarse owner reason) and staff cannot approve one under the hold
	r.request(t, camp, 1_000, 422, "PAYOUT_NOT_ELIGIBLE")
	// the hold is visible to staff only, never to the owner
	if resp := r.owner.do("GET", "/admin/risk/holds/"+o2.HoldID, nil); resp.Status != 404 {
		t.Fatalf("owner reached a risk route: %d", resp.Status)
	}
	// the case: assign, triage, propose FALSE_POSITIVE (c1), self-approval refused, approval by c2 releases the hold
	c1, c2 := r.f.compliance, r.f.compliance2
	base := "/admin/risk/cases/" + o2.CaseID
	r.act(t, c1, "POST", base+"/assign", map[string]any{}, 200, "")
	r.act(t, c1, "POST", base+"/transition", map[string]any{"to": "UNDER_INVESTIGATION", "reason": "looking"}, 200, "")
	var sigIDs []string
	rows, err := r.mig.Query(ctx(t), `SELECT reference_id FROM risk.risk_case_evidence WHERE case_id = $1 AND evidence_type = 'SIGNAL'`, o2.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		sigIDs = append(sigIDs, s)
	}
	rows.Close()
	if len(sigIDs) < 2 {
		t.Fatalf("signal evidence: %v", sigIDs)
	}
	r.act(t, c1, "POST", base+"/signal-reviews", map[string]any{"signal_id": sigIDs[0], "classification": "FALSE_POSITIVE",
		"explanation": "IT: report reason code is a test artefact"}, 200, "")
	r.act(t, c1, "POST", base+"/signal-reviews", map[string]any{"signal_id": sigIDs[0], "classification": "RELEVANT",
		"explanation": "again"}, 409, "RISK_SIGNAL_ALREADY_REVIEWED")
	r.act(t, c1, "POST", base+"/propose-decision", map[string]any{"outcome": "FALSE_POSITIVE", "reason": "IT: reports were synthetic"}, 200, "")
	r.act(t, c1, "POST", base+"/approve-decision", map[string]any{"reason": "own proposal"}, 403, "SELF_APPROVAL_FORBIDDEN")
	r.act(t, c2, "POST", base+"/approve-decision", map[string]any{"reason": "IT: checked the evidence"}, 200, "")
	h, _ = r.rs.Hold(ctx(t), o2.HoldID)
	if h.Status != risk.HoldReleased || h.ReleasedBy == nil || *h.ReleasedBy != c2.id || h.ReleaseRequestedBy == nil || *h.ReleaseRequestedBy != c1.id {
		t.Fatalf("hold after the decision: %+v", h)
	}
	// signals are never deleted, and the false positive stays labelled
	if n := r.count(t, `SELECT count(*) FROM risk.risk_signals WHERE id = $1`, sigIDs[0]); n != 1 {
		t.Fatal("signal deleted")
	}
	if n := r.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type IN ('risk.hold_activated','risk.hold_released') AND aggregate_id = $1`, o2.HoldID); n != 2 {
		t.Fatalf("hold outbox events = %d", n)
	}
	// the payout is no longer held
	id := r.request(t, camp, 1_000, 202, "")
	if id == "" {
		t.Fatal("payout not requested after the release")
	}
	if _, err := r.po.CancelByStaff(ctx(t), r.f.finance1.id, id, "IT cleanup: cancel before submission"); err != nil {
		t.Logf("cleanup cancel: %v", err)
	}
}

// TestRiskStaffHoldsMakerChecker: disruptive holds need a second person; releases are maker-checker with step-up and
// never by anyone linked to the requester or the held user; holds stop donations and publication.
func TestRiskStaffHoldsMakerChecker(t *testing.T) {
	r := newRiskIT(t)
	camp := r.campaign(t, 0)
	c1, c2 := r.f.compliance, r.f.compliance2
	// a DONATION_HOLD is REQUESTED: not enforced until a second person activates it
	resp := r.act(t, c1, "POST", "/admin/risk/holds", map[string]any{"hold_type": "DONATION_HOLD", "subject_type": "CAMPAIGN", "subject_id": camp,
		"reason_code": "IT_REVIEW", "description": "IT: donation pause for review"}, 201, "")
	hid := resp.field(t, "id")
	if st := resp.field(t, "status"); st != "REQUESTED" {
		t.Fatalf("donation hold status %s", st)
	}
	donor := verifiedUser(t, r.s, "rdonor")
	intent := func(want int, code string) {
		t.Helper()
		donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": camp, "amount_minor": "500", "currency": "USD"},
			"Idempotency-Key", ids.New()), want, code)
	}
	intent(201, "")
	r.act(t, c1, "POST", "/admin/risk/holds/"+hid+"/activate", map[string]any{"reason": "IT: own approval attempt"}, 403, "SELF_APPROVAL_FORBIDDEN")
	r.act(t, c2, "POST", "/admin/risk/holds/"+hid+"/activate", map[string]any{"reason": "IT: confirmed"}, 200, "")
	intent(409, "DONATIONS_ON_HOLD")
	r.act(t, c1, "POST", "/admin/risk/holds", map[string]any{"hold_type": "DONATION_HOLD", "subject_type": "CAMPAIGN", "subject_id": camp,
		"reason_code": "IT_REVIEW", "description": "IT: duplicate"}, 409, "RISK_HOLD_EXISTS")
	// release: maker c2, the maker cannot approve, c1 approves
	r.act(t, c2, "POST", "/admin/risk/holds/"+hid+"/request-release", map[string]any{"reason": "IT: review done"}, 200, "")
	intent(409, "DONATIONS_ON_HOLD") // still enforced while RELEASE_PENDING
	r.act(t, c2, "POST", "/admin/risk/holds/"+hid+"/approve-release", map[string]any{"reason": "IT: own approval attempt"}, 403, "SELF_APPROVAL_FORBIDDEN")
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_holds SET status = 'RELEASED', released_by = release_requested_by, released_at = now(),
		release_step_up_at = now() WHERE id = $1`, hid); err == nil {
		t.Fatal("database accepted a self-release")
	}
	r.act(t, c1, "POST", "/admin/risk/holds/"+hid+"/approve-release", map[string]any{"reason": "IT: release checked"}, 200, "")
	intent(201, "")

	// linked accounts: a staff member linked to the held user can neither request nor approve the release
	held := basicUser(t, r.s, "rheld")
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, c2.id, held.id)
	t.Cleanup(func() { migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, c2.id) })
	uh := r.act(t, c1, "POST", "/admin/risk/holds", map[string]any{"hold_type": "WITHDRAWAL_HOLD", "subject_type": "USER", "subject_id": held.id,
		"reason_code": "IT_LINKED", "description": "IT: linked-account hold"}, 201, "").field(t, "id")
	r.act(t, c2, "POST", "/admin/risk/holds/"+uh+"/request-release", map[string]any{"reason": "my own account"}, 403, "RISK_CONFLICT_OF_INTEREST")
	r.act(t, c1, "POST", "/admin/risk/holds/"+uh+"/request-release", map[string]any{"reason": "IT: review done"}, 200, "")
	r.act(t, c2, "POST", "/admin/risk/holds/"+uh+"/approve-release", map[string]any{"reason": "IT: linked approval attempt"}, 403, "RISK_CONFLICT_OF_INTEREST")
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, c2.id)
	r.act(t, c2, "POST", "/admin/risk/holds/"+uh+"/approve-release", map[string]any{"reason": "IT: unlinked checker"}, 200, "")

	// a staff hold placed on yourself is refused
	r.act(t, c1, "POST", "/admin/risk/holds", map[string]any{"hold_type": "WITHDRAWAL_HOLD", "subject_type": "USER", "subject_id": c1.id,
		"reason_code": "IT_SELF", "description": "IT: self"}, 403, "RISK_CONFLICT_OF_INTEREST")

	// publication: a CAMPAIGN_HOLD on an approved campaign stops its publication
	u, pc := readyCampaign(t, r.s, r.f, "rpub-"+ids.New()[:6], "EDUCATION")
	u.expect(u.do("POST", "/campaigns/"+pc+"/submit", nil), 200, "")
	approveOnly(t, r.s, r.f, pc)
	ch := r.act(t, c1, "POST", "/admin/risk/holds", map[string]any{"hold_type": "CAMPAIGN_HOLD", "subject_type": "CAMPAIGN", "subject_id": pc,
		"reason_code": "IT_PUBLICATION", "description": "IT: hold publication"}, 201, "").field(t, "id")
	r.act(t, c2, "POST", "/admin/risk/holds/"+ch+"/activate", map[string]any{"reason": "IT"}, 200, "")
	u.expect(u.do("POST", "/campaigns/"+pc+"/publish", nil), 409, "CAMPAIGN_ON_HOLD")
	r.act(t, c1, "POST", "/admin/risk/holds/"+ch+"/request-release", map[string]any{"reason": "IT: done"}, 200, "")
	r.act(t, c2, "POST", "/admin/risk/holds/"+ch+"/approve-release", map[string]any{"reason": "IT: done"}, 200, "")
	u.expect(u.do("POST", "/campaigns/"+pc+"/publish", nil), 200, "")
}

// TestRiskCaseWorkflowAndEscalation: manual case, conflict of interest, immutable hashed evidence with supersession,
// access log, escalation to compliance, reopen.
func TestRiskCaseWorkflowAndEscalation(t *testing.T) {
	r := newRiskIT(t)
	c1, c2 := r.f.compliance, r.f.compliance2
	subject := basicUser(t, r.s, "rcase")
	// a case about yourself is refused
	r.act(t, c1, "POST", "/admin/risk/cases", map[string]any{"case_type": "IDENTITY_RISK", "subject_type": "USER", "subject_id": c1.id, "priority": "P3",
		"reason": "IT: about me"}, 403, "RISK_CONFLICT_OF_INTEREST")
	cid := r.act(t, c1, "POST", "/admin/risk/cases", map[string]any{"case_type": "IDENTITY_RISK", "subject_type": "USER", "subject_id": subject.id,
		"priority": "P3", "reason": "IT: manual referral"}, 201, "").field(t, "id")
	base := "/admin/risk/cases/" + cid
	// assigning to someone without risk.case.work is refused
	r.act(t, c1, "POST", base+"/assign", map[string]any{"assignee_id": r.f.support.id}, 422, "VALIDATION_FAILED")
	r.act(t, c1, "POST", base+"/assign", map[string]any{"assignee_id": c2.id, "reason": "IT: to colleague"}, 200, "")
	r.act(t, c1, "POST", base+"/transition", map[string]any{"to": "RESOLVED"}, 409, "RISK_CASE_STATE")
	r.act(t, c2, "POST", base+"/transition", map[string]any{"to": "UNDER_INVESTIGATION"}, 200, "")
	ev := r.act(t, c2, "POST", base+"/evidence", map[string]any{"note": "IT: first observation"}, 201, "").field(t, "evidence_id")
	r.act(t, c2, "POST", base+"/evidence", map[string]any{"note": "IT: corrected observation", "supersedes_id": ev}, 201, "")
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_case_evidence SET content = '{"note":"tampered"}' WHERE id = $1`, ev); err == nil {
		t.Fatal("evidence edited")
	}
	d := r.act(t, c1, "GET", base, nil, 200, "")
	var cd risk.CaseDetail
	if err := json.Unmarshal(d.Data, &cd); err != nil {
		t.Fatal(err)
	}
	var orig *risk.Evidence
	for i := range cd.Evidence {
		if !risk.VerifyEvidence(cd.Evidence[i]) {
			t.Fatalf("evidence hash mismatch %s", cd.Evidence[i].ID)
		}
		if cd.Evidence[i].ID == ev {
			orig = &cd.Evidence[i]
		}
	}
	if orig == nil || orig.SupersededBy == nil || orig.RetentionClass != "RISK_RECORD_LEGAL_REVIEW_REQUIRED" {
		t.Fatalf("original evidence: %+v", orig)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_case_access_log WHERE case_id = $1 AND staff_id = $2 AND action = 'VIEW_CASE'`, cid, c1.id); n < 1 {
		t.Fatal("case view not logged")
	}
	// escalate: compliance receives risk.case_escalated and opens a RISK_REVIEW case linked to the risk case
	r.act(t, c2, "POST", base+"/escalate", map[string]any{"reason": "IT: needs compliance"}, 200, "")
	var del outbox.Delivery
	var payload []byte
	if err := r.mig.QueryRow(ctx(t), `SELECT id::text, event_type, aggregate_type, aggregate_id::text, payload, occurred_at FROM app.outbox_events
		WHERE event_type = 'risk.case_escalated' AND aggregate_id = $1`, cid).Scan(&del.EventID, &del.EventType, &del.AggregateType, &del.AggregateID,
		&payload, &del.OccurredAt); err != nil {
		t.Fatal(err)
	}
	del.Payload = payload
	res, err := r.s.d.Verification.Compliance.OpenFromEvent(ctx(t), del) // the compliance.open_case consumer, run inline
	if err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM compliance.compliance_case_links WHERE case_id = $1 AND subject_type = 'RISK_CASE' AND subject_id = $2`,
		res.CaseID, cid); n != 1 {
		t.Fatalf("compliance case not linked to the risk case (%s)", res.CaseID)
	}
	if again, err := r.s.d.Verification.Compliance.OpenFromEvent(ctx(t), del); err != nil || !again.Duplicate {
		t.Fatalf("redelivered escalation: %+v %v", again, err)
	}
	// resolve as escalated (maker c2, checker c1), close, reopen
	r.act(t, c2, "POST", base+"/propose-decision", map[string]any{"outcome": "ESCALATED_TO_COMPLIANCE", "reason": "IT: handed over"}, 200, "")
	r.act(t, c1, "POST", base+"/approve-decision", map[string]any{"reason": "IT: agreed"}, 200, "")
	r.act(t, c1, "POST", base+"/transition", map[string]any{"to": "CLOSED"}, 200, "")
	r.act(t, c1, "POST", base+"/reopen", map[string]any{"reason": "IT: new information"}, 200, "")
	if n := r.count(t, `SELECT count(*) FROM risk.risk_events WHERE aggregate_id = $1 AND event_type = 'CASE_RESOLVED'`, cid); n != 1 {
		t.Fatal("previous decision lost from the history")
	}
	// no human outcome or event ever declares fraud (database)
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_cases SET proposed_outcome = 'FRAUD_CONFIRMED' WHERE id = $1`, cid); err == nil {
		t.Fatal("fraud outcome accepted")
	}
}

// TestRiskScreeningSimulator: every simulator scenario; provider result ≠ analyst decision; failures are never cleared.
func TestRiskScreeningSimulator(t *testing.T) {
	r := newRiskIT(t)
	c1, c2 := r.f.compliance, r.f.compliance2
	sb := r.s.d.Verification.RiskSandbox
	req := func(scenario string) risk.ScreeningRequest {
		t.Helper()
		u := basicUser(t, r.s, "rscr")
		resp := r.act(t, c1, "POST", "/admin/risk/screening", map[string]any{"subject_type": "USER", "subject_id": u.id, "scenario": scenario}, 201, "")
		var x risk.ScreeningRequest
		_ = json.Unmarshal(resp.Data, &x)
		if x.Label != "TEST_ONLY" || x.Environment != "TEST" || !strings.HasPrefix(x.SyntheticReference, "TEST-") {
			t.Fatalf("screening request: %+v", x)
		}
		return x
	}
	ready := func(x risk.ScreeningRequest) {
		t.Helper()
		if _, err := r.mig.Exec(ctx(t), `UPDATE risk.sandbox_screenings SET ready_at = now() - interval '1 second' WHERE request_id = $1`, x.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := sb.DeliverDue(ctx(t), 500); err != nil {
			t.Fatal(err)
		}
	}
	get := func(id string) risk.ScreeningRequest {
		t.Helper()
		x, err := r.rs.Screening1(ctx(t), id)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	results := func(id string) int {
		return r.count(t, `SELECT count(*) FROM risk.risk_screening_results WHERE request_id = $1`, id)
	}

	nm := req("NO_MATCH")
	ready(nm)
	if x := get(nm.ID); x.Status != risk.ScrNoMatch || x.ReviewedBy != nil {
		t.Fatalf("NO_MATCH: %+v (a provider no-match is never CLEARED)", x)
	}
	pm := req("POTENTIAL_MATCH")
	ready(pm)
	x := get(pm.ID)
	if x.Status != risk.ScrPotentialMatch || x.CaseID == nil || x.MatchCount != 1 {
		t.Fatalf("POTENTIAL_MATCH: %+v", x)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_signals WHERE signal_type = 'SCREENING_POTENTIAL_MATCH' AND source_event_id = $1`, pm.ID); n != 1 {
		t.Fatal("no potential-match signal")
	}
	// the requester cannot decide; another analyst confirms → signal, case escalated to compliance, no restriction applied here
	r.act(t, c1, "POST", "/admin/risk/screening/"+pm.ID+"/review", map[string]any{"decision": "CLEARED", "reason": "own request"}, 403, "SELF_APPROVAL_FORBIDDEN")
	r.act(t, c2, "POST", "/admin/risk/screening/"+pm.ID+"/review", map[string]any{"decision": "CONFIRMED_MATCH", "reason": "IT: synthetic match confirmed"}, 200, "")
	if n := r.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'risk.case_escalated' AND aggregate_id = $1`, *x.CaseID); n != 1 {
		t.Fatal("confirmed match not escalated")
	}
	mm := req("MULTIPLE_MATCHES")
	ready(mm)
	if x := get(mm.ID); x.Status != risk.ScrPotentialMatch || x.MatchCount != 3 {
		t.Fatalf("MULTIPLE_MATCHES: %+v", x)
	}
	r.act(t, c2, "POST", "/admin/risk/screening/"+mm.ID+"/review", map[string]any{"decision": "CLEARED", "reason": "IT: synthetic matches cleared"}, 200, "")
	dup := req("DUPLICATE_CALLBACK")
	ready(dup)
	if x := get(dup.ID); x.Status != risk.ScrPotentialMatch || results(dup.ID) != 1 {
		t.Fatalf("DUPLICATE_CALLBACK: %+v results=%d", x, results(dup.ID))
	}
	ooo := req("OUT_OF_ORDER_CALLBACK")
	ready(ooo)
	if x := get(ooo.ID); x.Status != risk.ScrPotentialMatch || x.ResultSequence != 2 ||
		r.count(t, `SELECT count(*) FROM risk.risk_screening_results WHERE request_id = $1 AND NOT applied`, ooo.ID) != 1 {
		t.Fatalf("OUT_OF_ORDER_CALLBACK: %+v", x)
	}
	bad := req("INVALID_SIGNATURE")
	ready(bad)
	if x := get(bad.ID); x.Status != risk.ScrPending || results(bad.ID) != 0 {
		t.Fatalf("INVALID_SIGNATURE applied: %+v", x)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_monitoring_events WHERE event_type = 'SCREENING_CALLBACK_REJECTED' AND occurred_at > now() - interval '5 minutes'`); n < 1 {
		t.Fatal("rejected callback not monitored")
	}
	// polling finds the result the rejected callback carried
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_screening_requests SET next_poll_at = now() - interval '1 second' WHERE id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rs.PollScreenings(ctx(t), 500); err != nil {
		t.Fatal(err)
	}
	if x := get(bad.ID); x.Status != risk.ScrNoMatch {
		t.Fatalf("poll after rejected callback: %+v", x)
	}
	pe := req("PROVIDER_ERROR")
	ready(pe)
	if x := get(pe.ID); x.Status != risk.ScrError {
		t.Fatalf("PROVIDER_ERROR: %+v", x)
	}
	to := req("PROVIDER_TIMEOUT")
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_screening_requests SET timeout_at = now() - interval '1 second', next_poll_at = now() - interval '1 second'
		WHERE id = $1`, to.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rs.PollScreenings(ctx(t), 500); err != nil {
		t.Fatal(err)
	}
	if x := get(to.ID); x.Status != risk.ScrError || x.LastError == nil || *x.LastError != "TIMEOUT" {
		t.Fatalf("PROVIDER_TIMEOUT: %+v (a timeout is ERROR, never cleared)", x)
	}
	// payouts treat an open potential match as review and a confirmed match as blocking, never a timeout as clear
	if st, _ := r.rs.ScreeningStatus(ctx(t), "USER", to.SubjectID); st != risk.ScrError {
		t.Fatalf("screening status %s", st)
	}
	r.act(t, c1, "POST", "/admin/risk/screening/"+to.ID+"/retry", nil, 200, "")
	if x := get(to.ID); x.Status != risk.ScrPending {
		t.Fatalf("retry: %+v", x)
	}
	dl := req("DELAYED_RESULT")
	if _, err := sb.DeliverDue(ctx(t), 500); err != nil {
		t.Fatal(err)
	}
	if x := get(dl.ID); x.Status != risk.ScrPending {
		t.Fatalf("DELAYED_RESULT delivered early: %+v", x)
	}
	ready(dl)
	if x := get(dl.ID); x.Status != risk.ScrNoMatch {
		t.Fatalf("DELAYED_RESULT: %+v", x)
	}
	// the public webhook verifies signatures over the exact bytes
	body := []byte(`{"provider_reference":"SBXS-unknown","event_id":"x","sequence":1,"status":"NO_MATCH_REPORTED","matches":[]}`)
	if code := postScreening(t, r, body, "t=1,v1="+strings.Repeat("0", 64)); code != 401 {
		t.Fatalf("bad signature → %d", code)
	}
	if code := postScreening(t, r, body, risk.SignScreeningCallback(r.rs.ScreeningKey, time.Now(), body)); code != 404 {
		t.Fatalf("unknown reference → %d", code)
	}
	if code := postScreening(t, r, body, risk.SignScreeningCallback(r.rs.ScreeningKey, time.Now().Add(-time.Hour), body)); code != 401 {
		t.Fatalf("stale signature → %d", code)
	}
	if resp := r.act(t, c1, "GET", "/admin/risk/screening/capabilities", nil, 200, ""); !strings.Contains(string(resp.Data), `"production_screening":"BLOCKED"`) {
		t.Fatalf("capabilities: %s", resp.Data)
	}
}

func postScreening(t *testing.T, r *riskIT, body []byte, sig string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", r.s.srv.URL+"/api/v1/webhooks/screening/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(risk.ScreeningSignatureHeader, sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// approveOnly runs the campaign review without publishing.
func approveOnly(t *testing.T, s *itServer, f *verifStaff, id string) {
	t.Helper()
	rev := s.staff(t, f.campRev1)
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/assign", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/start-review", nil), 200, "")
	rev.expect(rev.do("POST", "/admin/campaigns/"+id+"/approve", decision("CHECKLIST_COMPLETE")), 200, "")
}

// TestRiskConcurrentSignalsAndReleases: concurrent signals for one subject produce one case and one hold; concurrent
// release approvals release once.
func TestRiskConcurrentSignalsAndReleases(t *testing.T) {
	r := newRiskIT(t)
	camp := r.campaign(t, 0)
	code := itCode("ITC")
	r.rule(t, "CAMPAIGN", "CAMPAIGN_RISK", reportsRule(code, 1), 80, "TEMPORARY_HOLD", map[string]any{"hold_type": "WITHDRAWAL_HOLD",
		"hold_duration_seconds": 3600}, "P1")
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.rs.RecordSignal(ctx(t), risk.Signal{Type: risk.SigCampaignReported, SubjectType: "CAMPAIGN", SubjectID: camp, SourceModule: "campaigns",
				SourceEventID: ids.New(), SourceEventType: "campaigns.report_escalated", ObservedAt: time.Now().UTC(), Facts: map[string]any{"reason_code": code}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_cases WHERE subject_id = $1`, camp); n != 1 {
		t.Fatalf("%d cases", n)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_holds WHERE subject_id = $1`, camp); n != 1 {
		t.Fatalf("%d holds", n)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_evaluations WHERE subject_id = $1`, camp); n != 20 {
		t.Fatalf("%d evaluations", n)
	}
	var hid string
	_ = r.mig.QueryRow(ctx(t), `SELECT id::text FROM risk.risk_holds WHERE subject_id = $1`, camp).Scan(&hid)
	if _, err := r.rs.RequestRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance.id, HoldID: hid, Reason: "IT: concurrent release"}); err != nil {
		t.Fatal(err)
	}
	var ok, failed int
	var mu sync.Mutex
	var wg2 sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			_, err := r.rs.DecideRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance2.id, StepUpAt: time.Now(), HoldID: hid, Reason: "IT: approve"}, true)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else {
				failed++
			}
		}()
	}
	wg2.Wait()
	if ok != 1 || failed != 4 {
		t.Fatalf("concurrent release: ok=%d failed=%d", ok, failed)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_events WHERE aggregate_id = $1 AND event_type = 'HOLD_RELEASED'`, hid); n != 1 {
		t.Fatalf("%d release events", n)
	}
}

// TestRiskReconciliationAndReports: reconciliation finds an orphaned hold; overview and quality never claim accuracy;
// the state machines in Go equal the database's.
func TestRiskReconciliationAndReports(t *testing.T) {
	r := newRiskIT(t)
	for machine, edges := range map[string]map[string][]string{"risk_case": risk.CaseEdges, "risk_hold": risk.HoldEdges,
		"risk_rule_version": risk.RuleVersionEdges, "risk_screening": risk.ScreeningEdges} {
		want := map[string]bool{}
		for from, tos := range edges {
			for _, to := range tos {
				want[from+">"+to] = true
			}
		}
		rows, err := r.mig.Query(ctx(t), `SELECT from_status, to_status FROM app.status_transitions WHERE machine = $1`, machine)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for rows.Next() {
			var f, to string
			_ = rows.Scan(&f, &to)
			got[f+">"+to] = true
		}
		rows.Close()
		if fmt.Sprint(len(got)) != fmt.Sprint(len(want)) {
			t.Fatalf("%s: database %d edges, Go %d", machine, len(got), len(want))
		}
		for e := range want {
			if !got[e] {
				t.Fatalf("%s: edge %s missing in the database", machine, e)
			}
		}
	}
	camp := r.campaign(t, 0)
	cid := r.act(t, r.f.compliance, "POST", "/admin/risk/cases", map[string]any{"case_type": "CAMPAIGN_RISK", "subject_type": "CAMPAIGN", "subject_id": camp,
		"priority": "P4", "reason": "IT: reconciliation"}, 201, "").field(t, "id")
	// resolve the case as a false positive (maker-checker), then place a hold that names it: an open hold under a case
	// resolved without action is what reconciliation must flag
	base := "/admin/risk/cases/" + cid
	r.act(t, r.f.compliance, "POST", base+"/assign", map[string]any{}, 200, "")
	r.act(t, r.f.compliance, "POST", base+"/propose-decision", map[string]any{"outcome": "FALSE_POSITIVE", "reason": "IT: nothing found"}, 200, "")
	r.act(t, r.f.compliance2, "POST", base+"/approve-decision", map[string]any{"reason": "IT: agreed"}, 200, "")
	hid := r.act(t, r.f.compliance, "POST", "/admin/risk/holds", map[string]any{"hold_type": "WITHDRAWAL_HOLD", "subject_type": "CAMPAIGN", "subject_id": camp,
		"reason_code": "IT_RECON", "description": "IT: orphan", "case_id": cid}, 201, "").field(t, "id")
	rep := r.act(t, r.f.compliance, "POST", "/admin/risk/reconciliation/run", nil, 200, "")
	var rr risk.ReconReport
	_ = json.Unmarshal(rep.Data, &rr)
	found := false
	for _, c := range rr.Checks {
		if c.Code == "ORPHANED_HOLD" && c.Status == "FINDINGS" {
			found = true
		}
		if c.Code == "EVIDENCE_HASH" && c.Status != "PASS" {
			t.Fatalf("evidence hash findings: %+v", c)
		}
	}
	if !found {
		t.Fatalf("orphaned hold %s not found: %+v", hid, rr.Checks)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_holds WHERE id = $1 AND status = 'ACTIVE'`, hid); n != 1 {
		t.Fatal("reconciliation changed the hold")
	}
	// clean up the inconsistency through the normal maker-checker release
	_, _ = r.rs.RequestRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance.id, HoldID: hid, Reason: "IT cleanup release"})
	_, _ = r.rs.DecideRelease(ctx(t), risk.HoldOp{StaffID: r.f.compliance2.id, StepUpAt: time.Now(), HoldID: hid, Reason: "IT cleanup release"}, true)
	// unauthorized access and score manipulation: support staff cannot open a case; evaluations are immutable
	if resp := r.act(t, r.f.support, "GET", "/admin/risk/cases/"+cid, nil, 0, ""); resp.Status != 403 && resp.Status != 404 {
		t.Fatalf("support opened a risk case: %d", resp.Status)
	}
	if _, err := r.mig.Exec(ctx(t), `UPDATE risk.risk_evaluations SET score = 0 WHERE id = (SELECT id FROM risk.risk_evaluations LIMIT 1)`); err == nil {
		t.Fatal("an evaluation score was changed")
	}
	ov := r.act(t, r.f.finance1, "GET", "/admin/risk/overview", nil, 200, "")
	if !strings.Contains(string(ov.Data), `"accuracy_metrics":"NOT_MEASURED"`) || !strings.Contains(string(ov.Data), `"screening_production":"BLOCKED"`) {
		t.Fatalf("overview: %s", ov.Data)
	}
	q := r.act(t, r.f.finance1, "GET", "/admin/risk/quality?days=7", nil, 200, "")
	if !strings.Contains(string(q.Data), `"accuracy":"NOT_MEASURED"`) {
		t.Fatalf("quality: %s", q.Data)
	}
}

// TestRiskSignalSources: domain events carry what the risk consumer needs (ids only): a sign-in carries a keyed,
// per-user user-agent hash and never the raw user agent or the IP address.
func TestRiskSignalSources(t *testing.T) {
	r := newRiskIT(t)
	u := basicUser(t, r.s, "rsig")
	var payload string
	if err := r.mig.QueryRow(ctx(t), `SELECT payload::text FROM app.outbox_events WHERE event_type = 'identity.signed_in' AND aggregate_id = $1
		ORDER BY created_at DESC LIMIT 1`, u.id).Scan(&payload); err != nil {
		t.Fatalf("no identity.signed_in event: %v", err)
	}
	if !strings.Contains(payload, `"ua_hash"`) || strings.Contains(payload, "Go-http-client") || strings.Contains(payload, "127.0.0.1") {
		t.Fatalf("signed_in payload: %s", payload)
	}
	var d outbox.Delivery
	var raw []byte
	if err := r.mig.QueryRow(ctx(t), `SELECT id::text, event_type, payload, occurred_at FROM app.outbox_events WHERE event_type = 'identity.signed_in'
		AND aggregate_id = $1 ORDER BY created_at DESC LIMIT 1`, u.id).Scan(&d.EventID, &d.EventType, &raw, &d.OccurredAt); err != nil {
		t.Fatal(err)
	}
	d.Payload = raw
	sig, ok, err := risk.SignalFromEvent(d)
	if err != nil || !ok || sig.Type != risk.SigSignedIn || sig.SubjectID != u.id {
		t.Fatalf("signal from sign-in: %+v %v %v", sig, ok, err)
	}
}

// TestRiskRuleFaultIsolation (failure injection, KI-S13-11): an ACTIVE rule the engine cannot run (as after a corrupted
// row or an engine mismatch — created here with SQL, bypassing the API's validation) no longer blocks signal recording.
// The signal is stored, the independent rule still runs, the failure is recorded and monitored, the evaluation and the
// subject's profile are marked degraded (payouts treat that as REVIEW), and reconciliation reports the rule. Retiring it
// restores complete evaluations. The rules target AUTHENTICATION_SESSION, which no other test or event uses.
func TestRiskRuleFaultIsolation(t *testing.T) {
	r := newRiskIT(t)
	ua := strings.Repeat("ab", 16)
	_, good := r.rule(t, "AUTHENTICATION_SESSION", "SESSION_RISK", map[string]any{"when": map[string]any{"fact_compare": map[string]any{
		"signal_types": []string{"SIGNED_IN"}, "fact": "ua_hash", "window_seconds": 3600, "op": "=", "value": ua}}}, 40, "REVIEW_REQUIRED", nil, "P3")
	ruleID, vid := ids.New(), ids.New()
	code := itCode("IT_BROKEN")
	migratorExec(t, `INSERT INTO risk.risk_rules (id, code, name, subject_type, category, created_by) VALUES ($1, $2, 'IT broken rule',
		'AUTHENTICATION_SESSION', 'SESSION_RISK', $3)`, ruleID, code, r.f.compliance.id)
	migratorExec(t, `INSERT INTO risk.risk_rule_versions (id, rule_id, version_no, status, definition, contribution, severity, recommended_action,
		case_priority, engine_version, created_by) VALUES ($1, $2, 1, 'DRAFT', '{"when":{"not_a_condition":true}}', 10, 'LOW', 'REVIEW_REQUIRED', 'P4',
		'risk-engine-v2', $3)`, vid, ruleID, r.f.compliance.id)
	migratorExec(t, `UPDATE risk.risk_rule_versions SET status = 'PENDING_APPROVAL', submitted_by = created_by, submitted_at = now(),
		definition_hash = sha256(definition::text::bytea) WHERE id = $1`, vid)
	migratorExec(t, `UPDATE risk.risk_rule_versions SET status = 'APPROVED', approved_by = $2, approved_at = now(), approved_step_up_at = now(),
		approval_reason = 'failure injection' WHERE id = $1`, vid, r.f.compliance2.id)
	migratorExec(t, `UPDATE risk.risk_rule_versions SET status = 'ACTIVE', activated_by = created_by, activated_at = now() WHERE id = $1`, vid)
	retired := false
	retire := func() {
		if !retired {
			migratorExec(t, `UPDATE risk.risk_rule_versions SET status = 'RETIRED', retired_by = created_by, retired_at = now(),
				retirement_reason = 'IT cleanup: failure injection' WHERE id = $1`, vid)
			retired = true
		}
	}
	t.Cleanup(retire)
	subject := ids.New()
	signal := func() (risk.Outcome, string) {
		src := ids.New()
		out, err := r.rs.RecordSignal(ctx(t), risk.Signal{Type: risk.SigSignedIn, SubjectType: "AUTHENTICATION_SESSION", SubjectID: subject,
			SourceModule: "identity", SourceEventID: src, SourceEventType: "identity.signed_in", ObservedAt: time.Now().UTC(), Facts: map[string]any{"ua_hash": ua}})
		if err != nil {
			t.Fatalf("signal recording blocked by a broken rule: %v", err)
		}
		return out, src
	}
	out, src := signal()
	if r.count(t, `SELECT count(*) FROM risk.risk_signals WHERE source_event_id = $1`, src) != 1 {
		t.Fatal("trusted event not persisted")
	}
	if !out.Degraded || out.EvaluationID == "" || out.CaseID == "" || out.Score2 != 40 {
		t.Fatalf("independent rule not evaluated or failure not flagged: %+v", out)
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_evaluations WHERE id = $1 AND degraded AND $2::uuid = ANY (failed_rule_version_ids)
		AND $3::uuid = ANY (rule_version_ids)`, out.EvaluationID, vid, good); n != 1 {
		t.Fatal("evaluation does not record the failed and the evaluated rule versions")
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_rule_failures WHERE rule_version_id = $1 AND error_code = 'UNPARSEABLE_DEFINITION'
		AND evaluation_id = $2`, vid, out.EvaluationID); n != 1 {
		t.Fatal("rule failure not recorded")
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_monitoring_events WHERE event_type = 'RULE_EVALUATION_FAILED' AND detail->>'rule_version_id' = $1`, vid); n != 1 {
		t.Fatal("rule failure not surfaced as a monitoring event")
	}
	if n := r.count(t, `SELECT count(*) FROM risk.risk_rule_versions WHERE id = $1 AND status = 'ACTIVE'`, vid); n != 1 {
		t.Fatal("the affected rule version was not preserved")
	}
	if degraded, err := r.rs.DegradedEvaluation(ctx(t), risk.HoldSubject{Type: "AUTHENTICATION_SESSION", ID: subject}); err != nil || !degraded {
		t.Fatalf("degraded evaluation not reported to financial consumers: %v %v", degraded, err)
	}
	fs, err := r.rs.RuleFailures(ctx(t), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fs {
		found = found || (f.RuleVersionID == vid && f.StillActive)
	}
	if !found {
		t.Fatalf("rule failure missing from the operations list: %+v", fs)
	}
	rep, err := r.rs.Reconcile(ctx(t), "failure-injection")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, c := range rep.Checks {
		found = found || (c.Code == "RULE_DEFINITIONS" && c.Status == "FINDINGS")
	}
	if !found {
		t.Fatalf("unparseable rule not reported: %+v", rep.Checks)
	}
	retire()
	if out2, _ := signal(); out2.Degraded {
		t.Fatalf("evaluation still degraded after the broken rule was retired: %+v", out2)
	}
	if degraded, _ := r.rs.DegradedEvaluation(ctx(t), risk.HoldSubject{Type: "AUTHENTICATION_SESSION", ID: subject}); degraded {
		t.Fatal("profile still degraded after a complete evaluation")
	}
}
