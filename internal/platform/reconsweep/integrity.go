package reconsweep

import (
	"context"
	"errors"
	"sort"
	"time"
)

// Checkpoint integrity (remediation phase 4). Two layers protect the coverage claim of a checkpoint:
//
//  1. Preventive, in the database: every position change of a checkpoint must be justified, in the same transaction,
//     by one append-only evidence row (Evidence) whose range starts at the old cursor and ends at the new one, whose key
//     count is exactly the growth of `examined`, and whose deferrals added/resolved turn the old deferral set into the
//     new one; a new cycle must start with the previous cycle's final deferrals (constraint triggers, migration
//     20261027090000). A forward jump of the cursor without evidence is refused at commit — by any role.
//  2. Detective, here (Verify): independent of the walk's keyset reads, it re-reads each detector's scope through the
//     module's own set query (Source: no ordering, no limit) and checks the evidence against it and against the
//     checkpoints. It finds what the database cannot judge: an in-scope record that existed, unchanged, before its
//     batch was examined but is absent from the batch's evidence (RECORD_SKIPPED); a completed cycle that ended before
//     records that already existed (COMPLETED_EARLY); and any break of the chain the triggers did not see (cycles from
//     before the evidence, rows written with the triggers disabled).
//
// Verify never moves a checkpoint, never examines a record and never creates a finding about money: it records an
// integrity status on the running checkpoint (RecordIntegrity), which Health turns into INTEGRITY_FAILURE and the
// operations scan into an in-app alert.
//
// Threat assumptions and limits: a writer that fabricates evidence listing REAL in-scope keys it never examined is not
// detectable (the evidence says what was examined; nothing can prove an examination happened). Records modified after
// their batch's examination time (minus Margin) are "late changes", expected under the moving-dataset rule (the next
// cycle examines them) and counted, not flagged.

// CycleRecord is one checkpoint row as the verifier reads it.
type CycleRecord struct {
	ID          string
	Cycle       int64
	Status      string
	StartedAt   time.Time
	CompletedAt *time.Time
	Cursor      string
	Examined    int64
	Batches     int64
	Deferred    []string
}

// IntegrityStore reads cycles and their evidence and records the verification outcome.
type IntegrityStore interface {
	IntegrityCycles(ctx context.Context, detector, scope string) (running, last, beforeLast *CycleRecord, err error)
	Evidence(ctx context.Context, sweepID string) ([]Evidence, error)
	RecordIntegrity(ctx context.Context, detector, scope string, r IntegrityReport) error
}

// Stamp is an in-scope key with its last modification time.
type Stamp struct {
	Key      string
	Modified time.Time
}

// Source re-reads a detector's scope independently of its keyset walk: every key in scope NOW with from < key ≤ to
// (from "" = from the start, to "" = unbounded), with its last modification time, in any order.
type Source func(ctx context.Context, from, to string) ([]Stamp, error)

// Integrity statuses.
const (
	IntegrityVerified     = "VERIFIED"     // every check passed over the running and the last completed cycle
	IntegrityInconsistent = "INCONSISTENT" // at least one problem: investigate; nothing is changed automatically
	IntegrityIncomplete   = "INCOMPLETE"   // the time budget ended before every batch was re-read (not verified)
	IntegrityUnverifiable = "UNVERIFIABLE" // a cycle has batches from before the evidence existed (migration)
	IntegrityError        = "ERROR"        // the verification itself failed (remediation phase 6): nothing was verified
)

// Reason codes of a verification that did not complete (IntegrityReport.Error; sanitised: never a database message).
const (
	IntegrityErrCyclesRead   = "CYCLES_READ_FAILED"
	IntegrityErrEvidenceRead = "EVIDENCE_READ_FAILED"
	IntegrityErrSourceRead   = "SOURCE_READ_FAILED"
	IntegrityErrInterrupted  = "INTERRUPTED" // the run's context ended (job timeout, shutdown)
	IntegrityErrTimeBudget   = "TIME_BUDGET" // the re-read budget ended before every batch was re-read
)

// Problem codes.
const (
	ProblemBatchMissing        = "BATCH_MISSING"          // evidence rows do not number 1..batches
	ProblemChainBroken         = "CHAIN_BROKEN"           // a batch does not start where the previous one ended
	ProblemCursorBeyond        = "CURSOR_BEYOND_EVIDENCE" // the checkpoint's cursor is not where the evidence ends
	ProblemExaminedMismatch    = "EXAMINED_MISMATCH"      // examined ≠ the evidenced keys
	ProblemKeyOutOfRange       = "KEY_OUT_OF_RANGE"       // an evidenced key outside its batch range, or out of order
	ProblemDuplicateKey        = "DUPLICATE_KEY"          // a key evidenced twice in one cycle
	ProblemDeferralsMismatch   = "DEFERRALS_MISMATCH"     // the carried deferrals differ from the evidenced ones
	ProblemRecordSkipped       = "RECORD_SKIPPED"         // an in-scope record unchanged since before its batch is not in it
	ProblemCompletedEarly      = "COMPLETED_EARLY"        // a completed cycle ended before records that already existed
	ProblemCompletionMissing   = "COMPLETION_NOT_EVIDENCED"
	maxProblems                = 50
	maxProblemKeys             = 10
	defaultIntegrityMargin     = 5 * time.Minute
	defaultIntegrityMaxRuntime = time.Minute
)

// Problem is one integrity finding (bounded: a few example keys).
type Problem struct {
	Code   string   `json:"code"`
	Cycle  int64    `json:"cycle"`
	Seq    int64    `json:"seq,omitempty"`
	Count  int      `json:"count"`
	Keys   []string `json:"keys,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// CycleIntegrity is the outcome for one cycle.
type CycleIntegrity struct {
	Cycle           int64  `json:"cycle"`
	Status          string `json:"status"` // RUNNING | COMPLETED
	Batches         int64  `json:"batches"`
	Evidenced       int    `json:"evidenced_batches"`
	ReReadBatches   int    `json:"reread_batches"`
	EvidencedKeys   int    `json:"evidenced_keys"`
	LateChanges     int    `json:"late_changes"`
	Unverifiable    bool   `json:"unverifiable,omitempty"`
	TailReRead      bool   `json:"tail_reread,omitempty"`
	TailLateChanges int    `json:"tail_late_changes,omitempty"`
}

// IntegrityReport is one verification.
type IntegrityReport struct {
	Detector  string           `json:"detector"`
	Scope     string           `json:"scope"`
	Status    string           `json:"status"`
	CheckedAt time.Time        `json:"checked_at"`
	Cycles    []CycleIntegrity `json:"cycles"`
	Problems  []Problem        `json:"problems"`
	Elapsed   string           `json:"elapsed"`
	Error     string           `json:"error,omitempty"` // reason code when the verification did not complete
}

func (r *IntegrityReport) problem(p Problem) {
	if len(p.Keys) > maxProblemKeys {
		p.Keys = p.Keys[:maxProblemKeys]
	}
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, p)
	}
}

// bounded is the report trimmed to fit the checkpoint's integrity_detail column (≤ 8 KiB).
func (r IntegrityReport) bounded() IntegrityReport {
	if len(r.Problems) > 20 {
		r.Problems = r.Problems[:20]
	}
	return r
}

// IntegrityOptions bound one verification.
type IntegrityOptions struct {
	Detector, Scope string
	// Margin: a record modified less than this before its batch was examined counts as a late change, not as skipped
	// (a transaction that inserted it may have committed after the batch's keyset read). Engineering default 5 min.
	Margin     time.Duration
	MaxRuntime time.Duration // re-reading stops here; the status is then INCOMPLETE (never VERIFIED)
	Now        func() time.Time
}

// Verify checks the running cycle and the last completed one of a detector and records the outcome on the running
// checkpoint — every outcome, including a failure of the verification itself (remediation phase 6, F-R5-01): a read
// error is recorded as ERROR, an interruption or the time budget as INCOMPLETE, each with a sanitised reason code (never a
// raw database message), so a previous VERIFIED is never left standing as the latest outcome. It returns the report and
// the error of reading or recording (a problem found is not an error).
func Verify(ctx context.Context, st IntegrityStore, src Source, o IntegrityOptions) (IntegrityReport, error) {
	if o.Margin <= 0 {
		o.Margin = defaultIntegrityMargin
	}
	if o.MaxRuntime <= 0 {
		o.MaxRuntime = defaultIntegrityMaxRuntime
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	start := o.Now()
	rep := IntegrityReport{Detector: o.Detector, Scope: o.Scope, CheckedAt: start, Problems: []Problem{}}
	incomplete, unverifiable := false, false
	fail := func(code string, err error) (IntegrityReport, error) {
		switch {
		case len(rep.Problems) > 0:
			rep.Status = IntegrityInconsistent // what was found stands, even if the verification could not finish
		case ctx.Err() != nil:
			rep.Status, code = IntegrityIncomplete, IntegrityErrInterrupted
		default:
			rep.Status = IntegrityError
		}
		rep.Error = code
		rep.Elapsed = o.Now().Sub(start).Round(time.Millisecond).String()
		return rep, errors.Join(err, st.RecordIntegrity(context.WithoutCancel(ctx), o.Detector, o.Scope, rep))
	}
	running, last, beforeLast, err := st.IntegrityCycles(ctx, o.Detector, o.Scope)
	if err != nil {
		return fail(IntegrityErrCyclesRead, err)
	}
	check := func(c *CycleRecord, initialDeferred []string) (string, error) {
		ev, err := st.Evidence(ctx, c.ID)
		if err != nil {
			return IntegrityErrEvidenceRead, err
		}
		ci, done, err := verifyCycle(ctx, &rep, c, ev, initialDeferred, src, o, start)
		if err != nil {
			return IntegrityErrSourceRead, err
		}
		incomplete = incomplete || !done
		unverifiable = unverifiable || ci.Unverifiable
		rep.Cycles = append(rep.Cycles, ci)
		return "", nil
	}
	if last != nil {
		var init []string
		if beforeLast != nil {
			init = beforeLast.Deferred
		}
		// the cycle before the last one is the only source of its initial deferrals (cycle 1 starts with none)
		if code, err := check(last, initOrUnknown(init, beforeLast != nil || last.Cycle == 1)); err != nil {
			return fail(code, err)
		}
	}
	if running != nil {
		var init []string
		known := running.Cycle == 1
		if last != nil {
			init, known = last.Deferred, true
		}
		if code, err := check(running, initOrUnknown(init, known)); err != nil {
			return fail(code, err)
		}
	}
	switch {
	case len(rep.Problems) > 0:
		rep.Status = IntegrityInconsistent
	case incomplete && ctx.Err() != nil:
		rep.Status, rep.Error = IntegrityIncomplete, IntegrityErrInterrupted
	case incomplete:
		rep.Status, rep.Error = IntegrityIncomplete, IntegrityErrTimeBudget
	case unverifiable:
		rep.Status = IntegrityUnverifiable
	default:
		rep.Status = IntegrityVerified
	}
	rep.Elapsed = o.Now().Sub(start).Round(time.Millisecond).String()
	if running == nil {
		return rep, nil // nothing to record on (no cycle has run)
	}
	return rep, st.RecordIntegrity(context.WithoutCancel(ctx), o.Detector, o.Scope, rep)
}

// initOrUnknown: nil slice = unknown initial deferrals (the deferral check is skipped); empty = known empty.
func initOrUnknown(init []string, known bool) []string {
	if !known {
		return nil
	}
	if init == nil {
		return []string{}
	}
	return init
}

func verifyCycle(ctx context.Context, rep *IntegrityReport, c *CycleRecord, ev []Evidence, initialDeferred []string, src Source,
	o IntegrityOptions, start time.Time) (CycleIntegrity, bool, error) {
	ci := CycleIntegrity{Cycle: c.Cycle, Status: c.Status, Batches: c.Batches, Evidenced: len(ev)}
	add := func(p Problem) { p.Cycle = c.Cycle; rep.problem(p) }
	// 1. the evidence must number 1..batches. A cycle whose evidence starts later (or is absent although batches ran)
	// began before the evidence existed (migration 20261027090000): only its evidenced part is checked.
	ci.Unverifiable = (len(ev) == 0 && c.Batches > 0) || (len(ev) > 0 && ev[0].Seq != 1)
	first := int64(1)
	if len(ev) > 0 {
		first = ev[0].Seq
	}
	for i := range ev {
		if ev[i].Seq != first+int64(i) {
			add(Problem{Code: ProblemBatchMissing, Seq: first + int64(i), Count: 1, Detail: "evidence rows are not numbered consecutively"})
			break
		}
	}
	if len(ev) > 0 && ev[len(ev)-1].Seq != c.Batches {
		add(Problem{Code: ProblemBatchMissing, Count: int(c.Batches - ev[len(ev)-1].Seq), Detail: "the checkpoint counts batches that are not evidenced"})
	}
	// 2. the chain: each batch starts where the previous one ended and ends at its last key; keys strictly ascending
	// inside (from, to]; no key twice in a cycle
	seen := map[string]bool{}
	var evidenced int64
	prevTo := ""
	for i, e := range ev {
		if (i > 0 || !ci.Unverifiable) && e.From != prevTo {
			add(Problem{Code: ProblemChainBroken, Seq: e.Seq, Count: 1, Detail: "batch starts at " + orStart(e.From) + ", previous ended at " + orStart(prevTo)})
		}
		prevTo = e.To
		var bad, dup []string
		prev := e.From
		for _, k := range e.Keys {
			if (prev != "" && k <= prev) || k > e.To {
				bad = append(bad, k)
			}
			prev = k
			if seen[k] {
				dup = append(dup, k)
			}
			seen[k] = true
		}
		if (len(e.Keys) > 0 && e.Keys[len(e.Keys)-1] != e.To) || (len(e.Keys) == 0 && e.To != e.From) {
			bad = append(bad, orStart(e.To))
		}
		if len(bad) > 0 {
			add(Problem{Code: ProblemKeyOutOfRange, Seq: e.Seq, Count: len(bad), Keys: bad})
		}
		if len(dup) > 0 {
			add(Problem{Code: ProblemDuplicateKey, Seq: e.Seq, Count: len(dup), Keys: dup})
		}
		evidenced += int64(len(e.Keys))
	}
	ci.EvidencedKeys = int(evidenced)
	if len(ev) > 0 && prevTo != c.Cursor {
		add(Problem{Code: ProblemCursorBeyond, Count: 1, Detail: "cursor " + orStart(c.Cursor) + ", evidence ends at " + orStart(prevTo)})
	}
	if len(ev) == 0 && c.Batches == 0 && c.Cursor != "" {
		add(Problem{Code: ProblemCursorBeyond, Count: 1, Detail: "cursor " + c.Cursor + " without any batch"})
	}
	if !ci.Unverifiable && evidenced != c.Examined {
		add(Problem{Code: ProblemExaminedMismatch, Count: int(c.Examined - evidenced), Detail: "examined differs from the evidenced keys"})
	}
	if c.Status == "COMPLETED" && !ci.Unverifiable && (len(ev) == 0 || !ev[len(ev)-1].Completing) {
		add(Problem{Code: ProblemCompletionMissing, Count: 1, Detail: "the cycle is COMPLETED but no evidenced batch completed it"})
	}
	// 3. deferrals: initial ∪ added − resolved, batch by batch, must equal the checkpoint's carried set
	if initialDeferred != nil && !ci.Unverifiable {
		set := map[string]bool{}
		for _, k := range initialDeferred {
			set[k] = true
		}
		for _, e := range ev {
			for _, k := range e.Resolved {
				delete(set, k)
			}
			for _, k := range e.Deferred {
				set[k] = true
			}
		}
		if missing, extra := diff(set, c.Deferred); len(missing)+len(extra) > 0 {
			add(Problem{Code: ProblemDeferralsMismatch, Count: len(missing) + len(extra), Keys: append(missing, extra...),
				Detail: "carried deferrals differ from the evidence (dropped or added outside a batch)"})
		}
	}
	// 4. independent re-read: every in-scope record unchanged since before its batch must be in the batch's evidence
	for _, e := range ev {
		if o.Now().Sub(start) >= o.MaxRuntime {
			return ci, false, nil
		}
		if e.From == e.To && len(e.Keys) == 0 {
			ci.ReReadBatches++
			continue // an empty final batch covers no range
		}
		stamps, err := src(ctx, e.From, e.To)
		if err != nil {
			return ci, false, err
		}
		in := make(map[string]bool, len(e.Keys))
		for _, k := range e.Keys {
			in[k] = true
		}
		var skipped []string
		for _, s := range stamps {
			if in[s.Key] {
				continue
			}
			if s.Modified.Before(e.ExaminedAt.Add(-o.Margin)) {
				skipped = append(skipped, s.Key)
			} else {
				ci.LateChanges++
			}
		}
		if len(skipped) > 0 {
			sort.Strings(skipped)
			add(Problem{Code: ProblemRecordSkipped, Seq: e.Seq, Count: len(skipped), Keys: skipped,
				Detail: "in-scope records unchanged since before the batch are not in its evidence"})
		}
		ci.ReReadBatches++
	}
	// 5. a completed cycle's tail: nothing in scope beyond its final cursor that existed, unchanged, before it completed
	if c.Status == "COMPLETED" && c.CompletedAt != nil && !ci.Unverifiable {
		if o.Now().Sub(start) >= o.MaxRuntime {
			return ci, false, nil
		}
		stamps, err := src(ctx, c.Cursor, "")
		if err != nil {
			return ci, false, err
		}
		var early []string
		for _, s := range stamps {
			if s.Modified.Before(c.CompletedAt.Add(-o.Margin)) {
				early = append(early, s.Key)
			} else {
				ci.TailLateChanges++
			}
		}
		ci.TailReRead = true
		if len(early) > 0 {
			sort.Strings(early)
			add(Problem{Code: ProblemCompletedEarly, Count: len(early), Keys: early,
				Detail: "in-scope records beyond the final cursor existed, unchanged, before the cycle completed"})
		}
	}
	return ci, true, nil
}

func orStart(k string) string {
	if k == "" {
		return "(start)"
	}
	return k
}

func diff(want map[string]bool, got []string) (missing, extra []string) {
	g := map[string]bool{}
	for _, k := range got {
		g[k] = true
		if !want[k] {
			extra = append(extra, k)
		}
	}
	for k := range want {
		if !g[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// MaxKey is the greatest uuid: the upper bound of an unbounded Source range ("" to).
const MaxKey = "ffffffff-ffff-ffff-ffff-ffffffffffff"

// Until is the upper bound of a Source range: to itself, or MaxKey when unbounded.
func Until(to string) string {
	if to == "" {
		return MaxKey
	}
	return to
}
