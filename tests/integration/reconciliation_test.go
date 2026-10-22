//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/reconciliation"
)

// Stage 17 reconciliation (ADR-049). These tests share the development database with the compose worker, which also
// parses statements and executes runs: every assertion is scoped to the test's own lines (unique token in every line
// id and statement reference) and records. Runs are requested and executed in-process; when the worker gets there
// first the test waits for the run it queued.

type reconIT struct {
	s   *itServer
	f   *verifStaff
	mig *pgxpool.Pool
	app *pgxpool.Pool
	rc  *reconciliation.Service
	tok string
}

func newReconIT(t *testing.T, s *itServer, f *verifStaff) *reconIT {
	t.Helper()
	need(t, "DATABASE_MIGRATION_URL")
	if s.d.Reconciliation == nil {
		t.Fatal("reconciliation is not wired (storage or a financial module missing)")
	}
	r := &reconIT{s: s, f: f, mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL")), app: pool(t, os.Getenv("DATABASE_URL")), rc: s.d.Reconciliation,
		tok: strings.ToUpper(ids.New()[24:])}
	r.ensureRuleSet(t)
	return r
}

// stepped runs a staff request, stepping up with TOTP when the permission asks for it.
func (r *reconIT) stepped(t *testing.T, c staffCred, method, path string, body any, hdr ...string) apiResp {
	t.Helper()
	b := r.s.staff(t, c)
	resp := b.do(method, path, body, hdr...)
	if resp.Status == 403 && resp.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		resp = b.do(method, path, body, hdr...)
	}
	return resp
}

// ensureRuleSet makes sure a matching rule set is ACTIVE (one global set; tests reuse whatever is active).
func (r *reconIT) ensureRuleSet(t *testing.T) {
	t.Helper()
	if _, ok, err := r.rc.ActiveRuleSet(ctx(t)); err != nil {
		t.Fatal(err)
	} else if ok {
		return
	}
	// a pending proposal left by an interrupted run blocks a new one: withdraw it (only its proposer may) or approve it
	var pending, proposer string
	_ = r.mig.QueryRow(ctx(t), `SELECT id::text, proposed_by::text FROM recon.matching_rule_sets WHERE status = 'PENDING_APPROVAL'`).Scan(&pending, &proposer)
	if pending == "" {
		resp := r.stepped(t, r.f.finance1, "POST", "/admin/reconciliation/rule-sets", map[string]any{"config": map[string]int{"date_window_days": 2,
			"settlement_horizon_days": 1, "stale_after_hours": 720}, "change_reason": "integration tests: engineering values for TEST statements"},
			"Idempotency-Key", ids.New())
		if resp.Status != 201 && resp.Error.Code != "RULE_SET_PENDING" {
			t.Fatalf("propose rule set: %d %s", resp.Status, resp.Error.Code)
		}
		_ = r.mig.QueryRow(ctx(t), `SELECT id::text, proposed_by::text FROM recon.matching_rule_sets WHERE status = 'PENDING_APPROVAL'`).Scan(&pending, &proposer)
	}
	checker := r.f.finance2
	if proposer == r.f.finance2.id {
		checker = r.f.finance1
	}
	resp := r.stepped(t, checker, "POST", "/admin/reconciliation/rule-sets/"+pending+"/approve", map[string]string{"note": "approved for TEST"})
	if resp.Status != 200 {
		if _, ok, _ := r.rc.ActiveRuleSet(ctx(t)); !ok {
			t.Fatalf("approve rule set: %d %s", resp.Status, resp.Error.Code)
		}
	}
}

func (r *reconIT) upload(t *testing.T, c staffCred, source, media, supersedes string, content []byte) apiResp {
	t.Helper()
	b := r.s.staff(t, c)
	send := func() apiResp {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("source", source)
		if supersedes != "" {
			_ = mw.WriteField("supersedes_import_id", supersedes)
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="../statement.`+strings.TrimPrefix(media, "text/")+`"`)
		h.Set("Content-Type", media)
		fw, _ := mw.CreatePart(h)
		_, _ = fw.Write(content)
		_ = mw.Close()
		req, _ := http.NewRequest("POST", r.s.srv.URL+"/api/v1/admin/reconciliation/imports", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Forwarded-For", b.ip)
		req.Header.Set("X-CSRF-Token", b.cookie("fz_csrf"))
		resp, err := b.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		out := apiResp{Status: resp.StatusCode, Header: resp.Header}
		var env struct {
			Data  json.RawMessage `json:"data"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("upload: non-JSON %d: %s", resp.StatusCode, raw)
		}
		out.Data = env.Data
		_ = json.Unmarshal(env.Error, &out.Error)
		return out
	}
	resp := send()
	if resp.Status == 403 && resp.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		resp = send()
	}
	return resp
}

// waitImport waits until the statement's scan finished and the parser reached a final state.
func (r *reconIT) waitImport(t *testing.T, id string) reconciliation.Import {
	t.Helper()
	var imp reconciliation.Import
	waitFor(t, 120*time.Second, "statement "+id+" parsed or failed", func() bool {
		_, _ = r.rc.ParseImport(ctx(t), id)
		var err error
		if imp, err = r.rc.ImportByID(ctx(t), id); err != nil {
			t.Fatal(err)
		}
		return imp.Status == "PARSED" || imp.Status == "FAILED"
	})
	return imp
}

func (r *reconIT) importOK(t *testing.T, source string, content []byte) reconciliation.Import {
	t.Helper()
	resp := r.upload(t, r.f.finance1, source, "application/json", "", content)
	if resp.Status != 202 {
		t.Fatalf("upload: %d %s %+v", resp.Status, resp.Error.Code, resp.Error.Details)
	}
	imp := r.waitImport(t, resp.field(t, "id"))
	if imp.Status != "PARSED" {
		t.Fatalf("import %s: %v %+v", imp.Status, imp.FailureCode, imp.ValidationReport)
	}
	return imp
}

// run queues an on-demand run for the scope and executes it (or waits for the worker that took it).
func (r *reconIT) run(t *testing.T, svc *reconciliation.Service, source, currency string) reconciliation.Run {
	t.Helper()
	var run reconciliation.Run
	waitFor(t, 120*time.Second, "a run of "+source, func() bool {
		var err error
		run, err = svc.RequestRun(ctx(t), r.f.finance1.id, reconciliation.RunInput{Source: source, Currency: currency, Reason: "integration test run"})
		if errors.Is(err, reconciliation.ErrRunActive) {
			_, _ = svc.ExecuteQueued(ctx(t), 2)
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return true
	})
	if err := svc.ExecuteRun(ctx(t), run.ID); err != nil {
		t.Fatalf("execute run: %v", err)
	}
	waitFor(t, 120*time.Second, "run "+run.ID+" finished", func() bool {
		var err error
		if run, err = svc.RunByID(ctx(t), run.ID); err != nil {
			t.Fatal(err)
		}
		return run.Status == "COMPLETED" || run.Status == "FAILED"
	})
	if run.Status != "COMPLETED" {
		t.Fatalf("run %s: %s %v", run.ID, run.Status, run.FailureCode)
	}
	return run
}

// line returns this test's line by its id suffix, with its active match.
func (r *reconIT) line(t *testing.T, importID, lineID string) reconciliation.ItemView {
	t.Helper()
	items, err := r.rc.Items(ctx(t), reconciliation.ItemFilter{ImportID: importID, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ExternalLineID == lineID {
			return it
		}
	}
	t.Fatalf("line %s not found in import %s", lineID, importID)
	return reconciliation.ItemView{}
}

// excOf returns the live exception on a line (or on a subject when itemID is "").
func (r *reconIT) excOf(t *testing.T, itemID, subjectID, typ string) reconciliation.Exception {
	t.Helper()
	var id string
	err := r.mig.QueryRow(ctx(t), `SELECT id::text FROM recon.exceptions WHERE exception_type = $1 AND ($2 = '' OR item_id::text = $2)
		AND ($3 = '' OR subject_id::text = $3) ORDER BY detected_at DESC LIMIT 1`, typ, itemID, subjectID).Scan(&id)
	if err != nil {
		t.Fatalf("exception %s (item %s, subject %s): %v", typ, itemID, subjectID, err)
	}
	x, err := r.rc.ExceptionByID(ctx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func statement(ref, ccy string, from, to time.Time, lines []map[string]string, batches ...map[string]string) []byte {
	doc := map[string]any{"format": reconciliation.FormatCanonicalV1, "statement": map[string]string{"ref": ref, "currency": ccy,
		"period_start": from.UTC().Format(time.RFC3339), "period_end": to.UTC().Format(time.RFC3339), "declared_lines": fmt.Sprint(len(lines))},
		"lines": lines}
	if len(batches) > 0 {
		doc["batches"] = batches
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func bankStatement(ref, ccy, account string, from, to time.Time, lines []map[string]string) []byte {
	raw := statement(ref, ccy, from, to, lines)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	doc["statement"].(map[string]any)["account_ref"] = account
	out, _ := json.Marshal(doc)
	return out
}

func pline(id, kind, status, amount, merchant, provider string, at time.Time, extra ...string) map[string]string {
	dir := "CREDIT"
	if kind != "PAYMENT" && kind != "PAYOUT_REVERSAL" {
		dir = "DEBIT"
	}
	m := map[string]string{"ref": id, "kind": kind, "direction": dir, "status": status, "amount_minor": amount, "currency": "USD",
		"booked_at": at.UTC().Format(time.RFC3339)}
	if merchant != "" {
		m["merchant_ref"] = merchant
	}
	if provider != "" {
		m["provider_ref"] = provider
	}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var _ clock.Clock = fixedClock{}

// later returns a copy of the service whose clock is d in the future (timing conditions: settlement horizon, staleness).
func (r *reconIT) later(d time.Duration) *reconciliation.Service {
	svc := *r.rc
	svc.Clock = fixedClock{time.Now().UTC().Add(d)}
	return &svc
}

func (r *reconIT) providerRef(t *testing.T, paymentID string) string {
	t.Helper()
	var ref string
	if err := r.mig.QueryRow(ctx(t), `SELECT coalesce(provider_payment_ref, '') FROM app.payment_intents WHERE id = $1`, paymentID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// ---- tests -------------------------------------------------------------------------------------------------------------

func TestReconciliationRuleSetsAreMakerChecker(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	r := newReconIT(t, s, f)
	active, _, err := r.rc.ActiveRuleSet(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	// bounds and closed keys
	bad := r.stepped(t, f.finance1, "POST", "/admin/reconciliation/rule-sets", map[string]any{"config": map[string]int{"date_window_days": 99,
		"settlement_horizon_days": 1, "stale_after_hours": 1}, "change_reason": "out of bounds on purpose"}, "Idempotency-Key", ids.New())
	if bad.Status != 422 {
		t.Fatalf("out-of-bounds rule set: %d", bad.Status)
	}
	resp := r.stepped(t, f.finance1, "POST", "/admin/reconciliation/rule-sets", map[string]any{"config": map[string]int{"date_window_days": 2,
		"settlement_horizon_days": 1, "stale_after_hours": 720}, "change_reason": "integration test: maker-checker"}, "Idempotency-Key", ids.New())
	if resp.Status != 201 {
		t.Fatalf("propose: %d %s", resp.Status, resp.Error.Code)
	}
	id := resp.field(t, "id")
	if self := r.stepped(t, f.finance1, "POST", "/admin/reconciliation/rule-sets/"+id+"/approve", map[string]string{"note": "my own proposal"}); self.Status != 403 ||
		self.Error.Code != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("self approval: %d %s", self.Status, self.Error.Code)
	}
	if _, err := r.app.Exec(ctx(t), `UPDATE recon.matching_rule_sets SET status = 'ACTIVE', decided_by = proposed_by, decided_at = now(),
		effective_from = now() WHERE id = $1`, id); err == nil {
		t.Fatal("the database accepted a self-approved rule set")
	}
	if _, err := r.app.Exec(ctx(t), `UPDATE recon.matching_rule_sets SET config = '{"date_window_days":9,"settlement_horizon_days":1,"stale_after_hours":1}'
		WHERE id = $1`, id); err == nil {
		t.Fatal("a proposed rule set's configuration changed")
	}
	if wrong := r.stepped(t, f.finance2, "POST", "/admin/reconciliation/rule-sets/"+id+"/approve", map[string]string{"note": "checked",
		"config_sha256": strings.Repeat("0", 64)}); wrong.Error.Code != "PROPOSAL_CHANGED" {
		t.Fatalf("hash mismatch: %d %s", wrong.Status, wrong.Error.Code)
	}
	ok := r.stepped(t, f.finance2, "POST", "/admin/reconciliation/rule-sets/"+id+"/approve", map[string]string{"note": "checked by a second person"})
	if ok.Status != 200 || ok.field(t, "status") != "ACTIVE" {
		t.Fatalf("approve: %d %s", ok.Status, ok.Error.Code)
	}
	if active.ID != "" {
		var st string
		_ = r.mig.QueryRow(ctx(t), `SELECT status FROM recon.matching_rule_sets WHERE id = $1`, active.ID).Scan(&st)
		if st != "RETIRED" {
			t.Fatalf("previous active set is %s", st)
		}
	}
	if n := countAudit(t, "audit.audit_events", "reconciliation.rule_set.approve", id); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	// support and reviewer roles see nothing
	for _, c := range []staffCred{f.support, f.rev1} {
		if resp := r.s.staff(t, c).do("GET", "/admin/reconciliation/overview", nil); resp.Status != 403 {
			t.Fatalf("%s sees reconciliation: %d", c.email, resp.Status)
		}
	}
}

func TestReconciliationImportsAreImmutableDeduplicatedAndValidated(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	r := newReconIT(t, s, f)
	now := time.Now().UTC()
	from, to := now.Add(-48*time.Hour), now.Add(-time.Minute)
	content := statement("IT-"+r.tok, "USD", from, to, []map[string]string{pline("L1-"+r.tok, "PAYMENT", "SUCCEEDED", "1200",
		ids.New(), "ext_"+r.tok, now.Add(-time.Hour))})
	imp := r.importOK(t, "sandbox_transactions_test", content)
	sum := sha256.Sum256(content)
	if imp.ContentSHA256 != hex.EncodeToString(sum[:]) || imp.Environment != "TEST" || imp.Label != "TEST_ONLY" {
		t.Fatalf("import %+v", imp)
	}
	var evHash []byte
	if err := r.mig.QueryRow(ctx(t), `SELECT content_sha256 FROM audit.evidence_records WHERE id = $1`, imp.EvidenceRecordID).Scan(&evHash); err != nil ||
		!bytes.Equal(evHash, sum[:]) {
		t.Fatalf("evidence record hash: %v", err)
	}
	// the same bytes again, under another filename: refused by hash
	if dup := r.upload(t, f.finance1, "sandbox_transactions_test", "application/json", "", content); dup.Status != 409 || dup.Error.Code != "DUPLICATE_IMPORT" {
		t.Fatalf("duplicate: %d %s", dup.Status, dup.Error.Code)
	}
	// lines and imports are immutable to the application role; nothing can be deleted
	it := r.line(t, imp.ID, "L1-"+r.tok)
	for _, sql := range []string{`UPDATE recon.items SET amount_minor = 1 WHERE id = $1`, `DELETE FROM recon.items WHERE id = $1`,
		`UPDATE recon.items SET merchant_ref = 'x' WHERE id = $1`} {
		if _, err := r.app.Exec(ctx(t), sql, it.ID); err == nil {
			t.Fatalf("accepted: %s", sql)
		}
	}
	if _, err := r.app.Exec(ctx(t), `UPDATE recon.imports SET content_sha256 = '\x00' WHERE id = $1`, imp.ID); err == nil {
		t.Fatal("an import's hash changed")
	}
	// malformed evidence: all or nothing, with a validation report and no lines
	bad := statement("IT-BAD-"+r.tok, "USD", from, to, []map[string]string{pline("B1-"+r.tok, "PAYMENT", "SUCCEEDED", "12.50", ids.New(), "x", now)})
	resp := r.upload(t, f.finance1, "sandbox_transactions_test", "application/json", "", bad)
	if resp.Status != 202 {
		t.Fatalf("bad upload: %d %s", resp.Status, resp.Error.Code)
	}
	failed := r.waitImport(t, resp.field(t, "id"))
	if failed.Status != "FAILED" || failed.FailureCode == nil || len(failed.ValidationReport) == 0 {
		t.Fatalf("malformed statement: %+v", failed)
	}
	var lines int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.items WHERE import_id = $1`, failed.ID).Scan(&lines)
	if lines != 0 {
		t.Fatalf("a failed import recorded %d lines", lines)
	}
	// a file that is not a statement is refused at upload (storage content rules)
	if html := r.upload(t, f.finance1, "sandbox_transactions_test", "text/csv", "", []byte("<html><script>alert(1)</script>,x\n")); html.Status < 400 {
		t.Fatalf("markup accepted: %d", html.Status)
	}
	// permissions: support cannot import; a non-staff user cannot reach the API
	if resp := r.upload(t, f.support, "sandbox_transactions_test", "application/json", "", []byte(`{}`)); resp.Status != 403 {
		t.Fatalf("support import: %d", resp.Status)
	}
	u := verifiedUser(t, s, "recon")
	if resp := u.do("GET", "/admin/reconciliation/imports", nil); resp.Status != 403 && resp.Status != 404 {
		t.Fatalf("user reads imports: %d", resp.Status)
	}
	if n := countAudit(t, "audit.audit_events", "reconciliation.import.received", imp.ID); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	// production refuses imports (code gate; sources are also CHECK-limited to TEST)
	prod := *r.rc
	prod.Production = true
	if _, err := prod.Upload(ctx(t), f.finance1.id, reconciliation.UploadInput{SourceCode: "sandbox_transactions_test", MediaType: "application/json",
		Content: []byte(`{}`)}); !errors.Is(err, reconciliation.ErrProductionImport) {
		t.Fatalf("production import: %v", err)
	}
	if _, err := r.mig.Exec(ctx(t), `INSERT INTO recon.sources (id, code, source_kind, provider_code, authority_rank, format, environment, description)
		VALUES (gen_random_uuid(), 'prod_x', 'PROVIDER_SETTLEMENT_REPORT', 'sandbox', 3, 'FUNDZIM_CANONICAL_V1', 'PRODUCTION', 'x')`); err == nil {
		t.Fatal("a PRODUCTION source was accepted")
	}
}

func TestReconciliationPaymentsEndToEnd(t *testing.T) {
	p := newPayIT(t)
	r := newReconIT(t, p.s, p.f)
	// three sandbox payments: two succeed, one ends UNKNOWN (the simulator never created it)
	succeed := func(amount string) string {
		id := p.intent(t, p.donor, amount)
		a := p.attempt(t, p.donor, id, "ECOCASH", 201, "")
		p.waitStatus(t, a.ID, "SUCCEEDED", 40*time.Second)
		return a.ID
	}
	pay1, pay2 := succeed("1500"), succeed("2500")
	unkIntent := p.intent(t, p.donor, "1705")
	unknown := p.attempt(t, p.donor, unkIntent, "ECOCASH", 201, "")
	if unknown.Status != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN, got %s", unknown.Status)
	}
	ref1 := r.providerRef(t, pay1)
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	lines := []map[string]string{
		pline("P1-"+r.tok, "PAYMENT", "SUCCEEDED", "1500", pay1, ref1, at),
		pline("P2-"+r.tok, "PAYMENT", "SUCCEEDED", "2499", pay2, "", at),                 // amount differs
		pline("P3-"+r.tok, "PAYMENT", "SUCCEEDED", "1705", unknown.ID, "stm_"+r.tok, at), // statement says success for an UNKNOWN payment
		pline("P4-"+r.tok, "PAYMENT", "SUCCEEDED", "1500", "", "nobody_"+r.tok, at),      // same amount as P1 but no reference to it
		pline("P5-"+r.tok, "PAYMENT", "SUCCEEDED", "1500", pay1, ref1, at),               // a second line for P1
	}
	imp := r.importOK(t, "sandbox_transactions_test", statement("IT-PAY-"+r.tok, "USD", now.Add(-2*time.Hour), now, lines))
	run := r.run(t, r.rc, "sandbox_transactions_test", "USD")
	if run.RuleSetVersion == nil || run.MatcherVersion != reconciliation.MatcherVersion || run.DecisionsSHA256 == nil {
		t.Fatalf("run provenance: %+v", run)
	}
	l1 := r.line(t, imp.ID, "P1-"+r.tok)
	if l1.MatchStatus != "MATCHED" || l1.Match == nil || l1.Match.Confidence != "EXACT_MATCH" || l1.Match.LedgerStatus != "NOT_POSTED_TEST_PAYMENT" ||
		l1.Match.SubjectID != pay1 {
		t.Fatalf("P1: %+v %+v", l1, l1.Match)
	}
	if e := r.excOf(t, r.line(t, imp.ID, "P2-"+r.tok).ID, "", "AMOUNT_MISMATCH"); e.Severity != "HIGH" || *e.ExpectedAmount != "2500" || *e.ActualAmount != "2499" {
		t.Fatalf("P2: %+v", e)
	}
	if e := r.excOf(t, r.line(t, imp.ID, "P4-"+r.tok).ID, "", "MISSING_PAYMENT"); e.SubjectID != nil {
		t.Fatalf("P4 was matched by amount: %+v", e)
	}
	if e := r.excOf(t, r.line(t, imp.ID, "P5-"+r.tok).ID, "", "DUPLICATE_TRANSACTION"); e.Status != "OPEN" {
		t.Fatalf("P5: %+v", e)
	}
	unk := r.excOf(t, r.line(t, imp.ID, "P3-"+r.tok).ID, unknown.ID, "UNKNOWN_PAYMENT_OUTCOME")
	if p.status(t, unknown.ID) != "UNKNOWN" {
		t.Fatal("statement evidence changed a payment without a decision")
	}

	// the UNKNOWN outcome is resolved only by an approved, evidenced resolution applied by payments (source RECON)
	path := "/admin/reconciliation/exceptions/" + unk.ID
	b1 := r.s.staff(t, r.f.finance1)
	b1.expect(b1.do("POST", path+"/assign", map[string]any{"assignee_id": r.f.finance1.id, "version": unk.Version}), 200, "")
	b1.expect(b1.do("POST", path+"/start", map[string]any{"note": "checking the statement line"}), 200, "")
	prop := b1.do("POST", path+"/resolutions", map[string]any{"resolution_type": "PROVIDER_OUTCOME_APPLIED",
		"justification": "the TEST statement line reports the payment as succeeded at the provider", "evidence_refs": []string{"import:" + imp.ID}},
		"Idempotency-Key", ids.New())
	b1.expect(prop, 201, "")
	resID, hash := prop.field(t, "id"), prop.field(t, "proposal_sha256")
	if self := r.stepped(t, r.f.finance1, "POST", "/admin/reconciliation/resolutions/"+resID+"/approve", map[string]string{"note": "my own proposal",
		"proposal_sha256": hash}); self.Error.Code != "SELF_DECISION_FORBIDDEN" {
		t.Fatalf("self approval: %d %s", self.Status, self.Error.Code)
	}
	if wrong := r.stepped(t, r.f.finance2, "POST", "/admin/reconciliation/resolutions/"+resID+"/approve", map[string]string{"note": "checked",
		"proposal_sha256": strings.Repeat("a", 64)}); wrong.Error.Code != "PROPOSAL_CHANGED" {
		t.Fatalf("stale proposal: %d %s", wrong.Status, wrong.Error.Code)
	}
	ok := r.stepped(t, r.f.finance2, "POST", "/admin/reconciliation/resolutions/"+resID+"/approve", map[string]string{"note": "evidence checked",
		"proposal_sha256": hash})
	if ok.Status != 200 || ok.field(t, "status") != "EXECUTED" {
		t.Fatalf("approve: %d %s %s", ok.Status, ok.Error.Code, ok.Data)
	}
	if st := p.status(t, unknown.ID); st != "SUCCEEDED" {
		t.Fatalf("payment after the approved resolution: %s", st)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND source = 'RECON' AND applied AND to_status = 'SUCCEEDED'`,
		unknown.ID); n != 1 {
		t.Fatalf("RECON events: %d", n)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE payment_intent_id = $1 AND source = 'RECON'`, unknown.ID); n != 1 {
		t.Fatalf("RECON evidence: %d", n)
	}
	if x, _ := r.rc.ExceptionByID(ctx(t), unk.ID); x.Status != "RESOLVED" {
		t.Fatalf("exception after execution: %s", x.Status)
	}
	// executing again (worker retry) is a no-op; the same evidence key is never applied twice
	if _, err := r.rc.ExecuteApproved(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND source = 'RECON' AND applied`, unknown.ID); n != 1 {
		t.Fatalf("RECON applied twice: %d", n)
	}

	// a replay of the run decides the same lines the same way: no duplicate match, no duplicate case, the payment's line now matches
	replay, err := r.rc.RequestRun(ctx(t), r.f.finance1.id, reconciliation.RunInput{ReplayOfRunID: run.ID, Reason: "integration replay"})
	if err != nil && !errors.Is(err, reconciliation.ErrRunActive) {
		t.Fatal(err)
	}
	if err == nil {
		_ = r.rc.ExecuteRun(ctx(t), replay.ID)
	}
	r.run(t, r.rc, "sandbox_transactions_test", "USD")
	if l3 := r.line(t, imp.ID, "P3-"+r.tok); l3.MatchStatus != "MATCHED" || l3.Match == nil || l3.Match.SubjectID != unknown.ID {
		t.Fatalf("P3 after resolution: %+v", l3)
	}
	var matches, cases int
	_ = r.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM recon.matches m JOIN recon.items x ON x.id = m.item_id WHERE x.import_id = $1 AND m.status = 'ACTIVE'),
		(SELECT count(*) FROM recon.exceptions e JOIN recon.items x ON x.id = e.item_id WHERE x.import_id = $1)`, imp.ID).Scan(&matches, &cases)
	if matches != 2 || cases != 4 {
		t.Fatalf("after replays: %d matches, %d cases", matches, cases)
	}
	var occ int
	_ = r.mig.QueryRow(ctx(t), `SELECT occurrences FROM recon.exceptions WHERE item_id = $1 AND exception_type = 'AMOUNT_MISMATCH'`,
		r.line(t, imp.ID, "P2-"+r.tok).ID).Scan(&occ)
	if occ < 2 {
		t.Fatalf("a re-seen problem must bump occurrences, got %d", occ)
	}

	// the simulator's own view of the window, as a statement: its lines carry our references and match
	gen := r.stepped(t, r.f.finance1, "POST", "/admin/reconciliation/imports/sandbox", map[string]string{"source": "sandbox_transactions_test",
		"currency": "USD", "period_start": now.Add(-10 * time.Minute).Format(time.RFC3339), "period_end": time.Now().UTC().Format(time.RFC3339)},
		"Idempotency-Key", ids.New())
	if gen.Status != 202 {
		t.Fatalf("sandbox statement: %d %s", gen.Status, gen.Error.Code)
	}
	if g := r.waitImport(t, gen.field(t, "id")); g.Status != "PARSED" || g.Origin != "SANDBOX_GENERATOR" {
		t.Fatalf("sandbox import: %+v", g)
	}
	// a line that adds nothing new about a matched payment is a duplicate (one match per payment per kind and source)
	r.run(t, r.rc, "sandbox_transactions_test", "USD")
	var gm int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.matches m JOIN recon.items x ON x.id = m.item_id WHERE x.import_id = $1 AND m.subject_id = $2
		AND m.status = 'ACTIVE'`, gen.field(t, "id"), pay2).Scan(&gm)
	if gm != 1 {
		t.Fatalf("simulator line for pay2 not matched: %d", gm)
	}

	// a statement covering the period must contain every succeeded payment once the horizon passed (a future clock)
	later := r.later(60 * 24 * time.Hour)
	r.run(t, later, "sandbox_transactions_test", "USD")
	var missing int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.exceptions WHERE exception_type = 'MISSING_SETTLEMENT' AND subject_id = $1`, pay1).Scan(&missing)
	if missing != 0 {
		t.Fatal("a matched payment was reported missing")
	}
}

func TestReconciliationSettlementBatchesAndExplanations(t *testing.T) {
	p := newPayIT(t)
	r := newReconIT(t, p.s, p.f)
	id := p.intent(t, p.donor, "3000")
	a := p.attempt(t, p.donor, id, "ECOCASH", 201, "")
	p.waitStatus(t, a.ID, "SUCCEEDED", 40*time.Second)
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	batch := "BATCH-" + r.tok
	lines := []map[string]string{
		pline("S1-"+r.tok, "PAYMENT", "SUCCEEDED", "3000", a.ID, r.providerRef(t, a.ID), at, "batch_ref", batch, "fee_minor", "90", "net_minor", "2910"),
		pline("S2-"+r.tok, "FEE", "BOOKED", "15", "", "", at, "batch_ref", batch), // a provider charge naming no record
		pline("S3-"+r.tok, "FEE_REMITTANCE", "BOOKED", "40", "", "rem_"+r.tok, at),
	}
	hdr := map[string]string{"ref": batch, "currency": "USD", "value_date": now.Format("2006-01-02"), "gross_credits_minor": "3000",
		"gross_debits_minor": "15", "fees_minor": "90", "net_minor": "2895", "declared_lines": "2"}
	// arithmetic must hold before anything is recorded
	broken := map[string]string{}
	for k, v := range hdr {
		broken[k] = v
	}
	broken["net_minor"] = "2900"
	resp := r.upload(t, r.f.finance1, "sandbox_settlement_test", "application/json", "", statement("IT-SET-BAD-"+r.tok, "USD", now.Add(-time.Hour), now,
		lines, broken))
	if bad := r.waitImport(t, resp.field(t, "id")); bad.Status != "FAILED" || *bad.FailureCode != "SETTLEMENT_ARITHMETIC" {
		t.Fatalf("broken settlement: %+v", bad)
	}
	imp := r.importOK(t, "sandbox_settlement_test", statement("IT-SET-"+r.tok, "USD", now.Add(-time.Hour), now, lines, hdr))
	r.run(t, r.rc, "sandbox_settlement_test", "USD")
	s1 := r.line(t, imp.ID, "S1-"+r.tok)
	if s1.Match == nil || (s1.Match.FeeStatus != "MATCHED" && s1.Match.FeeStatus != "MISMATCH" && s1.Match.FeeStatus != "NO_EXPECTATION") {
		t.Fatalf("S1: %+v", s1.Match)
	}
	adj := r.excOf(t, r.line(t, imp.ID, "S2-"+r.tok).ID, "", "PROVIDER_ADJUSTMENT")
	var batchID, status string
	if err := r.mig.QueryRow(ctx(t), `SELECT id::text, status FROM recon.settlement_batches WHERE import_id = $1`, imp.ID).Scan(&batchID, &status); err != nil {
		t.Fatal(err)
	}
	if status != "DISCREPANCY" {
		t.Fatalf("batch with an unexplained line: %s", status)
	}
	// explained by maker and checker: the line is IGNORED and no longer holds the batch
	path := "/admin/reconciliation/exceptions/" + adj.ID
	b1 := r.s.staff(t, r.f.finance1)
	b1.expect(b1.do("POST", path+"/assign", map[string]any{"assignee_id": r.f.finance1.id}), 200, "")
	b1.expect(b1.do("POST", path+"/start", map[string]any{"note": "provider charge"}), 200, "")
	prop := b1.do("POST", path+"/resolutions", map[string]any{"resolution_type": "EXPLAINED_NO_ACTION",
		"justification": "a TEST provider charge on the batch, explained by the TEST statement itself", "evidence_refs": []string{"batch:" + batchID}},
		"Idempotency-Key", ids.New())
	b1.expect(prop, 201, "")
	ok := r.stepped(t, r.f.finance2, "POST", "/admin/reconciliation/resolutions/"+prop.field(t, "id")+"/approve",
		map[string]string{"note": "agreed", "proposal_sha256": prop.field(t, "proposal_sha256")})
	if ok.Status != 200 {
		t.Fatalf("approve: %d %s", ok.Status, ok.Error.Code)
	}
	if st := r.line(t, imp.ID, "S2-"+r.tok).MatchStatus; st != "IGNORED" {
		t.Fatalf("explained line: %s", st)
	}
	r.run(t, r.rc, "sandbox_settlement_test", "USD")
	b, err := r.rc.Settlement(ctx(t), batchID)
	if err != nil {
		t.Fatal(err)
	}
	if b.LinesSettled != 2 || b.ExpectedNet == nil {
		t.Fatalf("batch after explanation: %+v", b)
	}
	// MATCHED when FundZim's expectation equals the header; otherwise SHORT/OVER with the signed values in detail
	if b.Status == "DISCREPANCY" {
		var typ string
		_ = r.mig.QueryRow(ctx(t), `SELECT exception_type FROM recon.exceptions WHERE settlement_batch_id = $1`, batchID).Scan(&typ)
		if typ != "SETTLEMENT_SHORT" && typ != "SETTLEMENT_OVER" {
			t.Fatalf("batch discrepancy without a case: %s", typ)
		}
	}
	if _, err := r.app.Exec(ctx(t), `UPDATE recon.settlement_batches SET net_minor = net_minor + 1 WHERE id = $1`, batchID); err == nil {
		t.Fatal("a settlement header amount changed")
	}
}

func TestReconciliationPayoutsUnknownOutcomeNeverResubmitted(t *testing.T) {
	po := newPayoutIT(t)
	r := newReconIT(t, po.s, po.f)
	camp := po.campaign(t, 20_000)
	done := po.requestDirect(t, camp, 1_000) // simulator SUCCESS
	po.approve(t, done)
	if _, err := po.po.SubmitNow(ctx(t), done); err != nil {
		t.Fatal(err)
	}
	completed := po.drive(t, 40*time.Second, "completed", status(payouts.StCompleted), done)
	po.journal(t, "payout:"+done+":completed")
	unk := po.requestDirect(t, camp, 1_004) // simulator UNKNOWN (never created)
	po.approve(t, unk)
	if _, err := po.po.SubmitNow(ctx(t), unk); err != nil {
		t.Fatal(err)
	}
	unknown := po.drive(t, 30*time.Second, "unknown", status(payouts.StUnknown), unk)
	attemptsBefore := po.createAttempts(t, unk)
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	lines := []map[string]string{
		pline("O1-"+r.tok, "PAYOUT", "COMPLETED", "1000", done, *completed.ProviderPayoutRef, at),
		pline("O2-"+r.tok, "PAYOUT", "COMPLETED", "1004", unk, "po_stm_"+r.tok, at),
		pline("O3-"+r.tok, "PAYOUT", "COMPLETED", "777", "", "po_nobody_"+r.tok, at), // money left for a payout FundZim never made
	}
	imp := r.importOK(t, "sandbox_transactions_test", statement("IT-PO-"+r.tok, "USD", now.Add(-time.Hour), now, lines))
	r.run(t, r.rc, "sandbox_transactions_test", "USD")
	o1 := r.line(t, imp.ID, "O1-"+r.tok)
	if o1.Match == nil || o1.Match.LedgerStatus != "POSTED" || o1.Match.LedgerTransactionID == nil {
		t.Fatalf("completed payout: %+v", o1.Match)
	}
	if e := r.excOf(t, r.line(t, imp.ID, "O3-"+r.tok).ID, "", "MISSING_PAYOUT"); e.Severity != "CRITICAL" {
		t.Fatalf("unknown debit: %+v", e)
	}
	e := r.excOf(t, r.line(t, imp.ID, "O2-"+r.tok).ID, unk, "UNKNOWN_PAYOUT_OUTCOME")
	if e.Severity != "CRITICAL" {
		t.Fatalf("unknown payout case: %+v", e)
	}
	if got := po.payout(t, unk); got.Status != payouts.StUnknown || po.createAttempts(t, unk) != attemptsBefore {
		t.Fatalf("statement evidence changed the payout or resubmitted it: %s, attempts %d", got.Status, po.createAttempts(t, unk))
	}
	// approved resolution: payouts' own machine completes it (source RECON); the reservation follows Y10; nothing is resubmitted
	path := "/admin/reconciliation/exceptions/" + e.ID
	b1 := r.s.staff(t, r.f.finance1)
	b1.expect(b1.do("POST", path+"/assign", map[string]any{"assignee_id": r.f.finance1.id}), 200, "")
	b1.expect(b1.do("POST", path+"/start", map[string]any{"note": "statement shows completion"}), 200, "")
	prop := b1.do("POST", path+"/resolutions", map[string]any{"resolution_type": "PROVIDER_OUTCOME_APPLIED",
		"justification": "the TEST statement reports the payout as completed by the provider", "evidence_refs": []string{"import:" + imp.ID}},
		"Idempotency-Key", ids.New())
	b1.expect(prop, 201, "")
	ok := r.stepped(t, r.f.finance2, "POST", "/admin/reconciliation/resolutions/"+prop.field(t, "id")+"/approve",
		map[string]string{"note": "statement checked", "proposal_sha256": prop.field(t, "proposal_sha256")})
	if ok.Status != 200 || ok.field(t, "status") != "EXECUTED" {
		t.Fatalf("approve: %d %s", ok.Status, ok.Error.Code)
	}
	after := po.payout(t, unk)
	if after.Status != payouts.StCompleted || po.createAttempts(t, unk) != attemptsBefore {
		t.Fatalf("after resolution: %s, attempts %d (before %d)", after.Status, po.createAttempts(t, unk), attemptsBefore)
	}
	var recon int
	_ = po.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_events WHERE payout_request_id = $1 AND source = 'RECON' AND to_status = 'COMPLETED'`, unk).Scan(&recon)
	if recon != 1 || after.Reservation == nil || after.Reservation.Status != "CONSUMED" {
		t.Fatalf("RECON completion events %d, reservation %+v", recon, after.Reservation)
	}
	po.journal(t, "payout:"+unk+":completed")
	po.integrity(t)
	_ = unknown
}

func TestReconciliationRateLimitedPayoutIsBudgeted(t *testing.T) {
	po := newPayoutIT(t)
	po.po.SubmitRetryBase, po.po.SubmitRetryMax = time.Millisecond, 5*time.Millisecond
	camp := po.campaign(t, 5_000)
	id := po.requestDirect(t, camp, 1_013) // simulator RATE_LIMITED (KI-S17-01)
	po.approve(t, id)
	if _, err := po.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	exc := po.dispatchUntilHalted(t, po.po, id)
	if n := po.createAttempts(t, id); n != payouts.DefaultSubmitRetryBudget {
		t.Fatalf("CREATE_PAYOUT attempts %d, want exactly the budget %d", n, payouts.DefaultSubmitRetryBudget)
	}
	var other, status int
	_ = po.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE operation = 'CREATE_PAYOUT' AND classification <> 'RATE_LIMITED'),
		count(*) FILTER (WHERE operation = 'GET_STATUS') FROM app.payout_attempts WHERE payout_request_id = $1`, id).Scan(&other, &status)
	if other != 0 || status != 0 {
		t.Fatalf("rate-limited payout: %d non-rate-limited creates, %d status queries (refused requests are not polled)", other, status)
	}
	if got := po.payout(t, id); got.Status != payouts.StSubmitted || got.Reservation.LedgerReleaseTxnID != nil {
		t.Fatalf("halting changed the payout: %+v", got)
	}
	// halted: no more attempts, the database refuses one
	for i := 0; i < 3; i++ {
		po.makeDue(t, id)
		_, _ = po.po.DispatchDue(ctx(t), 50)
		_, _ = po.po.PollDue(ctx(t), 50)
	}
	if n := po.createAttempts(t, id); n != payouts.DefaultSubmitRetryBudget {
		t.Fatalf("attempts after halting: %d", n)
	}
	_ = exc
}

func TestReconciliationBankStatementsAndLedgerAdjustment(t *testing.T) {
	p := newPayIT(t)
	r := newReconIT(t, p.s, p.f)
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	rem := "REM-" + r.tok
	// a settlement report with a fee remittance to FundZim's own account
	r.importOK(t, "sandbox_settlement_test", statement("IT-REM-"+r.tok, "USD", now.Add(-time.Hour), now, []map[string]string{
		pline("R1-"+r.tok, "FEE_REMITTANCE", "BOOKED", "640", "", rem, at)}))
	id := p.intent(t, p.donor, "1100")
	a := p.attempt(t, p.donor, id, "ECOCASH", 201, "")
	p.waitStatus(t, a.ID, "SUCCEEDED", 40*time.Second)
	bank := func(lid, dir, amount, ref string) map[string]string {
		return map[string]string{"ref": lid, "kind": "BANK_TRANSFER", "direction": dir, "status": "BOOKED", "amount_minor": amount, "currency": "USD",
			"counterparty_ref": ref, "booked_at": at.Format(time.RFC3339)}
	}
	imp := r.importOK(t, "fundzim_operating_bank_test", bankStatement("IT-BANK-"+r.tok, "USD", "ZW00 1234 5678 9012 "+r.tok[:4], now.Add(-time.Hour), now,
		[]map[string]string{bank("K1-"+r.tok, "CREDIT", "640", "FEE REMITTANCE "+rem), bank("K2-"+r.tok, "CREDIT", "1100", "DONATION "+a.ID),
			bank("K3-"+r.tok, "DEBIT", "35", "SERVICE CHARGE "+r.tok)}))
	if imp.AccountRefHint == nil || len(*imp.AccountRefHint) != 4 {
		t.Fatalf("account reference: %+v", imp.AccountRefHint)
	}
	var stored int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.imports WHERE id = $1 AND account_ref_hmac IS NOT NULL
		AND position('5678' in coalesce(account_ref_hint, '')) = 0`, imp.ID).Scan(&stored)
	if stored != 1 {
		t.Fatal("the bank account number must not be stored")
	}
	r.run(t, r.rc, "fundzim_operating_bank_test", "USD")
	if k1 := r.line(t, imp.ID, "K1-"+r.tok); k1.Match == nil || k1.Match.SubjectType != "FEE_REMITTANCE_LINE" {
		t.Fatalf("remittance not matched: %+v", k1)
	}
	if e := r.excOf(t, r.line(t, imp.ID, "K2-"+r.tok).ID, "", "POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT"); e.Severity != "CRITICAL" {
		t.Fatalf("Model A alarm: %+v", e)
	}
	k3 := r.excOf(t, r.line(t, imp.ID, "K3-"+r.tok).ID, "", "UNMATCHED_BANK_ENTRY")
	if k3.Severity != "CRITICAL" {
		t.Fatalf("unknown debit: %+v", k3)
	}

	// a ledger correction for the unexplained charge: the ledger's own maker-checker posts a balanced journal (TEST ledger)
	var sbx string // sandbox provider id (seeded)
	_ = r.mig.QueryRow(ctx(t), `SELECT id::text FROM app.payment_providers WHERE code = 'sandbox'`).Scan(&sbx)
	path := "/admin/reconciliation/exceptions/" + k3.ID
	b1 := r.s.staff(t, r.f.finance1)
	b1.expect(b1.do("POST", path+"/assign", map[string]any{"assignee_id": r.f.finance1.id}), 200, "")
	b1.expect(b1.do("POST", path+"/start", map[string]any{"note": "bank charge"}), 200, "")
	prop := b1.do("POST", path+"/resolutions", map[string]any{"resolution_type": "LEDGER_ADJUSTMENT",
		"justification": "TEST adjustment exercising the ledger's maker-checker path from reconciliation", "evidence_refs": []string{"import:" + imp.ID},
		"posting_rule": "SETTLEMENT_DISCREPANCY_RESOLVED", "lines": []map[string]string{
			{"account_code": "suspense:settlement_discrepancy:" + sbx, "direction": "DEBIT", "amount_minor": "35"},
			{"account_code": "asset:psp_settled:" + sbx, "direction": "CREDIT", "amount_minor": "35"}}}, "Idempotency-Key", ids.New())
	b1.expect(prop, 201, "")
	var adjID string
	var resp map[string]any
	_ = json.Unmarshal(prop.Data, &resp)
	adjID, _ = resp["ledger_adjustment_id"].(string)
	if adjID == "" {
		t.Fatalf("no ledger adjustment request: %s", prop.Data)
	}
	ok := r.stepped(t, r.f.finance2, "POST", "/admin/reconciliation/resolutions/"+prop.field(t, "id")+"/approve",
		map[string]string{"note": "lines checked", "proposal_sha256": prop.field(t, "proposal_sha256")})
	if ok.Status != 200 || ok.field(t, "status") != "EXECUTED" {
		t.Fatalf("approve: %d %s %s", ok.Status, ok.Error.Code, ok.Data)
	}
	adj, err := p.s.d.Verification.Ledger.AdjustmentByID(ctx(t), adjID)
	if err != nil || adj.Status != "POSTED" || adj.ResultingTx == nil || *adj.ApprovedBy != r.f.finance2.id {
		t.Fatalf("ledger adjustment: %+v %v", adj, err)
	}
	report, err := p.s.d.Verification.Ledger.TrialBalance(ctx(t), "USD", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Currencies {
		if c.Difference != "0" {
			t.Fatalf("trial balance %s off by %s", c.Currency, c.Difference)
		}
	}
}

func TestReconciliationSupersedeAndConflictingEvidence(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	r := newReconIT(t, s, f)
	now := time.Now().UTC()
	at := now.Add(-time.Minute)
	l := func(amount string) []map[string]string {
		return []map[string]string{pline("C1-"+r.tok, "PAYMENT", "SUCCEEDED", amount, "", "cref_"+r.tok, at)}
	}
	first := r.importOK(t, "sandbox_transactions_test", statement("IT-C-1-"+r.tok, "USD", now.Add(-time.Hour), now, l("100")))
	// the same line with other content, without a re-issue: kept as a CONFLICT revision with a case, never applied
	second := r.importOK(t, "sandbox_transactions_test", statement("IT-C-2-"+r.tok, "USD", now.Add(-time.Hour), now, l("200")))
	if second.LinesConflicting != 1 {
		t.Fatalf("conflicting lines: %d", second.LinesConflicting)
	}
	c2 := r.line(t, second.ID, "C1-"+r.tok)
	if c2.MatchStatus != "CONFLICT" || c2.Revision != 2 {
		t.Fatalf("conflict line: %+v", c2)
	}
	r.excOf(t, c2.ID, "", "CONFLICTING_EVIDENCE")
	// an identical overlapping statement: the line is recorded once
	third := r.importOK(t, "sandbox_transactions_test", statement("IT-C-3-"+r.tok, "USD", now.Add(-2*time.Hour), now, l("100")))
	if third.LinesDuplicate != 1 || third.LinesParsed != 0 {
		t.Fatalf("overlap: %+v", third)
	}
	// a corrected re-issue supersedes the first statement explicitly: old lines SUPERSEDED, new revision recorded
	resp := r.upload(t, f.finance1, "sandbox_transactions_test", "application/json", first.ID,
		statement("IT-C-1R-"+r.tok, "USD", now.Add(-time.Hour), now, l("150")))
	if resp.Status != 202 {
		t.Fatalf("re-issue: %d %s", resp.Status, resp.Error.Code)
	}
	reissue := r.waitImport(t, resp.field(t, "id"))
	if reissue.Status != "PARSED" {
		t.Fatalf("re-issue: %+v", reissue)
	}
	old, _ := r.rc.ImportByID(ctx(t), first.ID)
	if old.Status != "SUPERSEDED" || r.line(t, first.ID, "C1-"+r.tok).MatchStatus != "SUPERSEDED" {
		t.Fatalf("superseded: %s", old.Status)
	}
	if nl := r.line(t, reissue.ID, "C1-"+r.tok); nl.Revision != 3 || nl.AmountMinor != "150" {
		t.Fatalf("re-issued line: %+v", nl)
	}
	// a second re-issue of the same statement is refused (supersede once)
	again := r.upload(t, f.finance1, "sandbox_transactions_test", "application/json", first.ID,
		statement("IT-C-1RR-"+r.tok, "USD", now.Add(-time.Hour), now, l("151")))
	if again.Status != 422 {
		t.Fatalf("superseding a SUPERSEDED import: %d %s", again.Status, again.Error.Code)
	}
}

func TestReconciliationConcurrency(t *testing.T) {
	p := newPayIT(t)
	r := newReconIT(t, p.s, p.f)
	now := time.Now().UTC()
	content := statement("IT-CC-"+r.tok, "USD", now.Add(-time.Hour), now, []map[string]string{pline("CC1-"+r.tok, "PAYMENT", "SUCCEEDED", "999",
		"", "cc_"+r.tok, now.Add(-time.Minute))})
	// the same file uploaded concurrently: exactly one import
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = r.upload(t, r.f.finance1, "sandbox_transactions_test", "application/json", "", content).Status
		}(i)
	}
	wg.Wait()
	sum := sha256.Sum256(content)
	var n int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.imports WHERE content_sha256 = $1`, sum[:]).Scan(&n)
	if n != 1 {
		t.Fatalf("concurrent identical uploads made %d imports (%v)", n, statuses)
	}
	var impID string
	_ = r.mig.QueryRow(ctx(t), `SELECT id::text FROM recon.imports WHERE content_sha256 = $1`, sum[:]).Scan(&impID)
	// concurrent parsers record the lines once
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.rc.ParseImport(ctx(t), impID) }()
	}
	wg.Wait()
	imp := r.waitImport(t, impID)
	var lines int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.items WHERE import_id = $1`, imp.ID).Scan(&lines)
	if lines != 1 {
		t.Fatalf("lines %d", lines)
	}
	// concurrent run requests: one active run per scope
	var created, refused int
	var mu sync.Mutex
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.rc.RequestRun(ctx(t), r.f.finance1.id, reconciliation.RunInput{Source: "sandbox_transactions_test", Currency: "USD",
				Reason: "concurrency test"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case errors.Is(err, reconciliation.ErrRunActive):
				refused++
			default:
				var e *errs.Error
				if !errors.As(err, &e) {
					t.Errorf("run request: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if created > 1 {
		t.Fatalf("%d concurrent runs queued for one scope", created)
	}
	var active int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.runs r JOIN recon.sources s ON s.id = r.source_id WHERE s.code = 'sandbox_transactions_test'
		AND r.currency = 'USD' AND r.status IN ('QUEUED','RUNNING')`).Scan(&active)
	if active > 1 {
		t.Fatalf("%d active runs", active)
	}
	// concurrent executors: the run is claimed once
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.rc.ExecuteQueued(ctx(t), 1) }()
	}
	wg.Wait()
	var dupMatches int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM (SELECT item_id FROM recon.matches WHERE status = 'ACTIVE' GROUP BY item_id HAVING count(*) > 1) x`).Scan(&dupMatches)
	if dupMatches != 0 {
		t.Fatalf("%d lines with more than one active match", dupMatches)
	}
	// two checkers racing to approve the same resolution: executed once
	e := r.excOf(t, r.line(t, imp.ID, "CC1-"+r.tok).ID, "", "MISSING_PAYMENT")
	path := "/admin/reconciliation/exceptions/" + e.ID
	b1 := r.s.staff(t, r.f.finance1)
	b1.expect(b1.do("POST", path+"/assign", map[string]any{"assignee_id": r.f.finance1.id}), 200, "")
	b1.expect(b1.do("POST", path+"/start", map[string]any{"note": "unknown funds"}), 200, "")
	prop := b1.do("POST", path+"/resolutions", map[string]any{"resolution_type": "ESCALATED_TO_CASE", "case_ref": "OPS-" + r.tok,
		"justification": "unknown TEST funds escalated to an operations case for follow-up", "evidence_refs": []string{"item:" + *e.ItemID}},
		"Idempotency-Key", ids.New())
	b1.expect(prop, 201, "")
	results := make([]int, 2)
	for i, c := range []staffCred{r.f.finance2, r.f.finance3} {
		wg.Add(1)
		go func(i int, c staffCred) {
			defer wg.Done()
			results[i] = r.stepped(t, c, "POST", "/admin/reconciliation/resolutions/"+prop.field(t, "id")+"/approve",
				map[string]string{"note": "agreed", "proposal_sha256": prop.field(t, "proposal_sha256")}).Status
		}(i, c)
	}
	wg.Wait()
	if !((results[0] == 200) != (results[1] == 200)) {
		t.Fatalf("racing approvals: %v", results)
	}
	var executed int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.exception_events WHERE exception_id = $1 AND event_type = 'RESOLUTION_EXECUTED'`, e.ID).Scan(&executed)
	if executed != 1 {
		t.Fatalf("executed %d times", executed)
	}
}

func TestReconciliationReportsAndOperations(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	r := newReconIT(t, s, f)
	b := r.s.staff(t, f.compliance)
	ov := b.do("GET", "/admin/reconciliation/overview", nil)
	b.expect(ov, 200, "")
	var o reconciliation.Overview
	_ = json.Unmarshal(ov.Data, &o)
	if o.Nature != "TEST_ONLY" || o.Production["imports"] != "BLOCKED" || o.Production["settlement_posting"] != "BLOCKED" {
		t.Fatalf("overview gates: %+v", o.Production)
	}
	for _, kind := range reconciliation.ReportKinds {
		resp := b.do("GET", "/admin/reconciliation/reports?kind="+kind, nil)
		b.expect(resp, 200, "")
	}
	// export: formula-like cells neutralised, attachment, audited (step-up)
	now := time.Now().UTC()
	r.importOK(t, "sandbox_transactions_test", statement("IT-RPT-"+r.tok, "USD", now.Add(-time.Hour), now, []map[string]string{
		pline("RPT1-"+r.tok, "PAYMENT", "SUCCEEDED", "4321", "", "rpt_"+r.tok, now.Add(-time.Minute))}))
	r.run(t, r.rc, "sandbox_transactions_test", "USD")
	get := func() *http.Response {
		req, _ := http.NewRequest("GET", s.srv.URL+"/api/v1/admin/reconciliation/reports/export?kind=unmatched_lines", nil)
		req.Header.Set("X-Forwarded-For", b.ip)
		resp, err := b.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get()
	if resp.StatusCode == 403 {
		resp.Body.Close()
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, f.compliance.secret)}), 200, "")
		resp = get()
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %s", resp.StatusCode, resp.Header)
	}
	// line references can never start with a formula character (parser); every exported cell is neutralised anyway
	cells, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil || len(cells) < 2 || cells[0][0] != "nature" || !strings.Contains(string(body), "RPT1-"+r.tok) {
		t.Fatalf("export content: %v\n%s", err, body)
	}
	for _, row := range cells {
		for _, c := range row {
			if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
				t.Fatalf("a formula-prefixed cell was exported raw: %q", c)
			}
		}
	}
	var audited int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM audit.audit_events WHERE action = 'reconciliation.report.exported' AND actor_id = $1`, f.compliance.id).Scan(&audited)
	if audited == 0 {
		t.Fatal("export not audited")
	}
	// the operations portal shows reconciliation as TEST data with its production gate BLOCKED
	snap, err := r.rc.OpsSnapshot(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	gate := false
	for _, m := range snap.Metrics {
		if m.Key == "reconciliation.production" && m.Status == "BLOCKED" {
			gate = true
		}
		if m.Value != nil && m.Nature != "TEST_ONLY" && m.Nature != "REAL" {
			t.Fatalf("metric %s nature %s", m.Key, m.Nature)
		}
	}
	if !gate {
		t.Fatal("production gate not reported as BLOCKED")
	}
	// staleness: a used source without a new statement inside stale_after_hours opens one case, and only one
	later := r.later(4000 * time.Hour)
	if _, err := later.Ageing(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := later.Ageing(ctx(t)); err != nil {
		t.Fatal(err)
	}
	var stale int
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.exceptions e JOIN recon.sources s ON s.id = e.source_id WHERE e.exception_type = 'STALE_RECONCILIATION'
		AND s.code = 'sandbox_transactions_test' AND e.currency = 'USD' AND e.status <> 'CLOSED'`).Scan(&stale)
	if stale != 1 {
		t.Fatalf("stale cases: %d", stale)
	}
	// a fresh statement clears it
	if _, err := r.rc.Ageing(ctx(t)); err != nil {
		t.Fatal(err)
	}
	_ = r.mig.QueryRow(ctx(t), `SELECT count(*) FROM recon.exceptions e JOIN recon.sources s ON s.id = e.source_id WHERE e.exception_type = 'STALE_RECONCILIATION'
		AND s.code = 'sandbox_transactions_test' AND e.currency = 'USD' AND e.status <> 'CLOSED'`).Scan(&stale)
	if stale != 0 {
		t.Fatalf("stale case not closed after a fresh statement: %d", stale)
	}
}
