//go:build integration

package integration

import (
	"context"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// Remediation phase 3 (F-R2-01, F-R2-02, F-R2-03). Each test runs its detector in a scope of its own (never the
// worker's "default" checkpoints) with a one-record recency window, so the records it judges are reached by the
// coverage sweep. Meant for a disposable compose project (like the Phase 1 coverage tests): the fee test adds
// components to its own TEST calculations (insert-only table) and leaves its findings open.

// F-R2-01: every missing fee category of a (source, calculation) group is its own finding, with exact identities; an
// open finding of one category — also one recorded under the pre-fix key — never hides another; repeated and
// concurrent runs never duplicate; fixing one category leaves the other reported; a resolved but persisting
// discrepancy is recorded again once; an open finding of one AMOUNT_MISMATCH comparison never hides another.
func TestReconciliationCorrectionsFeeEveryMissingType(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	donate := func(amount string) fees.FixtureResult {
		return f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": amount, "currency": "USD", "payment_method": "ECOCASH"}, 201, "")
	}
	members := func(d fees.FixtureResult) []string {
		var out []string
		for _, a := range d.Assessments {
			out = append(out, a.ID)
		}
		return out
	}
	calcOf := func(d fees.FixtureResult) string {
		var c string
		if err := f.mig.QueryRow(ctx(t), `SELECT calculation_id::text FROM app.fee_assessments WHERE id = $1`, d.Assessments[0].ID).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	addComponent := func(d fees.FixtureResult, category string) { // an assessable component nobody assessed (UNKNOWN basis: no amount)
		migratorExec(t, `INSERT INTO app.fee_components (id, calculation_id, seq, category, basis)
			SELECT gen_random_uuid(), $1::uuid, coalesce(max(seq), 0) + 1, $2, 'UNKNOWN' FROM app.fee_components WHERE calculation_id = $1::uuid`, calcOf(d), category)
	}
	// open MISSING_FEE findings of a group: category -> count, and their dedupe keys
	missing := func(d fees.FixtureResult) (map[string]int, []string) {
		rows, err := f.mig.Query(ctx(t), `SELECT expected->>'category', dedupe_key FROM app.fee_reconciliation_references
			WHERE kind = 'MISSING_FEE' AND status = 'OPEN' AND assessment_id = ANY($1::uuid[])`, members(d))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m, keys := map[string]int{}, []string{}
		for rows.Next() {
			var c, k string
			if err := rows.Scan(&c, &k); err != nil {
				t.Fatal(err)
			}
			m[c]++
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return m, keys
	}
	expect := func(what string, d fees.FixtureResult, want map[string]int) {
		t.Helper()
		got, keys := missing(d)
		if len(got) != len(want) {
			t.Fatalf("%s: open MISSING_FEE %v (keys %v), want %v", what, got, keys, want)
		}
		for c, n := range want {
			if got[c] != n {
				t.Fatalf("%s: open MISSING_FEE %v (keys %v), want %v", what, got, keys, want)
			}
		}
	}
	svc := *f.fs
	svc.ReconWindow = 1
	svc.ReconSweep = ownSweep("fees-r3", 50)
	cycleOf := func(s *fees.Service) {
		t.Helper()
		for i := 0; i < 400; i++ {
			run, err := s.Reconcile(ctx(t), "it-r3")
			if err != nil {
				t.Fatalf("fee reconcile: %v", err)
			}
			if run.Sweep != nil && run.Sweep.CycleCompleted {
				return
			}
		}
		t.Fatal("the fee sweep cycle never completed")
	}
	cycle := func() { t.Helper(); cycleOf(&svc) }

	d0, d1, d2, d3 := donate("10000"), donate("11000"), donate("12000"), donate("13000")
	addComponent(d1, "REFUND_FEE")
	addComponent(d2, "REFUND_FEE")
	addComponent(d2, "CHARGEBACK_FEE")
	addComponent(d3, "PAYOUT_PROCESSING_FEE")
	addComponent(d3, "REFUND_FEE")
	addComponent(d3, "CHARGEBACK_FEE")
	cycle()
	expect("complete group", d0, map[string]int{})
	expect("one missing", d1, map[string]int{"REFUND_FEE": 1})
	expect("two missing", d2, map[string]int{"REFUND_FEE": 1, "CHARGEBACK_FEE": 1})
	expect("several missing", d3, map[string]int{"PAYOUT_PROCESSING_FEE": 1, "REFUND_FEE": 1, "CHARGEBACK_FEE": 1})
	g0 := slices.Min(members(d2))
	if _, keys := missing(d2); !slices.Equal(keys, []string{"MISSING_FEE:" + g0 + "::category:CHARGEBACK_FEE", "MISSING_FEE:" + g0 + "::category:REFUND_FEE"}) {
		t.Fatalf("finding identities: %v", keys)
	}

	// repeated reconciliation of unresolved discrepancies: nothing new
	cycle()
	cycle()
	expect("repeat: two missing", d2, map[string]int{"REFUND_FEE": 1, "CHARGEBACK_FEE": 1})
	expect("repeat: several missing", d3, map[string]int{"PAYOUT_PROCESSING_FEE": 1, "REFUND_FEE": 1, "CHARGEBACK_FEE": 1})

	// an open finding for REFUND_FEE while CHARGEBACK_FEE becomes missing: both reported
	addComponent(d1, "CHARGEBACK_FEE")
	cycle()
	expect("open finding plus a newly missing type", d1, map[string]int{"REFUND_FEE": 1, "CHARGEBACK_FEE": 1})

	// concurrent evaluation of the same groups (two workers, one scope): no duplicate
	other := svc
	var wg sync.WaitGroup
	for _, s := range []*fees.Service{&svc, &other} {
		wg.Add(1)
		go func(s *fees.Service) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				if _, err := s.Reconcile(context.Background(), "it-r3-concurrent"); err != nil && !t.Failed() {
					t.Errorf("concurrent reconcile: %v", err)
				}
			}
		}(s)
	}
	wg.Wait()
	cycle()
	expect("concurrent", d2, map[string]int{"REFUND_FEE": 1, "CHARGEBACK_FEE": 1})

	refID := func(d fees.FixtureResult, category string) string {
		var id string
		if err := f.mig.QueryRow(ctx(t), `SELECT id::text FROM app.fee_reconciliation_references WHERE kind = 'MISSING_FEE' AND status = 'OPEN'
			AND expected->>'category' = $2 AND assessment_id = ANY($1::uuid[])`, members(d), category).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// one type is fixed (its assessment now exists) and its finding resolved; the other stays reported, once
	migratorExec(t, `INSERT INTO app.fee_assessments SELECT (jsonb_populate_record(NULL::app.fee_assessments,
		to_jsonb(a) || jsonb_build_object('id', $2::uuid, 'category', 'REFUND_FEE'))).* FROM app.fee_assessments a WHERE a.id = $1::uuid`,
		d2.Assessments[0].ID, ids.New())
	f.act(t, f.f.finance1, "POST", "/admin/fees/reconciliation/"+refID(d2, "REFUND_FEE")+"/resolve", map[string]any{"note": "IT: refund fee assessed"}, 200, "")
	cycle()
	expect("one fixed, the other remains", d2, map[string]int{"CHARGEBACK_FEE": 1})

	// a resolved discrepancy that persists is recorded again — once
	f.act(t, f.f.finance1, "POST", "/admin/fees/reconciliation/"+refID(d2, "CHARGEBACK_FEE")+"/resolve", map[string]any{"note": "IT: resolved early"}, 200, "")
	expect("after resolution", d2, map[string]int{})
	cycle()
	cycle()
	expect("reintroduced", d2, map[string]int{"CHARGEBACK_FEE": 1})

	// pre-fix data: an OPEN finding recorded under the legacy key (kind:assessment:) for REFUND_FEE does not hide a
	// missing CHARGEBACK_FEE, and REFUND_FEE is not recorded twice
	d4 := donate("14000")
	addComponent(d4, "REFUND_FEE")
	addComponent(d4, "CHARGEBACK_FEE")
	var runID string
	if err := f.mig.QueryRow(ctx(t), `SELECT id::text FROM app.fee_reconciliation_runs ORDER BY started_at DESC LIMIT 1`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	g4 := slices.Min(members(d4))
	migratorExec(t, `INSERT INTO app.fee_reconciliation_references (id, run_id, kind, severity, assessment_id, currency, expected, observed, detail,
		dedupe_key, status, detected_at) VALUES ($1::uuid, $2::uuid, 'MISSING_FEE', 'SEV2', $3::uuid, 'USD', '{"category":"REFUND_FEE"}', '{"assessment":null}',
		'the calculation has a REFUND_FEE component but no assessment exists for it', $4, 'OPEN', now())`, ids.New(), runID, g4, "MISSING_FEE:"+g4+":")
	cycle()
	cycle()
	expect("legacy open finding", d4, map[string]int{"REFUND_FEE": 1, "CHARGEBACK_FEE": 1})

	// AMOUNT_MISMATCH: an open provider-report finding (legacy key) never hides the calculation-component comparison
	var proc fees.Assessment
	for _, a := range d4.Assessments {
		if a.Category == "PAYMENT_PROCESSING_FEE" {
			proc = a
		}
	}
	if proc.ID == "" {
		t.Fatal("no processing-fee assessment in the fixture")
	}
	migratorExec(t, `INSERT INTO app.fee_reconciliation_references (id, run_id, kind, severity, assessment_id, currency, expected, observed, detail,
		dedupe_key, status, detected_at) VALUES ($1::uuid, $2::uuid, 'AMOUNT_MISMATCH', 'SEV2', $3::uuid, 'USD', '{}', '{}',
		'the provider-reported fee differs from the estimate', $4, 'OPEN', now())`, ids.New(), runID, proc.ID, "AMOUNT_MISMATCH:"+proc.ID+":")
	migratorExec(t, `ALTER TABLE app.fee_calculations DISABLE TRIGGER trg_fee_calculations_no_mutation`)
	migratorExec(t, `UPDATE app.fee_calculations c SET result = jsonb_set(c.result, ARRAY['components', (e.ord - 1)::text, 'amount_minor'], '"1"')
		FROM (SELECT x.ord FROM app.fee_calculations c2, jsonb_array_elements(c2.result->'components') WITH ORDINALITY x(c, ord)
		      WHERE c2.id = $1::uuid AND x.c->>'category' = 'PAYMENT_PROCESSING_FEE') e WHERE c.id = $1::uuid`, calcOf(d4))
	migratorExec(t, `ALTER TABLE app.fee_calculations ENABLE TRIGGER trg_fee_calculations_no_mutation`)
	cycle()
	cycle()
	var calcN, provN int
	if err := f.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE dedupe_key = $2), count(*) FILTER (WHERE dedupe_key = $3)
		FROM app.fee_reconciliation_references WHERE status = 'OPEN' AND kind = 'AMOUNT_MISMATCH' AND assessment_id = $1::uuid`,
		proc.ID, "AMOUNT_MISMATCH:"+proc.ID+"::calculation_component", "AMOUNT_MISMATCH:"+proc.ID+":").Scan(&calcN, &provN); err != nil {
		t.Fatal(err)
	}
	if calcN != 1 || provN != 1 {
		t.Fatalf("calculation-component finding %d (want 1) next to the legacy provider-report finding %d (want 1)", calcN, provN)
	}
	// the sweep recorded every run's outcome (F-R2-03) and the scope is healthy
	var outcome *string
	var failed int
	if err := f.mig.QueryRow(ctx(t), `SELECT last_attempt_outcome, failed_attempts FROM app.fee_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING'`,
		svc.ReconSweep.Scope).Scan(&outcome, &failed); err != nil {
		t.Fatal(err)
	}
	if outcome == nil || failed != 0 {
		t.Fatalf("run outcome not recorded: %v, failed %d", outcome, failed)
	}
}

// faultAdapter is the TEST simulator with an injectable payout status lookup. Reconciliation may only query: any
// instruction (create, cancel) through it fails the test.
type faultAdapter struct {
	*sandbox.Adapter
	t      *testing.T
	mu     sync.Mutex
	status map[string]func(o psp.Observation) psp.Observation // payout id -> replacement of the simulator's answer
	calls  map[string]int
}

func (a *faultAdapter) GetPayout(ctx context.Context, m psp.CallMeta, payoutID string) psp.Observation {
	o := a.Adapter.GetPayout(ctx, m, payoutID)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[payoutID]++
	if f := a.status[payoutID]; f != nil {
		return f(o)
	}
	return o
}

func (a *faultAdapter) CreatePayout(context.Context, psp.CallMeta, psp.CreatePayoutRequest) psp.Observation {
	a.t.Error("reconciliation sent a CREATE_PAYOUT instruction")
	return psp.Observation{Class: psp.ClassInvalidRequest}
}

func (a *faultAdapter) CancelPayout(context.Context, psp.CallMeta, string) psp.Observation {
	a.t.Error("reconciliation sent a cancellation instruction")
	return psp.Observation{Class: psp.ClassInvalidRequest}
}

func (a *faultAdapter) set(id string, f func(psp.Observation) psp.Observation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status[id] = f
}

// F-R2-02: a payout whose provider status lookup fails (timeout, connection loss, rate limit, malformed or incomplete
// answer) is not reconciled: it is carried as a deferral with its reason and age, retried, survives a worker restart,
// never changes the payout, its reservation or its attempts, never sends an instruction, never opens an exception from
// the failure; every other payout is examined; concurrent workers stay consistent; once the provider answers, a genuine
// inconsistency is recorded and consistent payouts resolve. F-R2-03: the run outcomes and the health of the scope.
func TestReconciliationCorrectionsPayoutLookupFailure(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 60_000)
	var pays []string
	for i := 0; i < 6; i++ { // amounts ending in 00: the simulator's SUCCESS scenario (the last two digits select it)
		pays = append(pays, p.completedPayout(t, camp, int64(5_000+i*100)))
	}
	sb := p.s.d.Verification.Sandbox
	fa := &faultAdapter{Adapter: sb, t: t, status: map[string]func(psp.Observation) psp.Observation{}, calls: map[string]int{}}
	reg := psp.New(p.po.PSP.Pool, p.po.PSP.Clock, p.po.PSP.Logger, p.po.PSP.Secrets, p.po.PSP.Cfg, nil)
	reg.Register(fa)
	svc := *p.po
	svc.PSP = reg
	svc.ReconWindow = 1
	svc.ReconSweep = reconsweep.Config{Scope: "it.payouts-r3." + ids.New()[24:], BatchSize: 2, MaxRecords: 100000, MaxDuration: 10 * time.Minute}

	timeout := func(psp.Observation) psp.Observation {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout}
	}
	failing := map[string]func(psp.Observation) psp.Observation{
		pays[0]: timeout,
		pays[1]: func(psp.Observation) psp.Observation { // connection lost
			return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownConnResetAfter}
		},
		pays[2]: func(psp.Observation) psp.Observation {
			return psp.Observation{Class: psp.ClassRateLimited, HTTPStatus: 429}
		},
		pays[3]: func(psp.Observation) psp.Observation { // malformed body
			return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed}
		},
		pays[4]: func(o psp.Observation) psp.Observation { // incomplete: an answer without any status
			return psp.Observation{Class: psp.ClassAccepted, ReportedAmount: o.ReportedAmount}
		},
	}
	for id, fn := range failing {
		fa.set(id, fn)
	}
	type snap struct {
		status, res string
		attempts    int
	}
	state := func(id string) snap {
		po := p.payout(t, id)
		var n int
		if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return snap{po.Status, po.Reservation.Status, n}
	}
	// the sweep walks every payout of the database (other tests' too): deferral counts are this test's payouts only
	ours := func(keys []string) int {
		n := 0
		for _, k := range keys {
			if failing[k] != nil {
				n++
			}
		}
		return n
	}
	uniq := func(keys []string) []string {
		c := slices.Clone(keys)
		slices.Sort(c)
		return slices.Compact(c)
	}
	before := map[string]snap{}
	for _, id := range pays {
		before[id] = state(id)
	}
	reps := payoutCycle(t, &svc)
	var unverified int
	causes := map[string]int{}
	for _, r := range reps {
		unverified += r.ProviderUnverified
		for c, n := range r.ProviderUnverifiedBy {
			causes[c] += n
		}
	}
	for _, c := range []string{"INDETERMINATE:TIMEOUT", "INDETERMINATE:CONN_RESET_AFTER_SEND", "RATE_LIMITED", "INDETERMINATE:MALFORMED_RESPONSE", "INCOMPLETE_RESPONSE"} {
		if causes[c] == 0 {
			t.Errorf("cause %s not reported: %v", c, causes)
		}
	}
	cov, err := reconsweep.SQLStore{Pool: p.mig, Table: "app.payout_reconciliation_sweeps"}.Coverage(ctx(t), payouts.SweepDetector, svc.ReconSweep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	cp := checkpointDeferrals(t, p, svc.ReconSweep.Scope)
	for id := range failing {
		if !slices.Contains(cp, id) {
			t.Errorf("payout %s with a failed lookup is not carried as a deferral: %v", id, cp)
		}
	}
	if slices.Contains(cp, pays[5]) {
		t.Errorf("the payout with a working lookup was deferred")
	}
	if cov.LastCompletedCycleStartedAt == nil || cov.DeferredOutstanding < len(failing) || cov.OldestDeferralSince == nil {
		t.Fatalf("coverage after the cycle: %+v", cov)
	}
	for _, id := range pays {
		if fa.calls[id] == 0 {
			t.Errorf("payout %s was never examined", id)
		}
		if p.openReconExceptions(t, id) != 0 {
			t.Errorf("payout %s: an exception was opened although nothing inconsistent was observed", id)
		}
		if got := state(id); got != before[id] {
			t.Errorf("payout %s changed during reconciliation: %+v → %+v", id, before[id], got)
		}
	}

	// repeated failure across runs, and a worker restart (a fresh service on the same scope): still carried, same
	// first-deferral time, nothing recorded, nothing changed
	oldest := *cov.OldestDeferralSince
	for i := 0; i < 2; i++ {
		restarted := svc
		if _, err := restarted.Reconcile(ctx(t), "it-r3-restart"); err != nil {
			t.Fatal(err)
		}
	}
	cov2, _ := reconsweep.SQLStore{Pool: p.mig, Table: "app.payout_reconciliation_sweeps"}.Coverage(ctx(t), payouts.SweepDetector, svc.ReconSweep.Scope)
	if ours(checkpointDeferrals(t, p, svc.ReconSweep.Scope)) != len(failing) || cov2.OldestDeferralSince == nil || cov2.OldestDeferralSince.After(oldest) {
		t.Fatalf("deferrals after repeated failures: %+v (oldest was %v)", cov2, oldest)
	}

	// concurrent workers while the lookups still fail: no error but conflicts, no exception, deferrals kept once
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := svc
			if _, err := w.Reconcile(context.Background(), "it-r3-concurrent"); err != nil && !t.Failed() {
				t.Errorf("concurrent reconcile: %v", err)
			}
		}()
	}
	wg.Wait()
	if cp := checkpointDeferrals(t, p, svc.ReconSweep.Scope); ours(cp) != len(failing) || len(cp) != len(uniq(cp)) {
		t.Fatalf("concurrent runs changed the deferrals: %v", cp)
	}

	// the sweep health of the scope (F-R2-03): advancing, deferrals outstanding, no failed runs
	cov3, _ := reconsweep.SQLStore{Pool: p.mig, Table: "app.payout_reconciliation_sweeps"}.Coverage(ctx(t), payouts.SweepDetector, svc.ReconSweep.Scope)
	h := cov3.Health(time.Now(), reconsweep.DefaultThresholds, 10000, 0)
	if h.State != reconsweep.HealthProgressing || h.FailedAttempts != 0 || h.DeferredOutstanding < len(failing) || h.LastAttemptAt == nil {
		t.Fatalf("health: %+v", h)
	}

	// the provider answers again: pays[0] reports another amount (a genuine inconsistency discovered after the failure),
	// the others answer consistently — every deferral resolves, exactly one mismatch is recorded, nothing changes state
	for id := range failing {
		fa.set(id, nil)
	}
	fa.set(pays[0], func(o psp.Observation) psp.Observation {
		amt := *o.ReportedAmount + 1
		o.ReportedAmount = &amt
		return o
	})
	if _, err := svc.Reconcile(ctx(t), "it-r3-recovered"); err != nil {
		t.Fatal(err)
	}
	if n := ours(checkpointDeferrals(t, p, svc.ReconSweep.Scope)); n != 0 {
		t.Fatalf("%d of this test's deferrals left after the provider recovered", n)
	}
	if n := p.refsOfType(t, pays[0], "AMOUNT_MISMATCH"); n != 1 || p.openReconExceptions(t, pays[0]) != 1 {
		t.Fatalf("the inconsistency found after the failure: %d references, %d exceptions", n, p.openReconExceptions(t, pays[0]))
	}
	for _, id := range pays[1:] {
		if p.openReconExceptions(t, id) != 0 {
			t.Errorf("payout %s: exception after a consistent answer", id)
		}
	}
	for _, id := range pays {
		if got := state(id); got != before[id] {
			t.Errorf("payout %s changed: %+v → %+v", id, before[id], got)
		}
	}
	// idempotent: a further run records nothing new
	if _, err := svc.Reconcile(ctx(t), "it-r3-again"); err != nil {
		t.Fatal(err)
	}
	if n := p.refsOfType(t, pays[0], "AMOUNT_MISMATCH"); n != 1 {
		t.Fatalf("repeated run duplicated the mismatch: %d", n)
	}
}

// F-R2-02: a payout whose outcome is UNKNOWN and whose status lookup keeps failing stays UNKNOWN (never FAILED by
// assumption), keeps its reservation, is not resubmitted and is carried until an answer is compared.
func TestReconciliationCorrectionsUnknownPayoutLookupFailure(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 10_004, 202, "") // suffix 04: the simulator never created it, the call timed out
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	if po := p.payout(t, id); po.Status != payouts.StUnknown {
		t.Fatalf("fixture: %s", po.Status)
	}
	sb := p.s.d.Verification.Sandbox
	fa := &faultAdapter{Adapter: sb, t: t, status: map[string]func(psp.Observation) psp.Observation{}, calls: map[string]int{}}
	fa.set(id, func(psp.Observation) psp.Observation {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout}
	})
	reg := psp.New(p.po.PSP.Pool, p.po.PSP.Clock, p.po.PSP.Logger, p.po.PSP.Secrets, p.po.PSP.Cfg, nil)
	reg.Register(fa)
	svc := *p.po
	svc.PSP = reg
	svc.ReconWindow = 1
	svc.ReconSweep = reconsweep.Config{Scope: "it.payouts-r3u." + ids.New()[24:], BatchSize: 100, MaxRecords: 100000, MaxDuration: 10 * time.Minute}
	var creates int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&creates)
	payoutCycle(t, &svc)
	payoutCycle(t, &svc)
	po := p.payout(t, id)
	var creates2 int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&creates2)
	if po.Status != payouts.StUnknown || po.Reservation.Status != payouts.ResBlocked || creates2 != creates {
		t.Fatalf("UNKNOWN payout after failed lookups: %s / %s, creates %d → %d", po.Status, po.Reservation.Status, creates, creates2)
	}
	if fa.calls[id] < 2 || !slices.Contains(checkpointDeferrals(t, p, svc.ReconSweep.Scope), id) {
		t.Fatalf("the UNKNOWN payout was not retried and carried: calls %d", fa.calls[id])
	}
}

func checkpointDeferrals(t *testing.T, p *payoutIT, scope string) []string {
	t.Helper()
	var keys []string
	if err := p.mig.QueryRow(ctx(t), `SELECT coalesce(deferred_keys::text[], '{}') FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING'`,
		scope).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	return keys
}
