// Package reconsweep is the coverage half of FundZim's internal reconciliations (remediation of KI-S19-09,
// ADR-052). The windowed passes (the newest N records) detect recent problems quickly but guarantee nothing about
// older records; a sweep walks every record of a detector's scope in primary-key order, a bounded batch at a time,
// and persists its position (a checkpoint) only after every record of the batch has been examined and its findings
// recorded. When the walk reaches the end of the key range the cycle completes and the next one starts from the
// beginning, so every record is re-examined in its current state once per cycle.
//
// Guarantees (proved by the tests in this package and the integration tests):
//   - a record that exists when a cycle starts is examined in that cycle, or carried as a deferral until it is;
//   - a change committed at time t is examined by the end of the first cycle that starts after t (each cycle reads
//     every record after its start); no timestamp ordering or precision is relied on — keys only;
//   - the checkpoint never moves past an unexamined record: it is advanced by compare-and-swap on the checkpoint's
//     version after the batch, so a crash, error, timeout or lost race leaves it at the last fully examined batch
//     and the batch is examined again (examination is idempotent: findings are deduplicated by their owners);
//   - concurrent runs are safe: both may examine the same batch (deduplicated findings), only one advances;
//   - a record the detector may not judge yet (inside its grace period, or a required external check could not be
//     made) is never silently passed: it is carried in the checkpoint as a deferral, retried at the start of later
//     runs, and counted (oldest deferral age and the reasons of the run are visible);
//   - deferrals never stop the walk (remediation phase 3, F-R2-02): a run retries at most half its record budget of
//     them, oldest-untried first (a retried deferral that stays deferred goes to the back), and stops retrying at the
//     time budget; a batch also stops at the time budget after at least one record, and the checkpoint then advances
//     to the last examined key only — so slow or failing checks of some records never prevent progress on the rest;
//   - every run's outcome is recorded on the RUNNING checkpoint (RecordAttempt), so a sweep that fails or stops
//     making progress is visible (Health) even when nothing advances (F-R2-03).
//
// The package owns no table: each module stores its checkpoints in its own table through Store (SQLStore keeps the
// SQL in one place; the module names its own table).
package reconsweep

import (
	"context"
	"errors"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Checkpoint is the persisted position of one detector's current cycle.
type Checkpoint struct {
	ID         string
	Detector   string
	Scope      string
	Cycle      int64
	StartedAt  time.Time
	Cursor     string // last examined key ("" = nothing examined in this cycle yet)
	Examined   int64
	Exceptions int64
	Batches    int64
	Deferred   []Deferral
	Version    int64
}

// Deferral is a record that could not be judged yet (e.g. changed within the detector's grace period).
type Deferral struct {
	Key   string
	Since time.Time
}

// Coverage is what the checkpoints prove about a detector.
type Coverage struct {
	Detector string `json:"detector"`
	Scope    string `json:"scope"`
	// LastCompletedCycleStartedAt is the coverage watermark: every record that existed then was examined after it
	// (or is still deferred). Nil: no cycle has completed yet — coverage is NOT established.
	LastCompletedCycleStartedAt *time.Time `json:"last_completed_cycle_started_at"`
	LastCompletedAt             *time.Time `json:"last_completed_at"`
	LastCompletedExamined       int64      `json:"last_completed_examined"`
	RunningCycle                int64      `json:"running_cycle"`
	RunningStartedAt            *time.Time `json:"running_started_at"`
	RunningExamined             int64      `json:"running_examined"`
	DeferredOutstanding         int        `json:"deferred_outstanding"`
	OldestDeferralSince         *time.Time `json:"oldest_deferral_since"`
	// The running cycle's last advance and the recorded outcome of the latest run (F-R2-03; Health).
	LastAdvancedAt     *time.Time `json:"last_advanced_at"`
	LastAttemptAt      *time.Time `json:"last_attempt_at"`
	LastAttemptOutcome string     `json:"last_attempt_outcome"`
	LastAttemptError   string     `json:"last_attempt_error,omitempty"`
	FailedAttempts     int        `json:"failed_attempts"`
	// The latest integrity verification of the running and last completed cycle (remediation phase 4; Verify).
	IntegrityStatus    string     `json:"integrity_status,omitempty"`
	IntegrityCheckedAt *time.Time `json:"integrity_checked_at,omitempty"` // start of the latest verification (any outcome)
	// IntegrityVerifiedAt is the start of the latest SUCCESSFUL verification (remediation phase 6); IntegrityError the
	// reason code of a latest verification that did not complete.
	IntegrityVerifiedAt *time.Time `json:"integrity_verified_at,omitempty"`
	IntegrityError      string     `json:"integrity_error,omitempty"`
	// IntegrityUnverifiedSince: the start of the first verification that did not succeed since the last success.
	IntegrityUnverifiedSince *time.Time `json:"integrity_unverified_since,omitempty"`
	// IntegrityFailures counts consecutive verifications that did not succeed (0 after a success);
	// IntegrityLastFailureAt and IntegrityLastFailure ("STATUS" or "STATUS:CODE") keep the latest one after a later
	// success, so a recovery never erases an earlier failure.
	IntegrityFailures      int        `json:"integrity_failures"`
	IntegrityLastFailureAt *time.Time `json:"integrity_last_failure_at,omitempty"`
	IntegrityLastFailure   string     `json:"integrity_last_failure,omitempty"`
}

// Store persists checkpoints (one RUNNING checkpoint per detector and scope).
type Store interface {
	// Active returns the RUNNING checkpoint, creating cycle 1 when the detector has none.
	Active(ctx context.Context, detector, scope string) (Checkpoint, error)
	// Advance replaces cur with next when cur.Version is still current (false: another run advanced it first), and
	// stores ev — the evidence of the batch that justifies the move — in the same transaction (remediation phase 4: the
	// database refuses a position change without matching evidence). With complete, cur's cycle is closed and the next
	// cycle opens empty, carrying next.Deferred.
	Advance(ctx context.Context, cur, next Checkpoint, complete bool, runID string, ev Evidence) (bool, error)
	Coverage(ctx context.Context, detector, scope string) (Coverage, error)
	// RecordAttempt stores the outcome of one run (stop reason, error, consecutive failures) on the RUNNING checkpoint
	// without changing its position.
	RecordAttempt(ctx context.Context, detector, scope string, rep Report) error
}

// Evidence is the durable record of one checkpoint advance (remediation phase 4): the keys the batch examined, in key
// order, between the old cursor (From, exclusive) and the new one (To), the keys it newly carried as deferrals, and the
// carried deferrals it examined and resolved. The database checks every position change against it (constraint
// trigger); Verify compares it independently with the detector's scope.
type Evidence struct {
	Seq        int64     // = the checkpoint's batch counter after the advance
	From, To   string    // "" = before the first key
	Keys       []string  // examined by the walk in this batch (deferred ones included)
	Deferred   []string  // keys of this batch newly carried as deferrals
	Resolved   []string  // carried deferrals examined and no longer deferred
	Completing bool      // the batch closed the cycle
	ExaminedAt time.Time // set by the store (database time of the advance)
}

// Outcome is the result of examining one record.
type Outcome struct {
	Deferred   bool   // not judged yet: carried and retried
	Reason     string // why it was deferred (bounded vocabulary of the detector, e.g. GRACE_PERIOD); counted per run
	OutOfScope bool   // no longer in the detector's scope (counted as examined: nothing to judge)
	Exceptions int    // new findings recorded for it
}

// Examine judges one record in its current state and records its findings (idempotently).
type Examine func(ctx context.Context, key string) (Outcome, error)

// Next returns up to limit keys of the scope strictly after the given key ("" = from the start), in key order.
type Next func(ctx context.Context, after string, limit int) ([]string, error)

// Stop reasons.
const (
	StopCycleCompleted = "CYCLE_COMPLETED"
	StopRecordBudget   = "RECORD_BUDGET"
	StopTimeBudget     = "TIME_BUDGET"
	StopConflict       = "CHECKPOINT_CONFLICT" // another run advanced the checkpoint first
	StopDeferralLimit  = "DEFERRAL_LIMIT"      // fail closed: the checkpoint is not advanced past more deferrals
	StopError          = "ERROR"
)

// Config bounds the runs of one detector. Zero values take the module's defaults (engineering defaults, not capacity
// targets: no volume or freshness target is approved — owner decision D-05/D-06).
type Config struct {
	Scope       string        // DefaultScope in production; tests use their own scope
	BatchSize   int           // keys per checkpoint
	MaxRecords  int           // records examined per run (deferral retries included; at most half of it on retries)
	MaxDuration time.Duration // no new record starts after this (at least one record per run is examined)
	MaxDeferred int           // deferrals a checkpoint may carry
}

// Or returns c with its zero fields taken from d.
func (c Config) Or(d Config) Config {
	if c.Scope == "" {
		c.Scope = d.Scope
	}
	if c.BatchSize <= 0 {
		c.BatchSize = d.BatchSize
	}
	if c.MaxRecords <= 0 {
		c.MaxRecords = d.MaxRecords
	}
	if c.MaxDuration <= 0 {
		c.MaxDuration = d.MaxDuration
	}
	if c.MaxDeferred <= 0 {
		c.MaxDeferred = d.MaxDeferred
	}
	return c
}

// Options bound one run.
type Options struct {
	Config
	Detector string
	RunID    string
	Now      func() time.Time
}

// Report is the evidence of one run.
type Report struct {
	Detector          string    `json:"detector"`
	Scope             string    `json:"scope"`
	Cycle             int64     `json:"cycle"`
	CycleStartedAt    time.Time `json:"cycle_started_at"`
	CursorFrom        string    `json:"cursor_from"`
	CursorTo          string    `json:"cursor_to"`
	Examined          int       `json:"examined"`
	OutOfScope        int       `json:"out_of_scope"`
	DeferredNew       int       `json:"deferred_new"`
	DeferralsRetried  int       `json:"deferrals_retried"`
	DeferralsResolved int       `json:"deferrals_resolved"`
	// DeferralsNotRetried were left for later runs because the run's retry share or time budget was used up.
	DeferralsNotRetried int `json:"deferrals_not_retried"`
	// DeferredReasons counts this run's deferral decisions (new and retried-still-deferred) by the detector's reason.
	DeferredReasons map[string]int `json:"deferred_reasons,omitempty"`
	DeferredCarried int            `json:"deferred_carried"`
	OldestDeferral  *time.Time     `json:"oldest_deferral_since"`
	Exceptions      int            `json:"exceptions"`
	Batches         int            `json:"batches"`
	Advanced        bool           `json:"checkpoint_advanced"`
	CycleCompleted  bool           `json:"cycle_completed"`
	StoppedBy       string         `json:"stopped_by"`
	Error           string         `json:"error,omitempty"`
}

// ErrDeferralLimit is returned (with a report) when a batch would leave more deferrals than MaxDeferred.
var ErrDeferralLimit = errors.New("reconsweep: deferral limit reached; checkpoint not advanced")

// Run advances one detector's sweep by up to MaxRecords records. It returns the report and the first error; on
// error the checkpoint stays at the last batch that was fully examined. The outcome is recorded on the checkpoint
// in every case (non-cancelled context); a failure to record it is returned when the run itself succeeded.
func Run(ctx context.Context, st Store, o Options, next Next, examine Examine) (Report, error) {
	o = o.withDefaults()
	rep, err := runSweep(ctx, st, o, next, examine)
	if aerr := st.RecordAttempt(context.WithoutCancel(ctx), o.Detector, o.Scope, rep); aerr != nil && err == nil {
		return rep, aerr
	}
	return rep, err
}

func (r *Report) deferredFor(reason string) {
	if reason == "" {
		reason = "UNSPECIFIED"
	}
	if r.DeferredReasons == nil {
		r.DeferredReasons = map[string]int{}
	}
	r.DeferredReasons[reason]++
}

func runSweep(ctx context.Context, st Store, o Options, next Next, examine Examine) (Report, error) {
	rep := Report{Detector: o.Detector, Scope: o.Scope}
	start := o.Now()
	cp, err := st.Active(ctx, o.Detector, o.Scope)
	if err != nil {
		return rep.fail(err)
	}
	rep.Cycle, rep.CycleStartedAt, rep.CursorFrom, rep.CursorTo = cp.Cycle, cp.StartedAt, cp.Cursor, cp.Cursor
	retry := true
	for {
		if rep.Examined+rep.DeferralsRetried >= o.MaxRecords {
			rep.StoppedBy = StopRecordBudget
			break
		}
		if o.Now().Sub(start) >= o.MaxDuration {
			rep.StoppedBy = StopTimeBudget
			break
		}
		nx := cp
		nx.Deferred = append([]Deferral(nil), cp.Deferred...)
		var batchEx int
		// 1. deferrals carried from earlier batches (first batch of a run only), bounded so they never stop the walk:
		// at most half the record budget and the time budget; untried ones keep their place at the front, retried
		// ones that stay deferred go to the back (each keeps its first deferral time)
		var resolved, newlyDeferred []string
		if retry && len(nx.Deferred) > 0 {
			share := max(1, o.MaxRecords/2)
			var untried, kept []Deferral
			for i, d := range nx.Deferred {
				if rep.DeferralsRetried >= share || (rep.DeferralsRetried > 0 && o.Now().Sub(start) >= o.MaxDuration) {
					untried = append(untried, nx.Deferred[i:]...)
					break
				}
				out, err := examine(ctx, d.Key)
				if err != nil {
					return rep.fail(err)
				}
				rep.DeferralsRetried++
				batchEx += out.Exceptions // findings recorded while still deferred (e.g. internal checks) count too
				if out.Deferred {
					kept = append(kept, d)
					rep.deferredFor(out.Reason)
					continue
				}
				rep.DeferralsResolved++
				resolved = append(resolved, d.Key)
			}
			rep.DeferralsNotRetried = len(untried)
			nx.Deferred = append(untried, kept...)
		}
		retry = false
		// 2. the next keys after the checkpoint
		limit := min(o.BatchSize, o.MaxRecords-rep.Examined-rep.DeferralsRetried)
		if limit <= 0 {
			limit = 1 // the deferral retry used the budget; still move the walk forward by at least one key
		}
		keys, err := next(ctx, cp.Cursor, limit)
		if err != nil {
			return rep.fail(err)
		}
		var newly, outScope int
		fetched, truncated := len(keys), false
		for i, k := range keys {
			// the time budget also holds inside a batch (slow external checks): stop after at least one record and
			// advance only to the last examined key
			if (i > 0 || rep.Examined > 0) && o.Now().Sub(start) >= o.MaxDuration {
				keys, truncated = keys[:i], true
				break
			}
			out, err := examine(ctx, k)
			if err != nil {
				return rep.fail(err)
			}
			switch {
			case out.Deferred:
				rep.deferredFor(out.Reason)
				if !hasKey(nx.Deferred, k) {
					nx.Deferred = append(nx.Deferred, Deferral{Key: k, Since: o.Now()})
					newly++
					newlyDeferred = append(newlyDeferred, k)
				}
			case out.OutOfScope:
				outScope++
			}
			batchEx += out.Exceptions
		}
		if len(nx.Deferred) > o.MaxDeferred {
			rep.StoppedBy, rep.Error = StopDeferralLimit, ErrDeferralLimit.Error()
			return rep, ErrDeferralLimit
		}
		complete := !truncated && fetched < limit
		if len(keys) > 0 {
			nx.Cursor = keys[len(keys)-1]
		}
		nx.Examined += int64(len(keys))
		nx.Exceptions += int64(batchEx)
		nx.Batches++
		ev := Evidence{Seq: nx.Batches, From: cp.Cursor, To: nx.Cursor, Keys: append([]string(nil), keys...), Deferred: newlyDeferred,
			Resolved: resolved, Completing: complete}
		ok, err := st.Advance(ctx, cp, nx, complete, o.RunID, ev)
		if err != nil {
			return rep.fail(err)
		}
		// the batch was examined whether or not this run won the checkpoint
		rep.Examined += len(keys)
		rep.OutOfScope += outScope
		rep.DeferredNew += newly
		rep.Exceptions += batchEx
		rep.Batches++
		if !ok {
			rep.StoppedBy = StopConflict
			break
		}
		rep.Advanced, rep.CursorTo = true, nx.Cursor
		if complete {
			rep.CycleCompleted, rep.StoppedBy = true, StopCycleCompleted
			cp = nx
			break // at most one cycle per run: the next run starts the next cycle
		}
		nx.Version = cp.Version + 1
		cp = nx
		if truncated {
			rep.StoppedBy = StopTimeBudget
			break
		}
	}
	rep.DeferredCarried = len(cp.Deferred)
	for _, d := range cp.Deferred {
		if rep.OldestDeferral == nil || d.Since.Before(*rep.OldestDeferral) {
			t := d.Since
			rep.OldestDeferral = &t
		}
	}
	return rep, nil
}

func (r Report) fail(err error) (Report, error) {
	r.StoppedBy, r.Error = StopError, err.Error()
	return r, err
}

func hasKey(ds []Deferral, k string) bool {
	for _, d := range ds {
		if d.Key == k {
			return true
		}
	}
	return false
}

func (o Options) withDefaults() Options {
	o.Config = o.Config.Or(Config{Scope: DefaultScope, BatchSize: 500, MaxRecords: 5000, MaxDuration: 2 * time.Minute, MaxDeferred: 10000})
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// DefaultScope is the production scope; tests use their own so they never move the worker's checkpoints.
const DefaultScope = "default"

// After is the key a keyset query starts after: the nil UUID before the first batch (UUIDv7 keys are never nil), so
// the query can always use the primary-key range condition `id > $1::uuid`.
func After(cursor string) string {
	if cursor == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return cursor
}

// Metric is a detector's coverage as an operations-portal figure: the seconds since the start of its last completed
// cycle (every record that existed then has been examined since, or is deferred). No completed cycle: no value and
// status UNKNOWN — coverage is not established. Outstanding deferrals make it DEGRADED. No freshness target is
// configured (D-06), so the figure is never reported HEALTHY on age alone.
func (c Coverage) Metric(module, key, label, perm, source string, now time.Time) opsview.Metric {
	m := opsview.Metric{Key: key, Label: label, Module: module, Nature: opsview.TestOnly, Status: opsview.StatusUnknown, Permission: perm, Source: source}
	if c.LastCompletedCycleStartedAt != nil {
		m.Value = opsview.Int64(int64(now.Sub(*c.LastCompletedCycleStartedAt).Seconds()))
		if c.DeferredOutstanding > 0 {
			m.Status = opsview.Degraded
		}
	}
	return m
}
