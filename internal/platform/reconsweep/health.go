package reconsweep

import (
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Sweep health (remediation phase 3, F-R2-03): a stalled, blocked or failing sweep must be visible even though it
// advances nothing. Health is computed from the checkpoint rows only — the cycle's start, its last advance, its
// deferrals and the recorded outcome of the latest run (RecordAttempt) — so the API and the operations scan see the
// same state as the worker. It is a signal for people, never an alert delivery: FundZim has no paging channel (D-10).

// Health states of a detector's current cycle, in the order they are decided.
const (
	HealthIntegrityFailure   = "INTEGRITY_FAILURE"    // the checkpoint's coverage claim is contradicted by its evidence (Verify)
	HealthNotStarted         = "NOT_STARTED"          // no cycle has run yet: coverage is not established
	HealthBlockedOnDeferrals = "BLOCKED_ON_DEFERRALS" // the deferral limit stopped the walk, or a record has waited too long
	HealthFailing            = "FAILING"              // the latest runs ended in errors
	HealthStalled            = "STALLED"              // no checkpoint progress for longer than StallAfter
	HealthProgressingSlowly  = "PROGRESSING_SLOWLY"   // advancing, but the cycle has run longer than SlowAfter
	HealthProgressing        = "PROGRESSING"          // advancing within the engineering defaults
)

// Outcome of the last completed cycle, combined with the module's open discrepancies.
const (
	LastCycleNone          = "NONE"
	LastCycleClean         = "COMPLETED_CLEAN"
	LastCycleDiscrepancies = "COMPLETED_WITH_DISCREPANCIES"
)

// Thresholds are ENGINEERING DEFAULTS, not approved targets: no stall duration, freshness or alerting threshold has
// been approved (owner decisions D-06 coverage freshness, D-10 alerting). They are chosen against the 15-minute
// schedule: one hour without progress means about four scheduled runs advanced nothing.
type Thresholds struct {
	StallAfter       time.Duration // no checkpoint advance for longer than this: STALLED
	SlowAfter        time.Duration // a cycle running longer than this while advancing: PROGRESSING_SLOWLY
	DeferralAgeLimit time.Duration // a deferral older than this: BLOCKED_ON_DEFERRALS
	FailingAfter     int           // this many consecutive failed runs: FAILING
}

// DefaultThresholds are the engineering defaults (see Thresholds).
var DefaultThresholds = Thresholds{StallAfter: time.Hour, SlowAfter: 24 * time.Hour, DeferralAgeLimit: 24 * time.Hour, FailingAfter: 3}

// Health is one detector's sweep state.
type Health struct {
	Detector                 string     `json:"detector"`
	Scope                    string     `json:"scope"`
	State                    string     `json:"state"`
	Reason                   string     `json:"reason"`
	LastCycle                string     `json:"last_cycle"`
	OpenDiscrepancies        int        `json:"open_discrepancies"`
	SecondsSinceProgress     *int64     `json:"seconds_since_progress"`
	RunningCycleAgeSeconds   *int64     `json:"running_cycle_age_seconds"`
	FailedAttempts           int        `json:"failed_attempts"`
	LastAttemptAt            *time.Time `json:"last_attempt_at"`
	LastAttemptOutcome       string     `json:"last_attempt_outcome"`
	DeferredOutstanding      int        `json:"deferred_outstanding"`
	OldestDeferralAgeSeconds *int64     `json:"oldest_deferral_age_seconds"`
	IntegrityStatus          string     `json:"integrity_status,omitempty"`
}

func secs(d time.Duration) *int64 { v := int64(d.Seconds()); return &v }

// Health classifies the coverage. maxDeferred is the detector's MaxDeferred; openDiscrepancies is the module's count of
// unresolved findings (it decides LastCycle, never State: open findings are the sweep doing its job).
func (c Coverage) Health(now time.Time, t Thresholds, maxDeferred, openDiscrepancies int) Health {
	h := Health{Detector: c.Detector, Scope: c.Scope, LastCycle: LastCycleNone, OpenDiscrepancies: openDiscrepancies,
		FailedAttempts: c.FailedAttempts, LastAttemptAt: c.LastAttemptAt, LastAttemptOutcome: c.LastAttemptOutcome,
		DeferredOutstanding: c.DeferredOutstanding, IntegrityStatus: c.IntegrityStatus}
	if c.LastCompletedCycleStartedAt != nil {
		h.LastCycle = LastCycleClean
		if openDiscrepancies > 0 {
			h.LastCycle = LastCycleDiscrepancies
		}
	}
	if c.RunningStartedAt == nil && c.LastCompletedCycleStartedAt == nil {
		h.State, h.Reason = HealthNotStarted, "no sweep cycle has run: coverage is not established"
		return h
	}
	var progress time.Time
	switch {
	case c.RunningStartedAt != nil:
		progress = *c.RunningStartedAt
		h.RunningCycleAgeSeconds = secs(now.Sub(*c.RunningStartedAt))
	case c.LastCompletedAt != nil:
		progress = *c.LastCompletedAt
	}
	if c.LastAdvancedAt != nil && c.LastAdvancedAt.After(progress) {
		progress = *c.LastAdvancedAt
	}
	since := now.Sub(progress)
	h.SecondsSinceProgress = secs(since)
	var deferralAge time.Duration
	if c.OldestDeferralSince != nil {
		deferralAge = now.Sub(*c.OldestDeferralSince)
		h.OldestDeferralAgeSeconds = secs(deferralAge)
	}
	switch {
	case c.IntegrityStatus == IntegrityInconsistent:
		h.State, h.Reason = HealthIntegrityFailure, "the checkpoint's coverage claim is contradicted by its evidence or the source data: investigate (nothing is changed automatically)"
	case c.LastAttemptOutcome == StopDeferralLimit || (maxDeferred > 0 && c.DeferredOutstanding >= maxDeferred):
		h.State, h.Reason = HealthBlockedOnDeferrals, "the deferral limit stops the walk: the checkpoint cannot advance past more unjudged records"
	case t.FailingAfter > 0 && c.FailedAttempts >= t.FailingAfter:
		h.State, h.Reason = HealthFailing, "the latest runs ended in errors (last_attempt_error on the checkpoint)"
	case since > t.StallAfter:
		h.State, h.Reason = HealthStalled, "the checkpoint has not advanced for longer than the stall threshold (engineering default)"
	case c.OldestDeferralSince != nil && deferralAge > t.DeferralAgeLimit:
		h.State, h.Reason = HealthBlockedOnDeferrals, "a deferred record has waited longer than the deferral age threshold (engineering default)"
	case h.RunningCycleAgeSeconds != nil && now.Sub(*c.RunningStartedAt) > t.SlowAfter:
		h.State, h.Reason = HealthProgressingSlowly, "advancing, but the cycle has run longer than the slow threshold (engineering default)"
	default:
		h.State, h.Reason = HealthProgressing, "advancing"
	}
	return h
}

// Metric is the health as an operations-portal figure: value = seconds since the checkpoint last progressed, state =
// the health state. FAILING is UNAVAILABLE; STALLED, BLOCKED_ON_DEFERRALS and PROGRESSING_SLOWLY are DEGRADED;
// otherwise UNKNOWN — never HEALTHY, because no freshness target is approved (D-06).
func (h Health) Metric(module, key, label, perm, source string) opsview.Metric {
	m := opsview.Metric{Key: key, Label: label, Module: module, Nature: opsview.TestOnly, Status: opsview.StatusUnknown, Permission: perm,
		Source: source, Value: h.SecondsSinceProgress, State: h.State}
	switch h.State {
	case HealthFailing, HealthIntegrityFailure:
		m.Status = opsview.Unavailable
	case HealthStalled, HealthBlockedOnDeferrals, HealthProgressingSlowly:
		m.Status = opsview.Degraded
	}
	return m
}
