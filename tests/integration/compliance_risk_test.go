//go:build integration

// Compliance cases and the risk foundation (Stage 5, work stream C) against the REAL database with both
// pools: fundzim_app (risk) and fundzim_compliance (compliance). The HTTP handlers run behind an in-test
// router whose authorizer mirrors auth.Authorize's permission semantics (the production wiring is the
// lead's internal/app); the outbox end-to-end test runs River in-process with its own queue and an in-test
// registry (worker_outbox_test.go pattern), so it never depends on the worker container's consumers.
//
// Needs: DATABASE_URL, DATABASE_MIGRATION_URL, DATABASE_COMPLIANCE_URL, POSTGRES_WORKER_PASSWORD,
// COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY (from .env), migrations through 20261009150600.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/compliance"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// ---- fixtures ----------------------------------------------------------------------------------------------

type crEnv struct {
	t    *testing.T
	app  *pgxpool.Pool // fundzim_app
	cmp  *pgxpool.Pool // fundzim_compliance
	mig  *pgxpool.Pool // fundzim_migrator (fixtures only: staff/user rows)
	comp *compliance.Service
	risk *risk.Service
	rel  *stubRelations
}

// stubRelations stands in for the lead's users/organisations/auth adapter.
type stubRelations struct {
	mu       sync.Mutex
	personal map[string]string          // staff -> personal account
	members  map[string]map[string]bool // org -> personal accounts
	managers map[string]bool            // staff holding case.manage
}

func (r *stubRelations) PersonalAccount(_ context.Context, staff string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.personal[staff], nil
}

func (r *stubRelations) IsOrganisationMember(_ context.Context, org, user string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.members[org][user], nil
}

func (r *stubRelations) StaffHasPermission(_ context.Context, staff, perm string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return perm == compliance.PermManage && r.managers[staff], nil
}

func newCREnv(t *testing.T) *crEnv {
	t.Helper()
	need(t, "DATABASE_URL", "DATABASE_MIGRATION_URL", "DATABASE_COMPLIANCE_URL", "COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY")
	e := &crEnv{t: t, app: pool(t, os.Getenv("DATABASE_URL")), cmp: pool(t, os.Getenv("DATABASE_COMPLIANCE_URL")),
		mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL")),
		rel: &stubRelations{personal: map[string]string{}, members: map[string]map[string]bool{}, managers: map[string]bool{}}}
	var ok bool
	if err := e.mig.QueryRow(ctx(t), `SELECT to_regclass('compliance.compliance_cases') IS NOT NULL AND to_regclass('risk.risk_decisions') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		t.Fatalf("migrations 20261009150500/150600 are not applied (%v)", err)
	}
	aead, err := crypto.NewAEAD("local-compliance-1", os.Getenv("COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("FUNDZIM_IT_VERBOSE") == "1" {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	e.comp, err = compliance.New(compliance.Deps{Pool: e.cmp, Clock: clock.System, Logger: logger, NotesAEAD: aead, Relations: e.rel,
		StepUpMaxAge: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	e.risk = risk.New(e.app, clock.System, logger)
	return e
}

// staff creates a STAFF account (optionally linked to a personal account) directly as the migrator.
func (e *crEnv) staff(personal string) string {
	e.t.Helper()
	id := ids.New()
	var p any
	if personal != "" {
		p = personal
	}
	if _, err := e.mig.Exec(ctx(e.t), `INSERT INTO app.users (id, account_kind, status, staff_personal_user_id) VALUES ($1, 'STAFF', 'ACTIVE', $2)`, id, p); err != nil {
		e.t.Fatal(err)
	}
	e.rel.mu.Lock()
	e.rel.managers[id] = true
	if personal != "" {
		e.rel.personal[id] = personal
	}
	e.rel.mu.Unlock()
	return id
}

func (e *crEnv) personalUser() string {
	e.t.Helper()
	id := ids.New()
	if _, err := e.mig.Exec(ctx(e.t), `INSERT INTO app.users (id, account_kind, status) VALUES ($1, 'USER', 'ACTIVE')`, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func errCode(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func mustCode(t *testing.T, err error, code string) {
	t.Helper()
	if errCode(err) != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// openReviewed opens a staff case on subject `user`, assigns it to `assignee` and starts the review.
func (e *crEnv) openReviewed(opener, assignee, user string) compliance.Case {
	e.t.Helper()
	c, err := e.comp.Open(ctx(e.t), compliance.Staff(opener), compliance.OpenInput{CaseType: "AML_MONITORING", Severity: "S2",
		OpeningReasonCode: "STAFF_REFERRAL", Links: []compliance.LinkInput{{SubjectType: "USER", SubjectID: user, Role: "PRIMARY_SUBJECT"}}}, false)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.comp.Assign(ctx(e.t), compliance.Op{Actor: compliance.Staff(assignee), CaseID: c.ID}); err != nil {
		e.t.Fatal(err)
	}
	c, err = e.comp.Start(ctx(e.t), compliance.Op{Actor: compliance.Staff(assignee), CaseID: c.ID})
	if err != nil || c.Status != compliance.StatusInReview {
		e.t.Fatalf("start: %+v %v", c, err)
	}
	return c
}

func (e *crEnv) countOutbox(eventType, aggregateID string) int {
	var n int
	if err := e.app.QueryRow(ctx(e.t), `SELECT count(*) FROM app.outbox_events WHERE event_type = $1 AND aggregate_id = $2`,
		eventType, aggregateID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// ---- 1. gateway-only writes and least privilege -----------------------------------------------------------

func TestComplianceRoleIsGatewayOnly(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	denied := func(p *pgxpool.Pool, what, sql string, args ...any) {
		t.Helper()
		err := db.WithTx(c, p, db.TxOptions{MaxRetries: -1}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
		if sqlState(err) != "42501" {
			t.Errorf("%s: want permission denied (42501), got %v", what, err)
		}
	}
	// compliance role: no access to other modules' tables, the audit tables or the outbox
	denied(e.cmp, "read app.users", `SELECT 1 FROM app.users LIMIT 1`)
	denied(e.cmp, "read kyc", `SELECT 1 FROM kyc.kyc_cases LIMIT 1`)
	denied(e.cmp, "read audit", `SELECT 1 FROM audit.audit_events LIMIT 1`)
	denied(e.cmp, "read security audit", `SELECT 1 FROM audit.security_audit_events LIMIT 1`)
	denied(e.cmp, "insert audit directly", `INSERT INTO audit.audit_events (id, seq, occurred_at, actor_type, action, target_type, outcome, hash)
		VALUES ($1, 0, now(), 'system', 'compliance.x.y', 'test', 'success', '\x00')`, ids.New())
	denied(e.cmp, "read outbox", `SELECT 1 FROM app.outbox_events LIMIT 1`)
	denied(e.cmp, "insert outbox directly", `INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
		VALUES ($1, 'x', $1, 'compliance.x', '{}', now())`, ids.New())
	denied(e.cmp, "write risk outputs", `INSERT INTO risk.risk_signals (id, signal_type, subject_type, subject_id, source_module, source_event_id,
		source_event_type, observed_at) VALUES ($1, 'KYC_REJECTED', 'USER', $1, 'kyc', $1, 'kyc.case_rejected', now())`, ids.New())
	denied(e.cmp, "delete a case", `DELETE FROM compliance.compliance_cases`)
	// the app role has no privilege on the compliance schema
	denied(e.app, "app reads compliance", `SELECT 1 FROM compliance.compliance_cases LIMIT 1`)
	// compliance reads the risk outputs it reviews
	for _, tbl := range []string{"risk.risk_signals", "risk.risk_assessments", "risk.risk_decisions", "risk.limits"} {
		if _, err := e.cmp.Exec(c, `SELECT 1 FROM `+tbl+` LIMIT 1`); err != nil {
			t.Errorf("compliance read %s: %v", tbl, err)
		}
	}
	// gateways: compliance.* actions/events pass; other prefixes and C3-looking keys are refused
	errRollback := errors.New("rollback")
	err := db.WithTx(c, e.cmp, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "compliance.case.it_probe", ActorType: "system", TargetType: "compliance_case",
			TargetID: ids.New()}); err != nil {
			return err
		}
		if _, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "compliance_case", AggregateID: ids.New(),
			EventType: "compliance.it_probe", OccurredAt: time.Now()}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("gateway writes: %v", err)
	}
	for name, fn := range map[string]func(ctx context.Context, tx pgx.Tx) error{
		"kyc action": func(ctx context.Context, tx pgx.Tx) error {
			_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.case.approved", ActorType: "system", TargetType: "x"})
			return err
		},
		"risk event": func(ctx context.Context, tx pgx.Tx) error {
			_, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "x", AggregateID: ids.New(), EventType: "risk.escalation_recommended",
				OccurredAt: time.Now()})
			return err
		},
		"C3 key": func(ctx context.Context, tx pgx.Tx) error {
			_, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "x", AggregateID: ids.New(), EventType: "compliance.case_opened",
				Payload: map[string]any{"subject": map[string]any{"full_name": "x"}}, OccurredAt: time.Now()})
			return err
		},
	} {
		err := db.WithTx(c, e.cmp, db.TxOptions{}, fn)
		if sqlState(err) != "22023" {
			t.Errorf("gateway %s: want invalid_parameter_value, got %v", name, err)
		}
	}
	// append-only history
	cs := e.openReviewed(e.staff(""), e.staff(""), e.personalUser())
	if _, err := e.cmp.Exec(c, `UPDATE compliance.compliance_case_events SET reason_code = 'X_Y_Z' WHERE case_id = $1`, cs.ID); sqlState(err) != "42501" && sqlState(err) != "23001" {
		t.Errorf("events must be append-only: %v", err)
	}
	if _, err := e.cmp.Exec(c, `UPDATE compliance.compliance_cases SET case_number = 'CMP-2000-000001', version = version + 1 WHERE id = $1`, cs.ID); sqlState(err) != "23001" {
		t.Errorf("identity fields must be immutable: %v", err)
	}
	// a case version without its timeline event cannot commit (deferred trigger)
	if _, err := e.cmp.Exec(c, `UPDATE compliance.compliance_cases SET severity = 'S1', version = version + 1 WHERE id = $1`, cs.ID); sqlState(err) != "23514" {
		t.Errorf("version without event: %v", err)
	}
}

// ---- 2. RLS ----------------------------------------------------------------------------------------------------

func TestComplianceRLSHidesRestrictedSTR(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	officer, subject := e.staff(""), e.personalUser()
	in := compliance.OpenInput{CaseType: "AML_MONITORING", Severity: "S1", OpeningReasonCode: "INTERNAL_SUSPICION",
		Confidentiality: compliance.ConfRestrictedSTR, Note: "restricted narrative",
		Links: []compliance.LinkInput{{SubjectType: "USER", SubjectID: subject, Role: "PRIMARY_SUBJECT"}}}
	if _, err := e.comp.Open(c, compliance.Staff(officer), in, false); errCode(err) != "PERMISSION_DENIED" {
		t.Fatalf("opening a restricted case without STR access: %v", err)
	}
	str, err := e.comp.Open(c, compliance.Staff(officer), in, true)
	if err != nil || str.Confidentiality != compliance.ConfRestrictedSTR {
		t.Fatalf("open STR case: %+v %v", str, err)
	}
	normal := e.openReviewed(officer, officer, subject)

	// without the flag the restricted case and every child row are invisible
	if _, err := e.comp.Get(c, compliance.Staff(officer), str.ID, false); errCode(err) != "CASE_NOT_FOUND" {
		t.Fatalf("restricted case visible without flag: %v", err)
	}
	if _, err := e.comp.Assign(c, compliance.Op{Actor: compliance.Staff(officer), CaseID: str.ID}); errCode(err) != "CASE_NOT_FOUND" {
		t.Fatalf("restricted case writable without flag: %v", err)
	}
	if _, err := e.comp.AddNote(c, compliance.Op{Actor: compliance.Staff(officer), CaseID: str.ID, Note: "x"}); errCode(err) != "CASE_NOT_FOUND" {
		t.Fatalf("note on hidden case: %v", err)
	}
	list, _, err := e.comp.List(c, compliance.ListFilter{Limit: 100}, false)
	if err != nil {
		t.Fatal(err)
	}
	seenNormal := false
	for _, cs := range list {
		if cs.ID == str.ID || cs.Confidentiality != compliance.ConfNormal {
			t.Fatalf("restricted case listed without flag: %+v", cs)
		}
		seenNormal = seenNormal || cs.ID == normal.ID
	}
	if !seenNormal {
		t.Fatal("normal case missing from the list")
	}
	for _, tbl := range []string{"compliance_cases", "compliance_case_events", "compliance_case_links", "compliance_case_notes", "compliance_case_triggers"} {
		col := "case_id"
		if tbl == "compliance_cases" {
			col = "id"
		}
		var n int
		if err := e.cmp.QueryRow(c, `SELECT count(*) FROM compliance.`+tbl+` WHERE `+col+` = $1`, str.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows visible without flag (%v)", tbl, n, err)
		}
	}
	// with the flag (set after the permission check) it is visible, notes decrypt
	d, err := e.comp.Get(c, compliance.Staff(officer), str.ID, true)
	if err != nil || len(d.Notes) != 1 || d.Notes[0].Body != "restricted narrative" || len(d.Links) != 1 {
		t.Fatalf("restricted case with flag: %+v %v", d, err)
	}
	// confidentiality never decreases (even with the flag)
	err = db.WithTx(c, e.cmp, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('fundzim.str_access', 'on', true)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE compliance.compliance_cases SET confidentiality = 'NORMAL', version = version + 1 WHERE id = $1`, str.ID)
		return err
	})
	if sqlState(err) != "23001" {
		t.Fatalf("lowering confidentiality: %v", err)
	}
	// the flag is transaction-local: the next transaction on a pooled connection does not inherit it
	var n int
	if err := e.cmp.QueryRow(c, `SELECT count(*) FROM compliance.compliance_cases WHERE id = $1`, str.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("flag leaked: %d %v", n, err)
	}
}

// ---- 3. maker-checker and conflicts -------------------------------------------------------------------------

func TestComplianceMakerCheckerAndSubjectConflicts(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	subject := e.personalUser()
	a, b := e.staff(""), e.staff("")
	alias := e.staff(subject) // the subject's own staff account
	cs := e.openReviewed(a, a, subject)
	op := func(actor string) compliance.Op { return compliance.Op{Actor: compliance.Staff(actor), CaseID: cs.ID} }

	// conflict of interest: the subject's staff alias cannot be assigned (service, then DB without the stub)
	o := op(a)
	o.AssigneeID = alias
	if _, err := e.comp.Assign(c, o); errCode(err) != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("assigning the subject's alias (service): %v", err)
	}
	e.rel.mu.Lock()
	delete(e.rel.personal, alias) // hide the alias from the service: the database must still refuse
	e.rel.mu.Unlock()
	if _, err := e.comp.Assign(c, o); errCode(err) != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("assigning the subject's alias (database): %v", err)
	}
	// only the assignee resolves
	o = op(b)
	o.Decision, o.ReasonCode, o.Note = "SUSPEND", "PATTERN_CONFIRMED", "decision memo"
	if _, err := e.comp.Resolve(c, o); errCode(err) != "NOT_ASSIGNED" {
		t.Fatalf("resolve by non-assignee: %v", err)
	}
	// a SUSPEND needs a checker: PROPOSED, nothing emitted yet
	o.Actor = compliance.Staff(a)
	res, err := e.comp.Resolve(c, o)
	if err != nil || res.Status != compliance.StatusResolved || res.Resolution.Status != compliance.ResolutionProposed || !res.Resolution.RequiresApproval {
		t.Fatalf("resolve SUSPEND: %+v %v", res, err)
	}
	if n := e.countOutbox(compliance.EvCaseResolved, cs.ID); n != 0 {
		t.Fatalf("case_resolved emitted before approval: %d", n)
	}
	if _, err := e.comp.Close(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs.ID, ReasonCode: "DONE_DONE"}); errCode(err) != "RESOLUTION_NOT_APPROVED" {
		t.Fatalf("close before approval: %v", err)
	}
	// the decider cannot approve (application) ...
	if _, err := e.comp.ApproveResolution(c, op(a)); errCode(err) != "SELF_APPROVAL_FORBIDDEN" {
		t.Fatalf("self approval: %v", err)
	}
	// ... nor can the database be talked into it (CHECK), even bypassing the service
	err = db.WithTx(c, e.cmp, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE compliance.compliance_cases SET resolution_status = 'APPROVED', approved_by = decided_by,
			approved_at = now(), version = version + 1 WHERE id = $1`, cs.ID)
		return err
	})
	if sqlState(err) != "23514" || !strings.Contains(err.Error(), "ck_compliance_cases_maker_checker") {
		t.Fatalf("DB self approval: %v", err)
	}
	// nor can a checker-required decision be APPROVED without a checker
	err = db.WithTx(c, e.cmp, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE compliance.compliance_cases SET resolution_status = 'APPROVED', version = version + 1 WHERE id = $1`, cs.ID)
		return err
	})
	if sqlState(err) != "23514" || !strings.Contains(err.Error(), "ck_compliance_cases_checker_required") {
		t.Fatalf("DB approval without checker: %v", err)
	}
	// the subject's alias cannot approve either (DB trigger)
	if _, err := e.comp.ApproveResolution(c, op(alias)); errCode(err) != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("approval by subject alias: %v", err)
	}
	// a different, unconflicted officer approves: compliance.case_resolved is emitted once
	res, err = e.comp.ApproveResolution(c, op(b))
	if err != nil || res.Resolution.Status != compliance.ResolutionApproved || res.Resolution.ApprovedBy == nil || *res.Resolution.ApprovedBy != b {
		t.Fatalf("approval: %+v %v", res, err)
	}
	if n := e.countOutbox(compliance.EvCaseResolved, cs.ID); n != 1 {
		t.Fatalf("case_resolved events: %d", n)
	}
	if _, err := e.comp.ApproveResolution(c, op(b)); errCode(err) != "CASE_STATE_CHANGED" {
		t.Fatalf("second approval: %v", err)
	}
	closed, err := e.comp.Close(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs.ID, ReasonCode: "ACTIONS_COMPLETED"})
	if err != nil || closed.Status != compliance.StatusClosed || closed.Closure == nil {
		t.Fatalf("close: %+v %v", closed, err)
	}
	// reopen clears the resolution; the timeline keeps it
	re, err := e.comp.Reopen(c, compliance.Op{Actor: compliance.Staff(b), CaseID: cs.ID, ReasonCode: "NEW_INFORMATION", Note: "new evidence arrived"})
	if err != nil || re.Status != compliance.StatusInReview || re.Resolution != nil || re.Closure != nil {
		t.Fatalf("reopen: %+v %v", re, err)
	}
	d, err := e.comp.Get(c, compliance.Staff(b), cs.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, ev := range d.Events {
		types = append(types, ev.EventType)
	}
	for _, want := range []string{"OPENED", "ASSIGNED", "STATUS_CHANGED", "RESOLUTION_PROPOSED", "RESOLUTION_APPROVED", "CLOSED", "REOPENED"} {
		if !strings.Contains(strings.Join(types, ","), want) {
			t.Errorf("timeline lacks %s: %v", want, types)
		}
	}
	if d.Version != len(types)-countNotes(types) {
		t.Errorf("one versioned event per version: version %d, events %v", d.Version, types)
	}
	// a decision without a checker takes effect at once
	cs2 := e.openReviewed(a, a, e.personalUser())
	res, err = e.comp.Resolve(c, compliance.Op{Actor: compliance.Staff(a), CaseID: cs2.ID, Decision: "CLEARED", ReasonCode: "NO_CONCERN", Note: "checked"})
	if err != nil || res.Resolution.Status != compliance.ResolutionApproved || e.countOutbox(compliance.EvCaseResolved, cs2.ID) != 1 {
		t.Fatalf("CLEARED: %+v %v", res, err)
	}
	// audit trail through the gateway (read as the worker role)
	w := pool(t, workerURL(t))
	var audits int
	if err := w.QueryRow(c, `SELECT count(*) FROM audit.audit_events WHERE target_id = $1 AND action LIKE 'compliance.case.%'`, cs.ID).Scan(&audits); err != nil || audits < 8 {
		t.Fatalf("audit events for the case: %d %v", audits, err)
	}
	var problems int
	if err := w.QueryRow(c, `SELECT count(*) FROM audit.verify_chain('audit.audit_events')`).Scan(&problems); err != nil || problems != 0 {
		t.Fatalf("audit chain: %d problems, %v", problems, err)
	}
}

func countNotes(types []string) int {
	n := 0
	for _, t := range types {
		if t == "NOTE_ADDED" || t == "SOURCE_EVENT_LINKED" || t == "SUBJECT_LINKED" {
			n++
		}
	}
	return n
}

func TestComplianceOrganisationMemberCannotDecide(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	member := e.personalUser()
	org := ids.New()
	e.rel.mu.Lock()
	e.rel.members[org] = map[string]bool{member: true}
	e.rel.mu.Unlock()
	a := e.staff(member) // staff whose personal account is a member of the subject organisation
	cs, err := e.comp.Open(c, compliance.Staff(e.staff("")), compliance.OpenInput{CaseType: "KYB_REVIEW", Severity: "S3", OpeningReasonCode: "STAFF_REFERRAL",
		Links: []compliance.LinkInput{{SubjectType: "ORGANISATION", SubjectID: org, Role: "PRIMARY_SUBJECT"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.comp.Assign(c, compliance.Op{Actor: compliance.Staff(a), CaseID: cs.ID}); errCode(err) != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("org member self-assign: %v", err)
	}
	// without the relations adapter the check fails closed
	noRel, err := compliance.New(compliance.Deps{Pool: e.cmp, NotesAEAD: mustAEAD(t), StepUpMaxAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noRel.Assign(c, compliance.Op{Actor: compliance.Staff(a), CaseID: cs.ID}); errCode(err) != "SUBJECT_CHECK_UNAVAILABLE" {
		t.Fatalf("fail closed without relations: %v", err)
	}
}

func mustAEAD(t *testing.T) *crypto.AEAD {
	a, err := crypto.NewAEAD("local-compliance-1", os.Getenv("COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// ---- 4. concurrency ---------------------------------------------------------------------------------------

func TestComplianceConcurrentDecisionsExactlyOneWins(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	a, b, cc := e.staff(""), e.staff(""), e.staff("")
	for round := 0; round < 5; round++ {
		cs := e.openReviewed(a, a, e.personalUser())
		// two sessions of the assignee resolve at the same time with different decisions
		var wins, conflicts atomic.Int32
		var wg sync.WaitGroup
		for i, dec := range []string{"OFFBOARD", "RESTRICT"} {
			wg.Add(1)
			go func(i int, dec string) {
				defer wg.Done()
				_, err := e.comp.Resolve(c, compliance.Op{Actor: compliance.Staff(a), CaseID: cs.ID, Decision: dec, ReasonCode: "PATTERN_CONFIRMED", Note: "memo"})
				switch errCode(err) {
				case "":
					if err == nil {
						wins.Add(1)
						return
					}
					t.Errorf("resolve %d: %v", i, err)
				case "CASE_STATE_CHANGED":
					conflicts.Add(1)
				default:
					t.Errorf("resolve %d: %v", i, err)
				}
			}(i, dec)
		}
		wg.Wait()
		if wins.Load() != 1 || conflicts.Load() != 1 {
			t.Fatalf("round %d resolve: %d wins, %d conflicts", round, wins.Load(), conflicts.Load())
		}
		// two checkers approve at the same time: exactly one approval, one case_resolved event
		wins.Store(0)
		conflicts.Store(0)
		for _, checker := range []string{b, cc} {
			wg.Add(1)
			go func(checker string) {
				defer wg.Done()
				_, err := e.comp.ApproveResolution(c, compliance.Op{Actor: compliance.Staff(checker), CaseID: cs.ID})
				if err == nil {
					wins.Add(1)
				} else if errCode(err) == "CASE_STATE_CHANGED" {
					conflicts.Add(1)
				} else {
					t.Errorf("approve: %v", err)
				}
			}(checker)
		}
		wg.Wait()
		if wins.Load() != 1 || conflicts.Load() != 1 || e.countOutbox(compliance.EvCaseResolved, cs.ID) != 1 {
			t.Fatalf("round %d approve: %d wins, %d conflicts, %d events", round, wins.Load(), conflicts.Load(), e.countOutbox(compliance.EvCaseResolved, cs.ID))
		}
	}
	// a stale optimistic version is refused
	cs := e.openReviewed(a, a, e.personalUser())
	if _, err := e.comp.RequestInformation(c, compliance.Op{Actor: compliance.Staff(a), CaseID: cs.ID, ReasonCode: "NEED_DOCUMENTS", IfVersion: cs.Version - 1}); errCode(err) != "CASE_STATE_CHANGED" {
		t.Fatalf("stale If-Match: %v", err)
	}
}

// ---- 5. consumers: idempotent case opening ---------------------------------------------------------------

func escalation(evType string, payload map[string]any) outbox.Delivery {
	b, _ := json.Marshal(payload)
	return outbox.Delivery{EventID: ids.New(), EventType: evType, Payload: b, OccurredAt: time.Now().UTC()}
}

func TestComplianceDuplicateDeliveryOpensOneCase(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	user, kycCase := e.personalUser(), ids.New()
	d := escalation(compliance.EvKYCCaseEscalated, map[string]any{"case_id": kycCase, "kind": "KYC", "subject_type": "USER",
		"subject_id": user, "reason_code": "POSSIBLE_PEP"})
	var wg sync.WaitGroup
	results := make([]compliance.TriggerResult, 6)
	errsOut := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errsOut[i] = e.comp.OpenFromEvent(c, d) }(i)
	}
	wg.Wait()
	opened, dups := 0, 0
	var caseID string
	for i := range results {
		if errsOut[i] != nil {
			t.Fatalf("delivery %d: %v", i, errsOut[i])
		}
		if results[i].Duplicate {
			dups++
		} else if results[i].Outcome == "OPENED" {
			opened++
			caseID = results[i].CaseID
		}
	}
	if opened != 1 || dups != 5 {
		t.Fatalf("concurrent duplicate deliveries: %d opened, %d duplicates", opened, dups)
	}
	if err := e.comp.HandleEscalation(c, d); err != nil { // a later redelivery is a no-op
		t.Fatal(err)
	}
	var n int
	if err := e.cmp.QueryRow(c, `SELECT count(*) FROM compliance.compliance_case_links WHERE subject_type = 'KYC_CASE' AND subject_id = $1`, kycCase).Scan(&n); err != nil || n != 1 {
		t.Fatalf("cases for the kyc case: %d %v", n, err)
	}
	if e.countOutbox(compliance.EvCaseOpened, caseID) != 1 {
		t.Fatal("case_opened must be emitted exactly once")
	}
	cs, err := e.comp.Get(c, compliance.Staff(e.staff("")), caseID, false)
	if err != nil || cs.CaseType != "KYC_REVIEW" || cs.Source != "KYC_ESCALATION" || cs.OpenedBy.Type != "SYSTEM" || len(cs.Links) != 2 {
		t.Fatalf("opened case: %+v %v", cs, err)
	}
	// a different event about the same subject links to the open case instead of opening another
	d2 := escalation(compliance.EvBeneficiaryEscalated, map[string]any{"beneficiary_id": ids.New(), "owner_type": "USER", "owner_id": user})
	r2, err := e.comp.OpenFromEvent(c, d2)
	if err != nil || r2.Outcome != "OPENED" { // anchored on the beneficiary, which has no open case
		t.Fatalf("beneficiary escalation: %+v %v", r2, err)
	}
	d3 := escalation(compliance.EvRiskEscalationRecommended, map[string]any{"subject_type": "USER", "subject_id": user,
		"assessment_id": ids.New(), "decision_id": ids.New(), "rating": "RESTRICTED"})
	r3, err := e.comp.OpenFromEvent(c, d3)
	if err != nil || r3.Outcome != "LINKED" || (r3.CaseID != caseID && r3.CaseID != r2.CaseID) {
		t.Fatalf("risk escalation for a subject with an open case must link: %+v %v (cases %s %s)", r3, err, caseID, r2.CaseID)
	}
	// unusable payloads are acknowledged without opening anything
	if err := e.comp.HandleEscalation(c, escalation(compliance.EvKYCCaseEscalated, map[string]any{"case_id": "x"})); err != nil {
		t.Fatal(err)
	}
}

// ---- 6. risk ------------------------------------------------------------------------------------------------

func TestRiskSignalDedupeAndAssessment(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	user := ids.New() // subjects are plain ids (no FK)
	d := escalation(risk.EvKYCCaseRejected, map[string]any{"case_id": ids.New(), "kind": "KYC", "subject_type": "USER", "subject_id": user,
		"reason_code": "DOCUMENT_UNREADABLE"})
	sig, ok, err := risk.SignalFromEvent(d)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	var wg sync.WaitGroup
	outs := make([]risk.Outcome, 5)
	errs5 := make([]error, 5)
	for i := range outs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); outs[i], errs5[i] = e.risk.RecordSignal(c, sig) }(i)
	}
	wg.Wait()
	fresh := 0
	for i := range outs {
		if errs5[i] != nil {
			t.Fatal(errs5[i])
		}
		if !outs[i].Duplicate {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("concurrent duplicate signals recorded %d times", fresh)
	}
	var signals, assessments int
	_ = e.app.QueryRow(c, `SELECT count(*) FROM risk.risk_signals WHERE source_event_id = $1`, d.EventID).Scan(&signals)
	_ = e.app.QueryRow(c, `SELECT count(*) FROM risk.risk_assessments WHERE subject_id = $1`, user).Scan(&assessments)
	if signals != 1 || assessments != 1 {
		t.Fatalf("signals %d assessments %d", signals, assessments)
	}
	asm, sigs, err := e.risk.Latest(c, "USER", user)
	if err != nil || asm.Rating != risk.RatingStandard || asm.Score != 250 || asm.ModelVersion != risk.ModelVersion || len(sigs) != 1 ||
		asm.Decision == nil || asm.Decision.Decision != risk.DecisionNoAction || len(asm.Explanation) != len(risk.RulesV1) {
		t.Fatalf("latest: %+v %+v %v", asm, sigs, err)
	}
	// a destination rejection lifts the subject to ENHANCED → MANUAL_REVIEW (no event)
	out, err := e.risk.RecordSignal(c, mustSignal(t, escalation(risk.EvPayoutDestinationRejected, map[string]any{"destination_id": ids.New(),
		"owner_type": "USER", "owner_id": user})))
	if err != nil || out.Rating != risk.RatingEnhanced || out.Decision != risk.DecisionManualReview || out.EventID != "" {
		t.Fatalf("enhanced: %+v %v", out, err)
	}
	// a duplicate-identity signal makes it RESTRICTED → ESCALATE with risk.escalation_recommended
	out, err = e.risk.RecordSignal(c, mustSignal(t, escalation(risk.EvKYCDuplicateIdentity, map[string]any{"case_id": ids.New(),
		"subject_type": "USER", "subject_id": user, "other_profile_count": 1})))
	if err != nil || out.Rating != risk.RatingRestricted || out.Decision != risk.DecisionEscalate || out.EventID == "" {
		t.Fatalf("restricted: %+v %v", out, err)
	}
	var payload []byte
	if err := e.app.QueryRow(c, `SELECT payload FROM app.outbox_events WHERE id = $1 AND event_type = 'risk.escalation_recommended'`, out.EventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(out.DecisionID)) || !bytes.Contains(payload, []byte(`"rating": "RESTRICTED"`)) && !bytes.Contains(payload, []byte(`"rating":"RESTRICTED"`)) {
		t.Fatalf("escalation payload: %s", payload)
	}
	// append-only and never-fraud enforced by the database
	if _, err := e.app.Exec(c, `UPDATE risk.risk_decisions SET decision = 'NO_ACTION' WHERE id = $1`, out.DecisionID); sqlState(err) != "42501" && sqlState(err) != "23001" {
		t.Fatalf("decisions must be append-only: %v", err)
	}
	_, err = e.app.Exec(c, `INSERT INTO risk.risk_decisions (id, assessment_id, subject_type, subject_id, decision, reason_code, policy_version, decided_at)
		VALUES ($1, $2, 'USER', $3, 'MANUAL_REVIEW', 'SUSPECTED_FRAUD', 'risk-policy-v1', now())`, ids.New(), out.AssessmentID, user)
	if sqlState(err) != "23514" || !strings.Contains(err.Error(), "ck_risk_decisions_no_fraud_claim") {
		t.Fatalf("fraud wording accepted: %v", err)
	}
}

func mustSignal(t *testing.T, d outbox.Delivery) risk.Signal {
	t.Helper()
	s, ok, err := risk.SignalFromEvent(d)
	if err != nil || !ok {
		t.Fatalf("signal from %s: %v %v", d.EventType, ok, err)
	}
	return s
}

func TestRiskLimitsMakerCheckerAndBaseline(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	var n int
	if err := e.app.QueryRow(c, `SELECT count(*) FROM risk.limits WHERE approval_basis = 'MIGRATION_BASELINE' AND status = 'APPROVED'
		AND limit_type = 'INTERNAL_RISK'`).Scan(&n); err != nil || n != len(risk.RequiredKeys()) {
		t.Fatalf("baseline limits: %d %v", n, err)
	}
	// a runtime role can never create a baseline row or approve without a request
	maker, checker := e.staff(""), e.staff("")
	_, err := e.app.Exec(c, `INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_count, source_type, source_ref,
		approval_owner_role, approval_basis, status, made_by, effective_from, review_by) VALUES ($1, 'risk.it.k', 1, 'INTERNAL_RISK', 'GLOBAL', 'COUNT', 1,
		'INTERNAL_DECISION', 'DEC-S5-RISK-BASELINE', 'COMPLIANCE', 'MIGRATION_BASELINE', 'PROPOSED', '00000000-0000-0000-0000-000000000001', now(), current_date + 30)`, ids.New())
	if sqlState(err) != "42501" {
		t.Fatalf("baseline insert by app role: %v", err)
	}
	limitID, reqID := ids.New(), ids.New()
	key := "risk.it_" + strings.ReplaceAll(ids.New()[24:], "-", "") + ".weight"
	if _, err := e.app.Exec(c, `INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_count, source_type, source_ref,
		approval_owner_role, status, made_by, effective_from, review_by) VALUES ($1, $2, 1, 'INTERNAL_RISK', 'GLOBAL', 'COUNT', 5,
		'INTERNAL_DECISION', 'DEC-IT-1', 'COMPLIANCE', 'PROPOSED', $3, now(), current_date + 30)`, limitID, key, maker); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Exec(c, `UPDATE risk.limits SET status = 'APPROVED', approved_by = $2, approved_at = now() WHERE id = $1`, limitID, checker); sqlState(err) != "23514" {
		t.Fatalf("approval without a request: %v", err)
	}
	if _, err := e.app.Exec(c, `INSERT INTO risk.limit_change_requests (id, limit_id, action, status, requested_by, justification, payload_sha256, expires_at)
		VALUES ($1, $2, 'ACTIVATE', 'PENDING', $3, 'calibrated weight', sha256('x'), now() + interval '7 days')`, reqID, limitID, maker); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Exec(c, `UPDATE risk.limit_change_requests SET status = 'APPROVED', decided_by = $2, decided_at = now(), checker_step_up_at = now(),
		version = version + 1 WHERE id = $1`, reqID, maker); sqlState(err) != "23514" {
		t.Fatalf("maker approving own request: %v", err)
	}
	if _, err := e.app.Exec(c, `UPDATE risk.limit_change_requests SET status = 'APPROVED', decided_by = $2, decided_at = now(), checker_step_up_at = now(),
		version = version + 1 WHERE id = $1`, reqID, checker); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Exec(c, `UPDATE risk.limits SET status = 'APPROVED', approved_by = $2, approved_at = now(), approval_request_id = $3 WHERE id = $1`,
		limitID, checker, reqID); err != nil {
		t.Fatalf("approval with the checker's request: %v", err)
	}
	if _, err := e.app.Exec(c, `UPDATE risk.limits SET value_count = 6 WHERE id = $1`, limitID); sqlState(err) != "23001" {
		t.Fatalf("approved limit must be immutable: %v", err)
	}
}

// ---- 7. end to end through the outbox pipeline ------------------------------------------------------------

func TestRiskEscalationOpensComplianceCaseThroughOutbox(t *testing.T) {
	e := newCREnv(t)
	need(t, "POSTGRES_WORKER_PASSWORD")
	c := ctx(t)
	wrk := pool(t, workerURL(t))
	reg := outbox.NewRegistry()
	for _, cons := range e.risk.Consumers() {
		if cons.EventType == risk.EvKYCDuplicateIdentity {
			reg.Subscribe(cons.Name, cons.EventType, cons.Handle)
		}
	}
	for _, cons := range e.comp.Consumers() {
		if cons.EventType == compliance.EvRiskEscalationRecommended {
			reg.Subscribe(cons.Name, cons.EventType, cons.Handle)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	queue := "it_cr_" + letters(8)
	relay := &outbox.Relay{Pool: wrk, Registry: reg, Metrics: outbox.NewMetrics(prometheus.NewRegistry()), DeliverQueue: queue,
		DeliverMaxAttempts: 5, EventTypes: []string{risk.EvKYCDuplicateIdentity, compliance.EvRiskEscalationRecommended},
		Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	deliver := &outbox.DeliverWorker{Pool: wrk, Registry: reg, Logger: logger,
		Backoff: jobs.Backoff{Base: 100 * time.Millisecond, Cap: 500 * time.Millisecond}, JobLimit: 20 * time.Second}
	workers := river.NewWorkers()
	river.AddWorker(workers, deliver)
	river.AddWorker(workers, &outbox.RelayWorker{Relay: relay})
	river.AddWorker(workers, &outbox.PurgeWorker{Pool: wrk, Logger: logger})
	river.AddWorker(workers, &jobs.PurgeIdempotencyKeysWorker{Pool: wrk, Logger: logger})
	client, err := jobs.NewWorkerClient(jobs.WorkerConfig{Pool: wrk, Logger: logger, Metrics: jobs.NewMetrics(prometheus.NewRegistry()),
		Workers: workers, Queues: map[string]river.QueueConfig{queue: {MaxWorkers: 4}}, ID: "cr_" + queue, JobTimeout: 20 * time.Second,
		RescueStuckJobsAfter: time.Minute, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.StopAndCancel(sctx)
	})

	// a kyc.duplicate_identity_detected fact (fixture written on the app pool; kyc itself writes via its gateway)
	user := ids.New()
	err = db.WithTx(c, e.app, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		id, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "kyc_case", AggregateID: ids.New(), EventType: risk.EvKYCDuplicateIdentity,
			Payload: map[string]any{"case_id": ids.New(), "subject_type": "USER", "subject_id": user, "other_profile_count": 1}, OccurredAt: time.Now()})
		if err != nil {
			return err
		}
		// held back from any other relay serving this database (worker_outbox_test pattern)
		_, err = tx.Exec(ctx, `UPDATE app.outbox_events SET available_at = now() + interval '1 hour' WHERE id = $1`, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var caseID string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && caseID == "" {
		if _, err := relay.RunOnce(c, client); err != nil {
			t.Fatal(err)
		}
		_ = e.cmp.QueryRow(c, `SELECT case_id FROM compliance.compliance_case_links WHERE subject_type = 'USER' AND subject_id = $1 LIMIT 1`, user).Scan(&caseID)
		time.Sleep(200 * time.Millisecond)
	}
	if caseID == "" {
		var dispatched int
		_ = e.app.QueryRow(c, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'risk.escalation_recommended'
			AND payload->>'subject_id' = $1`, user).Scan(&dispatched)
		t.Fatalf("no compliance case for the escalated subject (escalation events: %d; if another relay serves this database it may "+
			"have dispatched them first — run against a database without the worker container)", dispatched)
	}
	cs, err := e.comp.Get(c, compliance.Staff(e.staff("")), caseID, false)
	if err != nil || cs.CaseType != "RISK_REVIEW" || cs.Source != "RISK_ENGINE" || cs.OpeningReasonCode != "RISK_RATING_RESTRICTED" {
		t.Fatalf("case: %+v %v", cs, err)
	}
	kinds := map[string]bool{}
	for _, l := range cs.Links {
		kinds[l.SubjectType+"/"+l.Role] = true
	}
	if !kinds["USER/PRIMARY_SUBJECT"] || !kinds["RISK_DECISION/RELATED_OBJECT"] || !kinds["RISK_ASSESSMENT/RELATED_OBJECT"] {
		t.Fatalf("links: %+v", cs.Links)
	}
	asm, _, err := e.risk.Latest(c, "USER", user)
	if err != nil || asm.Rating != risk.RatingRestricted || asm.Decision.Decision != risk.DecisionEscalate {
		t.Fatalf("risk latest: %+v %v", asm, err)
	}
}

// ---- 8. HTTP ------------------------------------------------------------------------------------------------

type crAuth struct{ perms map[string]map[string]bool }

// Authorize mirrors auth.Authorize for permission routes: 404 for non-staff, 403 without the permission.
func (a crAuth) Authorize(r *http.Request, pol httpx.Policy) error {
	p := authz.PrincipalFrom(r.Context())
	if pol.Kind != httpx.KindPermission {
		return errs.New(errs.Forbidden, "PERMISSION_DENIED", "denied")
	}
	if p == nil || p.Kind != authz.KindStaff {
		return errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	}
	if !p.Has(pol.Permission) {
		return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action.")
	}
	return nil
}

type crHTTP struct {
	t   *testing.T
	srv *httptest.Server
}

func newCRHTTP(t *testing.T, e *crEnv, perms map[string]map[string]bool) *crHTTP {
	r := httpx.NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.SetAuthorizer(crAuth{perms})
	for _, rt := range e.comp.Routes() {
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler)
	}
	principal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if id := req.Header.Get("X-Test-Staff"); id != "" {
				p := &authz.Principal{UserID: id, Kind: authz.KindStaff, Permissions: perms[id], MFAVerifiedAt: time.Now()}
				if req.Header.Get("X-Test-Step-Up") == "1" {
					p.StepUpAt = time.Now()
				}
				req = req.WithContext(authz.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(w, req)
		})
	}
	srv := httptest.NewServer(httpx.Chain(r, principal))
	t.Cleanup(srv.Close)
	return &crHTTP{t: t, srv: srv}
}

func (h *crHTTP) do(staff string, stepUp bool, method, path string, body any, hdr ...string) apiResp {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+"/api/v1/admin/compliance/cases"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if staff != "" {
		req.Header.Set("X-Test-Staff", staff)
	}
	if stepUp {
		req.Header.Set("X-Test-Step-Up", "1")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Header: resp.Header}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		h.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	out.Data = env.Data
	if len(env.Error) > 0 {
		_ = json.Unmarshal(env.Error, &out.Error)
	}
	return out
}

func (h *crHTTP) expect(r apiResp, status int, code string) {
	h.t.Helper()
	if r.Status != status || (code != "" && r.Error.Code != code) {
		h.t.Fatalf("want %d %s, got %d %s (%s)", status, code, r.Status, r.Error.Code, r.Data)
	}
}

func TestComplianceHTTPWorkflow(t *testing.T) {
	e := newCREnv(t)
	c := ctx(t)
	officer := map[string]bool{compliance.PermView: true, compliance.PermManage: true, compliance.PermCreate: true}
	a, b, support := e.staff(""), e.staff(""), e.staff("")
	h := newCRHTTP(t, e, map[string]map[string]bool{a: officer, b: officer, support: {compliance.PermCreate: true}})
	subject := e.personalUser()

	h.expect(h.do("", false, "GET", "", nil), 404, "ROUTE_NOT_FOUND")        // not staff
	h.expect(h.do(support, false, "GET", "", nil), 403, "PERMISSION_DENIED") // no compliance.case.view
	h.expect(h.do(support, false, "POST", "", map[string]any{"case_type": "NOPE"}), 422, "VALIDATION_FAILED")
	h.expect(h.do(support, false, "POST", "", map[string]any{"case_type": "OTHER", "unknown": 1}), 422, "VALIDATION_FAILED")
	r := h.do(support, false, "POST", "", map[string]any{"case_type": "ACCOUNT_TAKEOVER", "severity": "S2", "reason_code": "USER_REPORT",
		"subjects": []map[string]string{{"subject_type": "USER", "subject_id": subject, "role": "PRIMARY_SUBJECT"}}, "note": "caller says the account was taken over"})
	h.expect(r, 201, "")
	id := r.field(t, "id")
	if !strings.HasPrefix(r.field(t, "case_number"), "CMP-") || r.field(t, "status") != "OPEN" {
		t.Fatalf("created: %s", r.Data)
	}
	h.expect(h.do(support, false, "POST", "/"+id+"/assign", nil), 403, "PERMISSION_DENIED")
	h.expect(h.do(a, false, "GET", "/"+ids.New(), nil), 404, "CASE_NOT_FOUND")
	h.expect(h.do(a, false, "GET", "/not-a-uuid", nil), 404, "CASE_NOT_FOUND")
	h.expect(h.do(a, false, "POST", "/"+id+"/start", nil), 409, "CASE_STATE_CHANGED") // OPEN: assign first
	h.expect(h.do(a, false, "POST", "/"+id+"/assign", nil), 200, "")
	h.expect(h.do(b, false, "POST", "/"+id+"/start", nil), 409, "NOT_ASSIGNED")
	h.expect(h.do(a, false, "POST", "/"+id+"/start", map[string]any{}), 200, "")
	h.expect(h.do(a, false, "POST", "/"+id+"/notes", map[string]string{"body": "spoke to the PSP; awaiting statement"}), 201, "")
	h.expect(h.do(a, false, "POST", "/"+id+"/request-info", map[string]string{"reason_code": "bad code"}), 422, "VALIDATION_FAILED")
	h.expect(h.do(a, false, "POST", "/"+id+"/request-info", map[string]string{"reason_code": "NEED_PSP_STATEMENT"}), 200, "")
	h.expect(h.do(a, false, "POST", "/"+id+"/escalate", map[string]string{"reason_code": "SENIOR_REVIEW"}), 409, "CASE_STATE_CHANGED")
	h.expect(h.do(a, false, "POST", "/"+id+"/start", nil), 200, "")
	r = h.do(a, false, "POST", "/"+id+"/escalate", map[string]string{"reason_code": "SENIOR_REVIEW", "severity": "S1"})
	h.expect(r, 200, "")
	if r.field(t, "severity") != "S1" || r.field(t, "status") != "ESCALATED" {
		t.Fatalf("escalated: %s", r.Data)
	}
	resolve := map[string]string{"decision": "OFFBOARD", "reason_code": "ACCOUNT_MISUSE", "note": "offboarding memo"}
	h.expect(h.do(a, false, "POST", "/"+id+"/resolve", resolve), 403, "STEP_UP_REQUIRED")
	var cur struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(h.do(a, false, "GET", "/"+id, nil).Data, &cur)
	h.expect(h.do(a, true, "POST", "/"+id+"/resolve", resolve, "If-Match", `"1"`), 409, "CASE_STATE_CHANGED")
	r = h.do(a, true, "POST", "/"+id+"/resolve", resolve, "If-Match", `"`+itoa(cur.Version)+`"`)
	h.expect(r, 202, "")
	h.expect(h.do(a, true, "POST", "/"+id+"/approve-resolution", nil), 403, "SELF_APPROVAL_FORBIDDEN")
	h.expect(h.do(b, false, "POST", "/"+id+"/approve-resolution", nil), 403, "STEP_UP_REQUIRED")
	h.expect(h.do(b, true, "POST", "/"+id+"/close", map[string]string{"reason_code": "DONE_DONE"}), 409, "RESOLUTION_NOT_APPROVED")
	h.expect(h.do(b, true, "POST", "/"+id+"/approve-resolution", map[string]string{"note": "agree"}), 200, "")
	h.expect(h.do(b, true, "POST", "/"+id+"/close", map[string]string{"reason_code": "ACTIONS_COMPLETED"}), 200, "")
	h.expect(h.do(b, true, "POST", "/"+id+"/reopen", map[string]string{"reason_code": "NEW_INFORMATION"}), 422, "VALIDATION_FAILED")
	h.expect(h.do(b, true, "POST", "/"+id+"/reopen", map[string]string{"reason_code": "NEW_INFORMATION", "note": "user appealed"}), 200, "")

	// detail: timeline + decrypted notes (staff endpoint only); list filters
	r = h.do(b, false, "GET", "/"+id, nil)
	h.expect(r, 200, "")
	var d compliance.CaseDetail
	if err := json.Unmarshal(r.Data, &d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "IN_REVIEW" || len(d.Notes) < 5 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("detail: status %s, %d notes", d.Status, len(d.Notes))
	}
	bodies := map[string]bool{}
	for _, n := range d.Notes {
		bodies[n.Body] = true
		if n.Visibility != "STAFF_ONLY" {
			t.Fatalf("note visibility %s", n.Visibility)
		}
	}
	for _, want := range []string{"caller says the account was taken over", "spoke to the PSP; awaiting statement", "offboarding memo", "agree", "user appealed"} {
		if !bodies[want] {
			t.Errorf("note %q missing", want)
		}
	}
	r = h.do(a, false, "GET", "?status=IN_REVIEW&assigned=me&limit=100", nil)
	h.expect(r, 200, "")
	if !strings.Contains(string(r.Data), id) {
		t.Fatal("assigned=me list lacks the case")
	}
	h.expect(h.do(a, false, "GET", "?status=BOGUS", nil), 422, "VALIDATION_FAILED")

	// notes are ciphertext at rest and never appear in audit metadata or outbox payloads
	var sealed []byte
	if err := e.cmp.QueryRow(c, `SELECT body_ciphertext FROM compliance.compliance_case_notes WHERE case_id = $1 ORDER BY created_at LIMIT 1`, id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("taken over")) {
		t.Fatal("note stored in clear text")
	}
	w := pool(t, workerURL(t))
	var leaks int
	if err := w.QueryRow(c, `SELECT count(*) FROM audit.audit_events WHERE target_id = $1 AND (metadata::text LIKE '%memo%' OR metadata::text LIKE '%appealed%')`, id).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("note text in audit metadata: %d %v", leaks, err)
	}
	if err := e.app.QueryRow(c, `SELECT count(*) FROM app.outbox_events WHERE aggregate_id = $1 AND payload::text LIKE '%memo%'`, id).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("note text in outbox: %d %v", leaks, err)
	}
	if e.countOutbox(compliance.EvCaseOpened, id) != 1 || e.countOutbox(compliance.EvCaseResolved, id) != 1 {
		t.Fatal("case_opened / case_resolved events")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
