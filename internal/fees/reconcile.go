package fees

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// Fee reconciliation (ADR-044; brief §41). For each assessment it compares: the stored calculation recomputed from
// its canonical inputs (determinism/rounding), the assessed amount against its calculation component, the ledger
// journals that carry it (amount, currency, policy version, duplicates), its expected postings, the provider's
// reported fee, and whether every assessable component of a source was assessed. Settlement evidence does not exist
// (PCR-007): provider fees can be reported (TEST fixture) but never RECONCILED against a statement in Stage 12.

// Mismatch kinds.
const (
	RecMissingFee       = "MISSING_FEE"
	RecDuplicateFee     = "DUPLICATE_FEE"
	RecAmountMismatch   = "AMOUNT_MISMATCH"
	RecCurrencyMismatch = "CURRENCY_MISMATCH"
	RecPolicyMismatch   = "POLICY_MISMATCH"
	RecRoundingMismatch = "ROUNDING_MISMATCH"
	RecUnreconciledFee  = "UNRECONCILED_PROVIDER_FEE"
	RecMissingPosting   = "MISSING_POSTING"
	DefaultReconGrace   = 15 * time.Minute
	// DefaultReconWindow is the recency pass (newest assessments); the coverage sweep below covers the rest.
	DefaultReconWindow = 5000
	SweepDetector      = "FEE_ASSESSMENTS"
	sweepTable         = "app.fee_reconciliation_sweeps"
	recStatusOpen      = "OPEN"
	recStatusResolved  = "RESOLVED"
)

// DefaultReconSweep bounds one run of the coverage sweep (KI-S19-09, ADR-052): every assessment in id order, a
// bounded batch per run, resumable from a checkpoint in app.fee_reconciliation_sweeps. Engineering defaults, not
// capacity targets (D-05, D-06).
var DefaultReconSweep = reconsweep.Config{Scope: reconsweep.DefaultScope, BatchSize: 500, MaxRecords: 5000, MaxDuration: 3 * time.Minute,
	MaxDeferred: 10000}

// ReconRun is the result of one reconciliation run.
type ReconRun struct {
	ID                       string     `json:"id"`
	TriggeredBy              string     `json:"triggered_by"`
	StartedAt                time.Time  `json:"started_at"`
	FinishedAt               *time.Time `json:"finished_at"`
	Checked                  int        `json:"checked"`
	Matched                  int        `json:"matched"`
	Mismatches               int        `json:"mismatches"`
	SettlementReconciliation string     `json:"settlement_reconciliation"`
	// Sweep and Coverage are the coverage sweep's evidence (Checked/Matched count the recency pass; Mismatches both).
	Sweep     *reconsweep.Report          `json:"sweep,omitempty"`
	Coverage  *reconsweep.Coverage        `json:"coverage,omitempty"`
	Integrity *reconsweep.IntegrityReport `json:"integrity,omitempty"` // remediation phase 4 (reconsweep.Verify)
}

// ReconItem is a detected mismatch.
type ReconItem struct {
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	Kind                string          `json:"kind"`
	Severity            string          `json:"severity"`
	AssessmentID        *string         `json:"assessment_id"`
	AdjustmentID        *string         `json:"adjustment_id"`
	LedgerTransactionID *string         `json:"ledger_transaction_id"`
	Currency            *string         `json:"currency"`
	Expected            json.RawMessage `json:"expected"`
	Observed            json.RawMessage `json:"observed"`
	Detail              string          `json:"detail"`
	Status              string          `json:"status"`
	DetectedAt          time.Time       `json:"detected_at"`
	ResolvedBy          *string         `json:"resolved_by"`
	ResolvedAt          *time.Time      `json:"resolved_at"`
	ResolutionNote      *string         `json:"resolution_note"`
}

type finding struct {
	kind, severity, detail string
	assessmentID, txID     string
	currency               string
	expected, observed     map[string]any
	// check names the comparison that produced the finding when one kind can come from several comparisons on the same
	// assessment and journal (F-R2-01): it is part of the dedupe key, so an open finding of one comparison never hides
	// a different discrepancy. Empty for kinds with one comparison per (assessment, journal): their key is unchanged.
	check string
}

// Checks of the kinds that several comparisons produce for the same assessment and journal (F-R2-01).
const (
	checkCalcComponent   = "calculation_component"
	checkJournalLines    = "journal_fee_lines"
	checkPostedAssessed  = "posted_vs_assessed"
	checkProviderReport  = "provider_report"
	checkCategoryPrefix  = "category:"
	detailCalcComponent  = "the assessed fee differs from its calculation"
	detailJournalLines   = "the journal's fee lines differ from the recorded fee"
	detailPostedAssessed = "the posted fee differs from the assessed fee"
	detailProviderReport = "the provider-reported fee differs from the estimate"
)

// dedupeKey identifies one discrepancy: an OPEN reference with the same key is the same discrepancy, still unresolved.
func dedupeKey(f finding) string {
	k := legacyKey(f)
	if f.check != "" {
		k += ":" + f.check
	}
	return k
}

// legacyKey is the key before F-R2-01 (kind, assessment, journal) — still the key of kinds without a check, and the key
// of OPEN references recorded before the fix.
func legacyKey(f finding) string { return f.kind + ":" + f.assessmentID + ":" + f.txID }

// legacyCheck derives the check of a reference recorded under a legacy key from its stored content ("" when it cannot
// be derived: the reference is then not taken to represent the finding, so a distinct discrepancy is never hidden; at
// worst one finding is recorded once more under its new key).
func legacyCheck(kind, detail string, expected map[string]any) string {
	switch kind {
	case RecMissingFee:
		if c, ok := expected["category"].(string); ok && c != "" {
			return checkCategoryPrefix + c
		}
	case RecAmountMismatch:
		switch detail {
		case detailCalcComponent:
			return checkCalcComponent
		case detailJournalLines:
			return checkJournalLines
		case detailPostedAssessed:
			return checkPostedAssessed
		case detailProviderReport:
			return checkProviderReport
		}
	}
	return ""
}

func (s *Service) grace() time.Duration {
	if s.ReconGrace > 0 {
		return s.ReconGrace
	}
	return DefaultReconGrace
}

// Reconcile runs fee reconciliation. Detections are idempotent: an OPEN item with the same kind and subject is not
// duplicated.
func (s *Service) Reconcile(ctx context.Context, triggeredBy string) (ReconRun, error) {
	now := s.now()
	run := ReconRun{ID: ids.New(), TriggeredBy: triggeredBy, StartedAt: now, SettlementReconciliation: "NOT_PERFORMED"}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO app.fee_reconciliation_runs (id, triggered_by, started_at) VALUES ($1, $2, $3)`,
		run.ID, triggeredBy, now); err != nil {
		return run, err
	}
	window := s.ReconWindow
	if window <= 0 {
		window = DefaultReconWindow
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a ORDER BY a.assessed_at DESC LIMIT $1`, window)
	if err != nil {
		return run, err
	}
	var list []Assessment
	for rows.Next() {
		a, err := scanAssessment(rows)
		if err != nil {
			rows.Close()
			return run, err
		}
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return run, err
	}
	var findings []finding
	sources := map[string][]Assessment{}
	for _, a := range list {
		run.Checked++
		fs, err := s.checkAssessment(ctx, a, now)
		if err != nil {
			return run, err
		}
		if len(fs) == 0 {
			run.Matched++
		}
		findings = append(findings, fs...)
		sources[a.SourceType+":"+a.SourceID+":"+a.CalculationID] = append(sources[a.SourceType+":"+a.SourceID+":"+a.CalculationID], a)
	}
	for _, part := range sources {
		// judge the whole group (it may extend beyond the window), keyed by its first assessment like the sweep
		group, err := s.sourceGroup(ctx, part[0])
		if err != nil {
			return run, err
		}
		fs, err := s.missingFees(ctx, group)
		if err != nil {
			return run, err
		}
		findings = append(findings, fs...)
	}
	for _, f := range findings {
		inserted, err := s.recordFinding(ctx, run.ID, f, now)
		if err != nil {
			return run, err
		}
		if inserted {
			run.Mismatches++
		}
	}
	// coverage sweep: every assessment, a bounded batch per run, resumable
	store := reconsweep.SQLStore{Pool: s.Pool, Table: sweepTable}
	cfg := s.ReconSweep.Or(DefaultReconSweep)
	sweep, sweepErr := reconsweep.Run(ctx, store, reconsweep.Options{Config: cfg, Detector: SweepDetector, RunID: run.ID, Now: s.now},
		s.sweepNext, func(ctx context.Context, id string) (reconsweep.Outcome, error) {
			n, err := s.sweepExamine(ctx, run.ID, id)
			run.Mismatches += n
			return reconsweep.Outcome{Exceptions: n}, err
		})
	run.Sweep = &sweep
	if ctx.Err() == nil {
		integ, ierr := reconsweep.Verify(ctx, store, s.sweepScope, reconsweep.IntegrityOptions{Detector: SweepDetector, Scope: cfg.Scope, Now: s.now})
		run.Integrity = &integ
		if sweepErr == nil {
			sweepErr = ierr
		}
	}
	// the run is closed even when the sweep stopped on an error (non-cancelled context)
	actx := context.WithoutCancel(ctx)
	if cov, err := store.Coverage(actx, SweepDetector, cfg.Scope); err == nil {
		run.Coverage = &cov
	}
	fin := s.now()
	run.FinishedAt = &fin
	_, err = s.Pool.Exec(actx, `UPDATE app.fee_reconciliation_runs SET finished_at = $2, checked = $3, matched = $4, mismatches = $5 WHERE id = $1`,
		run.ID, fin, run.Checked, run.Matched, run.Mismatches)
	if sweepErr != nil {
		return run, sweepErr
	}
	return run, err
}

// missingFees reports MISSING_FEE: an assessable component of the calculation behind a source with no assessment.
// group holds every assessment of one (source, calculation) that the caller knows.
func (s *Service) missingFees(ctx context.Context, group []Assessment) ([]finding, error) {
	cs, err := components(ctx, s.Pool, group[0].CalculationID)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	members := make([]string, 0, len(group))
	for _, a := range group {
		have[a.Category] = true
		members = append(members, a.ID)
	}
	// an OPEN finding for the same missing category on any member already reports it (Stage 12 keyed it by the
	// newest member of the window; the group's first member is used from ADR-052 on)
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT expected->>'category' FROM app.fee_reconciliation_references
		WHERE kind = 'MISSING_FEE' AND status = 'OPEN' AND assessment_id = ANY($1::uuid[])`, members)
	if err != nil {
		return nil, err
	}
	open, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	if err != nil {
		return nil, err
	}
	for _, c := range open {
		if c != nil {
			have[*c] = true
		}
	}
	return missingFindings(group, cs, have), nil
}

// missingFindings is the pure MISSING_FEE judgement of one (source, calculation) group: one finding per assessable
// component category with neither an assessment nor an OPEN finding (have), each with its own check, so every missing
// category is reported (F-R2-01). A category listed twice in the calculation is reported once.
func missingFindings(group []Assessment, cs []componentRow, have map[string]bool) []finding {
	var out []finding
	seen := map[string]bool{}
	for _, c := range cs {
		if c.Basis == BasisNotConfigured || have[c.Category] || seen[c.Category] {
			continue
		}
		seen[c.Category] = true
		out = append(out, finding{kind: RecMissingFee, severity: "SEV2", assessmentID: group[0].ID, currency: group[0].Currency,
			detail:   "the calculation has a " + c.Category + " component but no assessment exists for it",
			expected: map[string]any{"category": c.Category, "amount_minor": amtP(c.Amount)}, observed: map[string]any{"assessment": nil},
			check: checkCategoryPrefix + c.Category})
	}
	return out
}

// sourceGroup returns every assessment of a's source and calculation, in id order (group[0] carries the group's
// MISSING_FEE finding in both passes, so their dedupe keys agree).
func (s *Service) sourceGroup(ctx context.Context, a Assessment) ([]Assessment, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a WHERE a.source_type = $1 AND a.source_id = $2
		AND a.calculation_id = $3 ORDER BY a.id`, a.SourceType, a.SourceID, a.CalculationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Assessment, error) { return scanAssessment(r) })
}

// sweepNext lists the next assessments in id order.
func (s *Service) sweepNext(ctx context.Context, after string, limit int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text FROM app.fee_assessments a WHERE a.id > $1::uuid ORDER BY a.id LIMIT $2`, reconsweep.After(after), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// sweepScope is the integrity Source of the fee sweep: every assessment in (from, to] (all are in scope) with its last
// modification (updated_at, set by the database on every change). Independent of sweepNext: no ordering, no limit.
func (s *Service) sweepScope(ctx context.Context, from, to string) ([]reconsweep.Stamp, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text, a.updated_at FROM app.fee_assessments a WHERE a.id > $1::uuid AND a.id <= $2::uuid`,
		reconsweep.After(from), reconsweep.Until(to))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (reconsweep.Stamp, error) {
		var st reconsweep.Stamp
		err := r.Scan(&st.Key, &st.Modified)
		return st, err
	})
}

// sweepExamine checks one assessment in its current state, and the completeness of its source's calculation (every
// assessment of that source and calculation is read, so a group split across batches is judged whole), and records
// the findings. Returns the number of new findings.
func (s *Service) sweepExamine(ctx context.Context, runID, id string) (int, error) {
	now := s.now()
	a, err := s.Assessment(ctx, id)
	if err != nil {
		return 0, err
	}
	fs, err := s.checkAssessment(ctx, a, now)
	if err != nil {
		return 0, err
	}
	group, err := s.sourceGroup(ctx, a)
	if err != nil {
		return 0, err
	}
	if len(group) > 0 && group[0].ID == a.ID { // one completeness judgement per group: by its first assessment
		mf, err := s.missingFees(ctx, group)
		if err != nil {
			return 0, err
		}
		fs = append(fs, mf...)
	}
	n := 0
	for _, f := range fs {
		inserted, err := s.recordFinding(ctx, runID, f, now)
		if err != nil {
			return n, err
		}
		if inserted {
			n++
		}
	}
	return n, nil
}

func (s *Service) checkAssessment(ctx context.Context, a Assessment, now time.Time) ([]finding, error) {
	var out []finding
	add := func(kind, sev, detail, txID string, exp, obs map[string]any) {
		out = append(out, finding{kind: kind, severity: sev, detail: detail, assessmentID: a.ID, txID: txID, currency: a.Currency,
			expected: exp, observed: obs})
	}
	addAmount := func(check, sev, detail, txID string, exp, obs map[string]any) {
		add(RecAmountMismatch, sev, detail, txID, exp, obs)
		out[len(out)-1].check = check
	}
	// 1. determinism: recompute the calculation from its canonical inputs
	var inputsRaw, resultRaw []byte
	var calcCcy string
	if err := s.Pool.QueryRow(ctx, `SELECT inputs, result, currency FROM app.fee_calculations WHERE id = $1`, a.CalculationID).
		Scan(&inputsRaw, &resultRaw, &calcCcy); err != nil {
		return nil, err
	}
	var in Inputs
	if err := json.Unmarshal(inputsRaw, &in); err != nil {
		return nil, err
	}
	re, err := Compute(in)
	var stored ResultDoc
	_ = json.Unmarshal(resultRaw, &stored)
	if err != nil {
		add(RecRoundingMismatch, "SEV2", "the stored calculation cannot be recomputed: "+err.Error(), "", nil, nil)
	} else if !sameJSON(re.ResultJSON, resultRaw) {
		add(RecRoundingMismatch, "SEV2", "recomputing the stored inputs gives a different result", "",
			map[string]any{"result": json.RawMessage(re.ResultJSON)}, map[string]any{"result": json.RawMessage(resultRaw)})
	}
	if calcCcy != a.Currency {
		add(RecCurrencyMismatch, "SEV1", "the assessment's currency differs from its calculation's", "", map[string]any{"currency": calcCcy},
			map[string]any{"currency": a.Currency})
	}
	// 2. the assessed amount equals its calculation component
	for _, c := range stored.Components {
		if c.Category == a.Category && !eqStrP(c.Amount, a.FeeMinor) {
			addAmount(checkCalcComponent, "SEV1", detailCalcComponent, "", map[string]any{"fee_minor": c.Amount},
				map[string]any{"fee_minor": a.FeeMinor})
		}
	}
	// 3. postings
	refs, err := s.accountingRefs(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	purposes := map[string]int{}
	for _, r := range refs {
		own := r.AssessmentID != nil // the assessment's own journal (adjustments may have many: one per refund)
		if own {
			purposes[r.Purpose]++
		}
		if own && purposes[r.Purpose] == 2 {
			add(RecDuplicateFee, "SEV1", "more than one "+r.Purpose+" journal carries this fee", r.LedgerTransactionID,
				map[string]any{"journals": 1}, map[string]any{"journals": purposes[r.Purpose]})
		}
		if s.Ledger == nil {
			continue
		}
		j, ok, err := s.Ledger.JournalAmountsByKey(ctx, r.JournalKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			add(RecMissingPosting, "SEV1", "the referenced ledger journal does not exist", r.LedgerTransactionID, map[string]any{"journal_key": r.JournalKey}, nil)
			continue
		}
		if j.Currency != a.Currency {
			add(RecCurrencyMismatch, "SEV1", "the fee journal's currency differs from the assessment's", j.TransactionID,
				map[string]any{"currency": a.Currency}, map[string]any{"currency": j.Currency})
		}
		if a.PolicyVersionID != nil && (j.FeePolicyVersionID == nil || *j.FeePolicyVersionID != *a.PolicyVersionID) && r.Purpose != "PROVIDER_FEE" {
			add(RecPolicyMismatch, "SEV1", "the journal does not reference the assessment's policy version", j.TransactionID,
				map[string]any{"policy_version_id": a.PolicyVersionID}, map[string]any{"policy_version_id": j.FeePolicyVersionID})
		}
		if got, want := postedFee(j, r.Purpose), r.FeeAmountMinor; got != want {
			addAmount(checkJournalLines, "SEV1", detailJournalLines, j.TransactionID,
				map[string]any{"fee_minor": amt(want)}, map[string]any{"fee_minor": amt(got)})
		}
		if own && r.Purpose == "CAPTURE" && a.fee != nil && r.FeeAmountMinor != *a.fee {
			addAmount(checkPostedAssessed, "SEV1", detailPostedAssessed, j.TransactionID,
				map[string]any{"fee_minor": a.FeeMinor}, map[string]any{"fee_minor": amt(r.FeeAmountMinor)})
		}
	}
	if a.AccountingStatus == AcctPending && now.Sub(a.AssessedAt) > s.grace() {
		add(RecMissingPosting, "SEV2", "the fee is still PENDING_POSTING after the grace period", "", map[string]any{"accounting_status": AcctPosted},
			map[string]any{"accounting_status": a.AccountingStatus})
	}
	// 4. provider-reported fees
	if a.Category != CatPlatform {
		reps, err := s.providerReports(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		switch {
		case len(reps) == 0 && now.Sub(a.AssessedAt) > s.grace():
			add(RecUnreconciledFee, "SEV3", "no provider-reported or provider-confirmed fee exists for this "+a.Basis+" fee (no provider evidence source in Stage 12)",
				"", map[string]any{"basis": BasisProviderConfirmed}, map[string]any{"basis": a.Basis})
		case len(reps) > 0 && (a.fee == nil || reps[0] != *a.fee):
			addAmount(checkProviderReport, "SEV2", detailProviderReport, "", map[string]any{"fee_minor": a.FeeMinor},
				map[string]any{"reported_fee_minor": amt(reps[0])})
		}
	}
	return out, nil
}

// postedFee is the fee amount a journal carries for a purpose.
func postedFee(j ledger.JournalAmounts, purpose string) int64 {
	switch purpose {
	case "CAPTURE":
		return j.Amount(ledger.ClassPlatformFees, ledger.Credit)
	case "REFUND", "CHARGEBACK":
		return j.Amount(ledger.ClassPlatformFees, ledger.Debit)
	case "PROVIDER_FEE":
		return j.Amount(ledger.ClassPSPClearing, ledger.Credit)
	case "PAYOUT_FEE":
		return j.Amount(ledger.ClassPSPProcessingFees, ledger.Debit)
	case "CHARGEBACK_FEE":
		return j.Amount(ledger.ClassPSPSettled, ledger.Credit)
	}
	return 0
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

func eqStrP(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// AccountingRef is a fee ↔ journal link.
type AccountingRef struct {
	ID                  string    `json:"id"`
	AssessmentID        *string   `json:"assessment_id"`
	AdjustmentID        *string   `json:"adjustment_id"`
	LedgerTransactionID string    `json:"ledger_transaction_id"`
	JournalKey          string    `json:"journal_key"`
	PostingRule         string    `json:"posting_rule"`
	Purpose             string    `json:"purpose"`
	Currency            string    `json:"currency"`
	FeeAmountMinor      int64     `json:"-"`
	FeeAmount           string    `json:"fee_amount_minor"`
	PolicyVersionID     *string   `json:"policy_version_id"`
	RecordedAt          time.Time `json:"recorded_at"`
}

func (s *Service) accountingRefs(ctx context.Context, assessmentID string) ([]AccountingRef, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, assessment_id::text, adjustment_id::text, ledger_transaction_id, journal_key, posting_rule, purpose,
		currency, fee_amount_minor, policy_version_id::text, recorded_at FROM app.fee_accounting_references
		WHERE assessment_id = $1 OR adjustment_id IN (SELECT id FROM app.fee_adjustments WHERE assessment_id = $1) ORDER BY recorded_at, id`, assessmentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountingRef, error) {
		var x AccountingRef
		err := r.Scan(&x.ID, &x.AssessmentID, &x.AdjustmentID, &x.LedgerTransactionID, &x.JournalKey, &x.PostingRule, &x.Purpose, &x.Currency,
			&x.FeeAmountMinor, &x.PolicyVersionID, &x.RecordedAt)
		x.FeeAmount = amt(x.FeeAmountMinor)
		return x, err
	})
}

// AccountingRefs returns the journals behind an assessment and its adjustments (staff detail).
func (s *Service) AccountingRefs(ctx context.Context, assessmentID string) ([]AccountingRef, error) {
	if !ids.Valid(assessmentID) {
		return nil, ErrNotFound
	}
	return s.accountingRefs(ctx, assessmentID)
}

func (s *Service) providerReports(ctx context.Context, assessmentID string) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT reported_fee_minor FROM app.fee_provider_reports WHERE assessment_id = $1 ORDER BY reported_at DESC`, assessmentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func (s *Service) recordFinding(ctx context.Context, runID string, f finding, now time.Time) (bool, error) {
	exp, _ := json.Marshal(nonNil(f.expected))
	obs, _ := json.Marshal(nonNil(f.observed))
	key := dedupeKey(f)
	if f.check != "" {
		// an OPEN reference recorded before F-R2-01 under the legacy key is this finding only if its content shows the
		// same check; a reference of another check (another category, another comparison) does not hide this one
		var detail string
		var expRaw []byte
		err := s.Pool.QueryRow(ctx, `SELECT detail, expected FROM app.fee_reconciliation_references WHERE dedupe_key = $1 AND status = 'OPEN'`,
			legacyKey(f)).Scan(&detail, &expRaw)
		switch {
		case err == nil:
			var e map[string]any
			_ = json.Unmarshal(expRaw, &e)
			if legacyCheck(f.kind, detail, e) == f.check {
				return false, nil
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return false, err
		}
	}
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO app.fee_reconciliation_references (id, run_id, kind, severity, assessment_id, ledger_transaction_id, currency,
		expected, observed, detail, dedupe_key, status, detected_at)
		VALUES ($1, $2, $3, $4, nullif($5, '')::uuid, nullif($6, '')::uuid, nullif($7, ''), $8, $9, $10, $11, 'OPEN', $12)
		ON CONFLICT (dedupe_key) WHERE status = 'OPEN' DO NOTHING RETURNING id`,
		ids.New(), runID, f.kind, f.severity, f.assessmentID, f.txID, f.currency, exp, obs, f.detail, key, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

const reconCols = `id, run_id, kind, severity, assessment_id::text, adjustment_id::text, ledger_transaction_id::text, currency, expected, observed,
	detail, status, detected_at, resolved_by::text, resolved_at, resolution_note`

func scanRecon(r pgx.Row) (ReconItem, error) {
	var x ReconItem
	err := r.Scan(&x.ID, &x.RunID, &x.Kind, &x.Severity, &x.AssessmentID, &x.AdjustmentID, &x.LedgerTransactionID, &x.Currency, &x.Expected,
		&x.Observed, &x.Detail, &x.Status, &x.DetectedAt, &x.ResolvedBy, &x.ResolvedAt, &x.ResolutionNote)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// ReconItems lists reconciliation items (OPEN first, newest first).
func (s *Service) ReconItems(ctx context.Context, status string, limit int) ([]ReconItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+reconCols+` FROM app.fee_reconciliation_references WHERE ($1 = '' OR status = $1)
		ORDER BY (status = 'OPEN') DESC, detected_at DESC, id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReconItem{}
	for rows.Next() {
		x, err := scanRecon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// LatestRun returns the latest reconciliation run (nil: none).
func (s *Service) LatestRun(ctx context.Context) (*ReconRun, error) {
	var r ReconRun
	err := s.Pool.QueryRow(ctx, `SELECT id, triggered_by, started_at, finished_at, checked, matched, mismatches FROM app.fee_reconciliation_runs
		ORDER BY started_at DESC LIMIT 1`).Scan(&r.ID, &r.TriggeredBy, &r.StartedAt, &r.FinishedAt, &r.Checked, &r.Matched, &r.Mismatches)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	r.SettlementReconciliation = "NOT_PERFORMED"
	return &r, err
}

// ResolveRecon records a resolution note (fee.adjust.request). It never changes a fee, an assessment or the ledger.
func (s *Service) ResolveRecon(ctx context.Context, staffID, id, note string) (ReconItem, error) {
	note = strings.TrimSpace(note)
	if n := utf8.RuneCountInString(note); n < 5 || n > 2000 {
		return ReconItem{}, validation("note", "INVALID_LENGTH")
	}
	if !ids.Valid(id) {
		return ReconItem{}, ErrNotFound
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.fee_reconciliation_references SET status = 'RESOLVED', resolved_by = $2, resolved_at = $3, resolution_note = $4
			WHERE id = $1 AND status = 'OPEN'`, id, staffID, now, note)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := scanRecon(tx.QueryRow(ctx, `SELECT `+reconCols+` FROM app.fee_reconciliation_references WHERE id = $1`, id)); err != nil {
				return err
			}
			return ErrReconState
		}
		if err := event(ctx, tx, "fee_reconciliation", id, "RECONCILIATION_RESOLVED", staff(staffID), map[string]any{"note": note}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.reconciliation.resolved", TargetType: "fee_reconciliation", TargetID: id})
	})
	if err != nil {
		return ReconItem{}, err
	}
	return scanRecon(s.Pool.QueryRow(ctx, `SELECT `+reconCols+` FROM app.fee_reconciliation_references WHERE id = $1`, id))
}
