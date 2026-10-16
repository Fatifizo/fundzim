//go:build integration

package integration

// Stage 14 operations portal (ADR-046) against the real stack: the command center reads every module's snapshot, the
// approval center routes decisions to the owning module, and the operations records (support cases, incidents,
// exception triage, access reviews, SLA targets) keep their lifecycles in the database. Shared dev database: tests use
// their own records and leave fixture roles unchanged.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/risk"
)

var (
	opsOnce     sync.Once
	opsSecAdmin staffCred
	opsErr      string
)

type opsIT struct {
	*riskIT
	sec staffCred // SECURITY_ADMIN (access reviews, incidents, session revocation)
}

func newOpsIT(t *testing.T) *opsIT {
	t.Helper()
	r := newRiskIT(t)
	opsOnce.Do(func() {
		opsErr = "operations fixture failed in an earlier test"
		a, b := r.s.staff(t, r.f.adminA), r.s.staff(t, r.f.adminB)
		opsSecAdmin = inviteStaff(t, r.s, a, b, "Security Admin", "SECURITY_ADMIN")
		opsErr = ""
	})
	if opsErr != "" {
		t.Fatal(opsErr)
	}
	return &opsIT{riskIT: r, sec: opsSecAdmin}
}

func (o *opsIT) data(t *testing.T, r apiResp, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Data, v); err != nil {
		t.Fatalf("decode %s: %v", r.Data, err)
	}
}

// TestOperationsOverviewSourcesAndPermissions: every module's snapshot can be read (its SQL runs against the real
// schema); each viewer sees only metrics they hold the permission for; every metric names its source; disabled
// capabilities are BLOCKED; non-staff get 404; /me carries effective permissions for staff only.
func TestOperationsOverviewSourcesAndPermissions(t *testing.T) {
	o := newOpsIT(t)
	type overview struct {
		Metrics []opsview.Metric          `json:"metrics"`
		Sources []operations.SourceStatus `json:"sources"`
		Alerts  []operations.Alert        `json:"alerts"`
		Notice  string                    `json:"notice"`
	}
	get := func(c staffCred) overview {
		var ov overview
		o.data(t, o.act(t, c, "GET", "/admin/operations/overview", nil, 200, ""), &ov)
		return ov
	}
	comp := get(o.f.compliance)
	modules := map[string]bool{}
	for _, s := range comp.Sources {
		if s.Status != opsview.Healthy {
			t.Fatalf("source %s unavailable: %s", s.Module, s.Error)
		}
		modules[s.Module] = true
	}
	for _, m := range []string{"identity", "campaigns", "kyc", "beneficiaries", "compliance", "risk", "payments", "providers", "ledger", "payouts", "fees"} {
		if !modules[m] {
			t.Fatalf("module %s not wired: %+v", m, comp.Sources)
		}
	}
	keys := map[string]opsview.Metric{}
	for _, m := range comp.Metrics {
		if m.Source == "" || m.Nature == "" || m.Permission == "" {
			t.Fatalf("metric without source/nature/permission: %+v", m)
		}
		keys[m.Key] = m
	}
	for _, k := range []string{"risk.cases.open", "compliance.cases.open", "campaigns.reviews.pending", "kyc.reviews.pending", "payments.unknown"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("compliance is missing %s", k)
		}
	}
	if m := keys["payments.live_execution"]; m.Status != opsview.StatusBlocked || m.Nature != opsview.Blocked {
		t.Fatalf("live payments not BLOCKED: %+v", m)
	}
	if m := keys["risk.screening.production"]; m.Status != opsview.StatusBlocked {
		t.Fatalf("production screening not BLOCKED: %+v", m)
	}
	if m := keys["payments.unknown"]; m.Nature != opsview.TestOnly {
		t.Fatalf("TEST payment figure not labelled: %+v", m)
	}
	if !strings.Contains(comp.Notice, "never business performance") {
		t.Fatalf("notice: %s", comp.Notice)
	}
	// SUPPORT sees no ledger, payout, risk or KYC figures
	sup := get(o.f.support)
	for _, m := range sup.Metrics {
		for _, p := range []string{"ledger.", "payouts.", "risk.", "kyc.", "kyb.", "compliance."} {
			if strings.HasPrefix(m.Key, p) {
				t.Fatalf("support sees %s", m.Key)
			}
		}
	}
	// FINANCE sees payouts and the ledger
	fin := get(o.f.finance1)
	fk := map[string]bool{}
	for _, m := range fin.Metrics {
		fk[m.Key] = true
	}
	if !fk["payouts.awaiting_approval"] || !fk["ledger.integrity"] || fk["kyc.reviews.pending"] {
		t.Fatalf("finance metrics: %+v", fk)
	}
	// non-staff: 404 (no discovery)
	if r := o.owner.do("GET", "/admin/operations/overview", nil); r.Status != 404 {
		t.Fatalf("owner reached the command center: %d", r.Status)
	}
	// /me: staff carry permissions, users do not
	var me struct {
		Permissions []string `json:"permissions"`
	}
	o.data(t, o.s.staff(t, o.f.compliance).do("GET", "/me", nil), &me)
	if !contains(me.Permissions, "risk.case.work") || contains(me.Permissions, "ledger.adjustment.approve") {
		t.Fatalf("compliance permissions: %v", me.Permissions)
	}
	var um struct {
		Permissions []string `json:"permissions"`
	}
	o.data(t, o.owner.do("GET", "/me", nil), &um)
	if len(um.Permissions) != 0 {
		t.Fatalf("a user got permissions: %v", um.Permissions)
	}
	// health measured now; workers and outbox are reported from their tables
	var h operations.HealthReport
	o.data(t, o.act(t, o.f.compliance, "GET", "/admin/operations/health", nil, 200, ""), &h)
	names := map[string]string{}
	for _, c := range h.Components {
		names[c.Name] = c.Status
	}
	if names["database"] != opsview.Healthy || names["outbox"] == "" || names["workers"] == "" || names["application_errors"] != opsview.StatusUnknown {
		t.Fatalf("health: %+v", names)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// TestOperationsApprovalCenterRoutesToDomain: a pending risk rule version appears for the checker (decidable) and its
// maker (blocked); the decision goes through the risk module's own route; afterwards it leaves the queue. Concurrent
// approvals of one version succeed once (domain service).
func TestOperationsApprovalCenterRoutesToDomain(t *testing.T) {
	o := newOpsIT(t)
	c1, c2 := o.f.compliance, o.f.compliance2
	ver := map[string]any{"definition": reportsRule(itCode("ITO"), 99), "contribution": 1, "severity": "LOW", "recommended_action": "ALLOW_WITH_MONITORING",
		"case_priority": "P4"}
	resp := o.act(t, c1, "POST", "/admin/risk/rules", map[string]any{"code": itCode("IT_APPR"), "name": "IT approval center rule", "subject_type": "CAMPAIGN",
		"category": "CAMPAIGN_RISK", "version": ver}, 201, "")
	var rr risk.RiskRule
	o.data(t, resp, &rr)
	vid := rr.Versions[0].ID
	t.Cleanup(func() {
		_, _ = o.rs.RetireRuleVersion(ctx(t), risk.RuleOp{StaffID: c1.id, StepUpAt: time.Now(), VersionID: vid, Reason: "IT cleanup: retire"})
	})
	o.act(t, c1, "POST", "/admin/risk/rule-versions/"+vid+"/submit", nil, 200, "")
	find := func(c staffCred) *operations.ApprovalView {
		var page struct {
			Items []operations.ApprovalView `json:"items"`
		}
		o.data(t, o.act(t, c, "GET", "/admin/approvals?module=risk", nil, 200, ""), &page)
		for i := range page.Items {
			if page.Items[i].SourceRecordID == vid {
				return &page.Items[i]
			}
		}
		return nil
	}
	if a := find(c2); a == nil || !a.CanDecide || a.RequiredPermission != "risk.rule.approve" || !strings.Contains(a.Href, rr.ID) {
		t.Fatalf("checker view: %+v", a)
	}
	if a := find(c1); a == nil || a.CanDecide || a.Blocked != "MAKER" {
		t.Fatalf("maker view: %+v", a)
	}
	if a := find(o.f.support); a != nil {
		t.Fatalf("support sees a risk approval: %+v", a)
	}
	// concurrent decisions through the owning module: exactly one wins
	var mu sync.Mutex
	okN := 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := o.rs.DecideRuleVersion(ctx(t), risk.RuleOp{StaffID: c2.id, StepUpAt: time.Now(), VersionID: vid, Reason: "IT concurrent approval"}, true)
			if err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okN != 1 {
		t.Fatalf("%d concurrent approvals succeeded", okN)
	}
	if a := find(c2); a != nil {
		t.Fatalf("decided approval still queued: %+v", a)
	}
}

// TestOperationsSupportCases: lifecycle, assignment eligibility, escalation keeps ownership, history, concurrency, and
// support staff do not gain access to risk or KYC detail.
func TestOperationsSupportCases(t *testing.T) {
	o := newOpsIT(t)
	sup := o.f.support
	body := map[string]any{"subject": "Donor asks about a TEST donation", "category": "DONATION", "priority": "P3", "requester_user_id": o.owner.id,
		"related_reference_type": "DONATION_INTENT", "related_reference_id": ids.New(), "note": "IT: first contact"}
	var c operations.SupportCase
	o.data(t, o.act(t, sup, "POST", "/admin/support/cases", body, 201, ""), &c)
	if c.Status != "OPEN" || c.DueAt != nil && c.DueAt.Before(c.CreatedAt) {
		t.Fatalf("opened: %+v", c)
	}
	base := "/admin/support/cases/" + c.ID
	// compliance may view but not work support cases; an assignee must hold ops.support.work
	o.act(t, o.f.compliance, "GET", base, nil, 200, "")
	if r := o.act(t, o.f.compliance, "POST", base+"/assign", map[string]any{}, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("compliance assigned a support case: %d", r.Status)
	}
	o.act(t, sup, "POST", base+"/assign", map[string]any{"assignee_id": o.f.compliance.id}, 422, "VALIDATION_FAILED")
	o.data(t, o.act(t, sup, "POST", base+"/assign", map[string]any{}, 200, ""), &c)
	if c.Status != "TRIAGE" || c.AssignedTo == nil || *c.AssignedTo != sup.id {
		t.Fatalf("assigned: %+v", c)
	}
	o.act(t, sup, "POST", base+"/transition", map[string]any{"to": "IN_PROGRESS"}, 200, "")
	o.act(t, sup, "POST", base+"/transition", map[string]any{"to": "CLOSED", "note": "IT: not resolved yet"}, 409, "OPS_STATE")
	o.data(t, o.act(t, sup, "POST", base+"/escalate", map[string]any{"escalated_to": "PAYMENTS", "escalation_reference": "payments team ticket",
		"note": "IT: needs the payments team"}, 200, ""), &c)
	if c.Status != "ESCALATED" || c.AssignedTo == nil || *c.AssignedTo != sup.id || c.EscalatedTo == nil || *c.EscalatedTo != "PAYMENTS" {
		t.Fatalf("escalated (ownership must stay): %+v", c)
	}
	o.act(t, sup, "POST", base+"/notes", map[string]any{"note": "IT: payments confirmed TEST data"}, 200, "")
	o.act(t, sup, "POST", base+"/transition", map[string]any{"to": "RESOLVED"}, 422, "VALIDATION_FAILED")
	o.act(t, sup, "POST", base+"/transition", map[string]any{"to": "RESOLVED", "note": "IT: explained TEST donations"}, 200, "")
	o.act(t, sup, "POST", base+"/transition", map[string]any{"to": "REOPENED", "note": "IT: donor wrote again"}, 200, "")
	var det struct {
		Case    operations.SupportCase    `json:"case"`
		History []operations.HistoryEntry `json:"history"`
	}
	o.data(t, o.act(t, sup, "GET", base, nil, 200, ""), &det)
	if det.Case.Resolution != nil || len(det.History) < 7 {
		t.Fatalf("history after reopen: %+v", det)
	}
	// concurrent updates with the same version: one wins, the rest are stale
	var mu sync.Mutex
	stale, ok := 0, 0
	var wg sync.WaitGroup
	ops := opsService(t, o)
	cur, _ := ops.SupportCase(ctx(t), c.ID)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ops.AddSupportNote(ctx(t), operations.SupportOp{StaffID: sup.id, CaseID: c.ID, ExpectedVersion: cur.Version, Note: "IT concurrent note"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else {
				stale++
			}
		}()
	}
	wg.Wait()
	if ok != 1 || stale != 4 {
		t.Fatalf("concurrent updates: ok=%d stale=%d", ok, stale)
	}
	// support staff get no risk or KYC access through the portal
	for _, path := range []string{"/admin/risk/cases", "/admin/verification/cases", "/admin/finance/ledger/journals"} {
		if r := o.act(t, sup, "GET", path, nil, 0, ""); r.Status != 403 && r.Status != 404 {
			t.Fatalf("support reached %s: %d", path, r.Status)
		}
	}
	// the database refuses rewriting history
	if _, err := o.mig.Exec(ctx(t), `UPDATE app.ops_support_events SET note = 'tampered' WHERE case_id = $1`, c.ID); err == nil {
		t.Fatal("support history edited")
	}
}

// opsService builds the operations service the server uses (for direct, concurrent calls).
func opsService(t *testing.T, o *opsIT) *operations.Service {
	t.Helper()
	return &operations.Service{Pool: o.s.d.DB, Clock: clock.System, Perms: o.s.d.Auth, Identities: o.s.d.Verification.KYC, Revoker: o.s.d.Auth,
		StaffRoles: o.s.d.Auth}
}

// TestOperationsExceptionTriage: a domain exception (a TEST screening that ended in ERROR) appears in the exception
// center; triage assigns and tracks it; triage cannot finish while the module still reports it open; an unknown
// exception cannot enter triage.
func TestOperationsExceptionTriage(t *testing.T) {
	o := newOpsIT(t)
	u := basicUser(t, o.s, "opsexc")
	scr := o.act(t, o.f.compliance, "POST", "/admin/risk/screening", map[string]any{"subject_type": "USER", "subject_id": u.id, "scenario": "PROVIDER_ERROR"}, 201, "")
	sid := scr.field(t, "id")
	if _, err := o.mig.Exec(ctx(t), `UPDATE risk.sandbox_screenings SET ready_at = now() - interval '1 second' WHERE request_id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := o.s.d.Verification.RiskSandbox.DeliverDue(ctx(t), 500); err != nil {
		t.Fatal(err)
	}
	ref := "risk:screening_error:" + sid
	var page struct {
		Items []operations.ExceptionView `json:"items"`
	}
	o.data(t, o.act(t, o.f.finance1, "GET", "/admin/operations/exceptions?module=risk", nil, 200, ""), &page)
	var found *operations.ExceptionView
	for i := range page.Items {
		if page.Items[i].ID == ref {
			found = &page.Items[i]
		}
	}
	if found == nil || found.TriageStatus != "UNTRIAGED" || !found.DomainOpen {
		t.Fatalf("screening error not in the exception center: %+v", found)
	}
	path := "/admin/operations/exceptions/risk/" + urlEscape(ref) + "/triage"
	o.act(t, o.f.finance1, "POST", path, map[string]any{"assignee_id": o.f.support.id}, 422, "VALIDATION_FAILED") // support lacks ops.exception.work
	var x operations.ExceptionView
	o.data(t, o.act(t, o.f.finance1, "POST", path, map[string]any{"assignee_id": o.f.finance1.id, "note": "IT: looking"}, 200, ""), &x)
	if x.TriageStatus != "ACKNOWLEDGED" || x.AssignedTo == nil || *x.AssignedTo != o.f.finance1.id {
		t.Fatalf("triage: %+v", x)
	}
	o.act(t, o.f.finance1, "POST", path, map[string]any{"to": "INVESTIGATING"}, 200, "")
	o.act(t, o.f.finance1, "POST", path, map[string]any{"to": "RESOLVED", "note": "IT: done here"}, 409, "OPS_EXCEPTION_DOMAIN_OPEN")
	o.act(t, o.f.finance1, "POST", path, map[string]any{"to": "WAITING_EXTERNAL", "note": "IT: waiting for the screening owner"}, 200, "")
	var hist struct {
		Items []operations.HistoryEntry `json:"items"`
	}
	o.data(t, o.act(t, o.f.finance1, "GET", "/admin/operations/exceptions/risk/"+urlEscape(ref)+"/history", nil, 200, ""), &hist)
	if len(hist.Items) < 4 {
		t.Fatalf("triage history: %+v", hist.Items)
	}
	// triage records keep the module's visibility: SUPPORT (no exception work, no risk view) cannot read the history
	if r := o.s.staff(t, o.f.support).do("GET", "/admin/operations/exceptions/risk/"+urlEscape(ref)+"/history", nil); r.Status != 404 {
		t.Fatalf("support read triage history: %d", r.Status)
	}
	if n := o.count(t, `SELECT count(*) FROM app.ops_exception_assignments WHERE source_ref = $1 AND view_permission = 'risk.operations.view'`, ref); n != 1 {
		t.Fatalf("triage record without the module's permission: %d", n)
	}
	o.act(t, o.f.finance1, "POST", "/admin/operations/exceptions/risk/"+urlEscape("risk:screening_error:"+ids.New())+"/triage", map[string]any{"to": "ACKNOWLEDGED"},
		404, "OPS_EXCEPTION_NOT_OPEN")
}

// TestOperationsIncidentsAndAlerts: incident lifecycle with an owner and timeline; an alert key yields one open incident;
// support can view but not manage.
func TestOperationsIncidentsAndAlerts(t *testing.T) {
	o := newOpsIT(t)
	key := "IT_ALERT_" + strings.ToUpper(strings.ReplaceAll(ids.New()[24:], "-", ""))
	in := map[string]any{"title": "IT outbox backlog drill", "severity": "SEV3", "affected_module": "outbox", "alert_key": key, "note": "IT drill"}
	a := o.act(t, o.sec, "POST", "/admin/operations/incidents", in, 201, "").field(t, "id")
	b := o.act(t, o.sec, "POST", "/admin/operations/incidents", in, 201, "").field(t, "id")
	if a != b {
		t.Fatalf("one alert produced two open incidents: %s %s", a, b)
	}
	base := "/admin/operations/incidents/" + a
	if r := o.act(t, o.f.support, "POST", base+"/update", map[string]any{"to": "ACKNOWLEDGED"}, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("support managed an incident: %d", r.Status)
	}
	o.act(t, o.f.support, "GET", base, nil, 200, "")
	o.act(t, o.sec, "POST", base+"/update", map[string]any{"to": "RESOLVED", "note": "too early"}, 409, "OPS_STATE")
	for _, to := range []string{"ACKNOWLEDGED", "INVESTIGATING", "MITIGATING", "MONITORING"} {
		o.act(t, o.sec, "POST", base+"/update", map[string]any{"to": to, "note": "IT step " + to}, 200, "")
	}
	o.act(t, o.sec, "POST", base+"/update", map[string]any{"to": "RESOLVED", "note": "IT: backlog drained"}, 200, "")
	o.act(t, o.sec, "POST", base+"/update", map[string]any{"to": "CLOSED"}, 200, "")
	o.act(t, o.sec, "POST", base+"/update", map[string]any{"note": "IT post-incident: add a relay alarm"}, 200, "")
	var det struct {
		Incident operations.Incident       `json:"incident"`
		Timeline []operations.HistoryEntry `json:"timeline"`
	}
	o.data(t, o.act(t, o.sec, "GET", base, nil, 200, ""), &det)
	if det.Incident.Status != "CLOSED" || det.Incident.PostIncidentNotes == nil || len(det.Timeline) < 8 {
		t.Fatalf("incident: %+v", det)
	}
	// once closed, the same alert opens a new incident
	if c := o.act(t, o.sec, "POST", "/admin/operations/incidents", in, 201, "").field(t, "id"); c == a {
		t.Fatal("closed incident reused for a new alert occurrence")
	}
	o.act(t, o.f.compliance, "GET", "/admin/operations/alerts", nil, 200, "")
}

// TestOperationsStaffGovernance: directory and detail (no email or phone), role inventory, session revocation ends an
// active session immediately, access reviews keep separation of duties and file dual-controlled revocations.
func TestOperationsStaffGovernance(t *testing.T) {
	o := newOpsIT(t)
	adminA, adminB := o.f.adminA, o.f.adminB
	dir := o.act(t, adminA, "GET", "/admin/staff?q=Support", nil, 200, "")
	if strings.Contains(string(dir.Data), "@") {
		t.Fatalf("directory exposes email addresses: %s", dir.Data)
	}
	var detail struct {
		Member struct {
			Roles []string `json:"roles"`
		} `json:"member"`
		Permissions []string `json:"permissions"`
	}
	o.data(t, o.act(t, adminA, "GET", "/admin/staff/"+o.f.support.id, nil, 200, ""), &detail)
	if !contains(detail.Member.Roles, "SUPPORT") || !contains(detail.Permissions, "ops.support.work") {
		t.Fatalf("staff detail: %+v", detail)
	}
	o.act(t, adminA, "GET", "/admin/staff/roles", nil, 200, "")
	if r := o.act(t, o.f.support, "GET", "/admin/staff", nil, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("support listed staff: %d", r.Status)
	}
	// session revocation during an active session (a throwaway staff member)
	tmp := inviteStaff(t, o.s, o.s.staff(t, adminA), o.s.staff(t, adminB), "Temp Revoked", "")
	tb := o.s.browser(t)
	tb.staffLogin(tmp.email, tmp.secret)
	tb.expect(tb.do("GET", "/me", nil), 200, "")
	o.act(t, o.sec, "POST", "/admin/staff/"+o.sec.id+"/sessions/revoke", map[string]any{"reason": "IT: my own sessions"}, 403, "SELF_ACTION_FORBIDDEN")
	o.act(t, o.sec, "POST", "/admin/staff/"+tmp.id+"/sessions/revoke", map[string]any{"reason": "IT: suspected compromise drill"}, 200, "")
	if r := tb.do("GET", "/me", nil); r.Status != 401 {
		t.Fatalf("revoked session still works: %d", r.Status)
	}
	// access review: SECURITY_ADMIN starts it, a SUPER_ADMIN reviews; nobody certifies their own access
	o.act(t, o.sec, "POST", "/admin/access-reviews", map[string]any{"title": "IT self review", "reviewer_id": o.sec.id}, 403, "SELF_APPROVAL_FORBIDDEN")
	var rv operations.AccessReview
	o.data(t, o.act(t, o.sec, "POST", "/admin/access-reviews", map[string]any{"title": "IT super admin review", "scope_role": "SUPER_ADMIN",
		"reviewer_id": adminA.id}, 201, ""), &rv)
	var own, other *operations.ReviewItem
	for i := range rv.Items {
		switch rv.Items[i].StaffUserID {
		case adminA.id:
			own = &rv.Items[i]
		case adminB.id:
			other = &rv.Items[i]
		}
	}
	if own == nil || other == nil {
		t.Fatalf("review items: %+v", rv.Items)
	}
	rb := "/admin/access-reviews/" + rv.ID
	o.act(t, adminA, "POST", rb+"/items/"+own.ID+"/decide", map[string]any{"decision": "CERTIFIED", "reason": "IT: my own access"}, 403, "OPS_CONFLICT_OF_INTEREST")
	o.act(t, adminB, "POST", rb+"/items/"+other.ID+"/decide", map[string]any{"decision": "CERTIFIED", "reason": "IT: not the reviewer"}, 403, "OPS_NOT_REVIEWER")
	o.act(t, adminA, "POST", rb+"/items/"+other.ID+"/decide", map[string]any{"decision": "REVOKE_RECOMMENDED", "reason": "IT: drill recommendation"}, 200, "")
	o.data(t, o.act(t, adminA, "GET", rb, nil, 200, ""), &rv)
	var rec *operations.ReviewItem
	for i := range rv.Items {
		if rv.Items[i].ID == other.ID {
			rec = &rv.Items[i]
		}
	}
	if rec == nil || rec.Decision != "REVOKE_RECOMMENDED" || rec.RevokeRequestID == nil {
		t.Fatalf("revocation not filed: %+v", rec)
	}
	// the revocation is an ordinary dual-controlled role request: reject it to keep the fixture
	o.act(t, adminB, "POST", "/admin/role-assignment-requests/"+*rec.RevokeRequestID+"/approve", map[string]any{"reason": "IT: own role"}, 0, "")
	o.act(t, o.f.adminA, "POST", "/admin/role-assignment-requests/"+*rec.RevokeRequestID+"/reject", map[string]any{"reason": "IT drill: keep the fixture role"}, 0, "")
	var still struct {
		Member struct {
			Roles []string `json:"roles"`
		} `json:"member"`
	}
	o.data(t, o.act(t, adminA, "GET", "/admin/staff/"+adminB.id, nil, 200, ""), &still)
	if !contains(still.Member.Roles, "SUPER_ADMIN") {
		t.Fatalf("fixture lost its role: %+v", still)
	}
	// decisions are final in the database
	if _, err := o.mig.Exec(ctx(t), `UPDATE app.ops_access_review_items SET decision = 'CERTIFIED' WHERE id = $1`, other.ID); err == nil {
		t.Fatal("decided review item changed")
	}
}

// TestOperationsAuditCenter: search (filters, cursor), immutable records, security stream permission, bounded export
// that is itself audited.
func TestOperationsAuditCenter(t *testing.T) {
	o := newOpsIT(t)
	comp := o.f.compliance
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	o.data(t, o.act(t, comp, "GET", "/admin/audit/events?action=risk.&limit=2", nil, 200, ""), &page)
	if len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("audit page: %+v", page)
	}
	for _, e := range page.Items {
		if !strings.HasPrefix(e["action"].(string), "risk.") || e["ip"] != nil {
			t.Fatalf("audit item: %+v", e)
		}
	}
	o.act(t, comp, "GET", "/admin/audit/events?action=risk.&limit=2&cursor="+*page.NextCursor, nil, 200, "")
	if r := o.act(t, o.f.support, "GET", "/admin/audit/events", nil, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("support read the audit log: %d", r.Status)
	}
	if r := o.act(t, comp, "GET", "/admin/audit/security-events", nil, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("compliance read the security stream: %d", r.Status)
	}
	o.act(t, o.f.adminA, "GET", "/admin/audit/security-events?limit=5", nil, 200, "")
	if _, err := o.mig.Exec(ctx(t), `UPDATE audit.audit_events SET action = 'tampered.action' WHERE seq = (SELECT max(seq) FROM audit.audit_events)`); err == nil {
		t.Fatal("audit record edited")
	}
	from, to := time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339), time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	o.act(t, comp, "POST", "/admin/audit/exports", map[string]any{"format": "csv", "from": time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339), "to": to},
		422, "VALIDATION_FAILED")
	b := o.s.staff(t, comp)
	r := b.raw("POST", "/admin/audit/exports", map[string]any{"format": "csv", "from": from, "to": to, "action": "risk.rule."})
	if r.status == 403 {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, comp.secret)}), 200, "")
		r = b.raw("POST", "/admin/audit/exports", map[string]any{"format": "csv", "from": from, "to": to, "action": "risk.rule."})
	}
	if r.status != 200 || !strings.HasPrefix(r.body, "id,seq,occurred_at") || !strings.Contains(r.contentType, "text/csv") {
		t.Fatalf("export: %d %s %.200s", r.status, r.contentType, r.body)
	}
	if n := o.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'audit.exported' AND actor_id = $1 AND occurred_at > now() - interval '5 minutes'`,
		comp.id); n < 1 {
		t.Fatal("export not audited")
	}
	if r := o.act(t, o.f.finance1, "POST", "/admin/audit/exports", map[string]any{"format": "json", "from": from, "to": to}, 0, ""); r.Status != 403 && r.Status != 404 {
		t.Fatalf("finance exported the audit log: %d", r.Status)
	}
}

// TestOperationsReportsSearchAndSLA: reports document their limits; search respects per-module permissions; internal
// SLA targets are maker-checker and drive support due dates only once approved.
func TestOperationsReportsSearchAndSLA(t *testing.T) {
	o := newOpsIT(t)
	var reps struct {
		Items []struct {
			Key         string   `json:"key"`
			Source      string   `json:"source"`
			Environment string   `json:"environment"`
			Limitations []string `json:"limitations"`
		} `json:"items"`
	}
	o.data(t, o.act(t, o.f.compliance, "GET", "/admin/reports", nil, 200, ""), &reps)
	seen := map[string]bool{}
	for _, r := range reps.Items {
		if r.Source == "" || r.Environment != "TEST" {
			t.Fatalf("report without source/environment: %+v", r)
		}
		seen[r.Key] = true
	}
	if !seen["risk_investigation_backlog"] || !seen["support_case_volumes"] {
		t.Fatalf("compliance reports: %v", seen)
	}
	// SUPPORT gets no risk, verification or financial reports
	o.data(t, o.act(t, o.f.support, "GET", "/admin/reports", nil, 200, ""), &reps)
	for _, r := range reps.Items {
		switch r.Key {
		case "risk_investigation_backlog", "verification_processing", "financial_reconciliation", "payout_exceptions", "staff_access_review":
			t.Fatalf("support sees report %s", r.Key)
		}
	}
	o.act(t, o.f.compliance, "GET", "/admin/reports?from=2026-10-10T00:00:00Z&to=2025-10-10T00:00:00Z", nil, 422, "VALIDATION_FAILED")
	// search: a risk case by number for compliance, not for support
	cid := o.act(t, o.f.compliance, "POST", "/admin/risk/cases", map[string]any{"case_type": "OTHER", "subject_type": "CAMPAIGN", "subject_id": ids.New(),
		"priority": "P4", "reason": "IT: search target"}, 201, "")
	var cs risk.Case
	o.data(t, cid, &cs)
	q := cs.ID
	var sr operations.SearchResult
	o.data(t, o.act(t, o.f.compliance, "GET", "/admin/operations/search?q="+q, nil, 200, ""), &sr)
	hit := false
	for _, h := range sr.Hits {
		hit = hit || (h.Kind == "RISK_CASE" && h.ID == cs.ID)
	}
	if !hit {
		t.Fatalf("risk case not found by number: %+v", sr.Hits)
	}
	o.data(t, o.act(t, o.f.support, "GET", "/admin/operations/search?q="+q, nil, 200, ""), &sr)
	for _, h := range sr.Hits {
		if h.Kind == "RISK_CASE" || h.Kind == "KYC_CASE" {
			t.Fatalf("support found %s", h.Kind)
		}
	}
	o.act(t, o.f.support, "GET", "/admin/operations/search?q=ab", nil, 422, "VALIDATION_FAILED")
	// SLA targets: proposer (ops.sla.request) ≠ approver (ops.sla.approve); the database refuses a self-decision
	ops := opsService(t, o)
	if _, err := o.mig.Exec(ctx(t), `UPDATE app.ops_sla_targets SET status = 'REJECTED', decided_by = $1, decided_at = now(), decision_reason = 'IT cleanup'
		WHERE status = 'PROPOSED' AND metric_key = 'SUPPORT_RESOLUTION'`, o.f.approver1.id); err != nil {
		t.Fatal(err)
	}
	var tg operations.SLATarget
	o.data(t, o.act(t, o.f.compliance, "POST", "/admin/operations/sla-targets", map[string]any{"metric_key": "SUPPORT_RESOLUTION", "target_seconds": 3 * 24 * 3600,
		"reason": "IT: internal target for support resolution"}, 201, ""), &tg)
	if _, err := o.mig.Exec(ctx(t), `UPDATE app.ops_sla_targets SET status = 'ACTIVE', decided_by = proposed_by, decided_at = now(), decision_reason = 'self'
		WHERE id = $1`, tg.ID); err == nil {
		t.Fatal("database accepted a self-approved SLA target")
	}
	o.act(t, o.f.approver1, "POST", "/admin/operations/sla-targets/"+tg.ID+"/approve", map[string]any{"reason": "IT: agreed internal target"}, 200, "")
	c, err := ops.OpenSupportCase(ctx(t), o.f.support.id, operations.NewSupportCase{Subject: "IT due date check", Category: "GENERAL"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DueAt == nil || c.DueAt.Sub(c.CreatedAt) < 71*time.Hour {
		t.Fatalf("due date from the approved target: %+v", c.DueAt)
	}
}

func urlEscape(s string) string { return url.PathEscape(s) }

type rawResp struct {
	status      int
	contentType string
	body        string
}

// raw sends a JSON request and returns the undecoded body (for file downloads).
func (b *browser) raw(method, path string, body any) rawResp {
	b.t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, b.s.srv.URL+"/api/v1"+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", b.ip)
	req.Header.Set("Idempotency-Key", ids.New())
	if c := b.cookie("fz_csrf"); c != "" {
		req.Header.Set("X-CSRF-Token", c)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return rawResp{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: string(data)}
}
