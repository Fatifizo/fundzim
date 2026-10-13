package sandbox

import (
	"regexp"
	"time"
)

// Scenario names (docs/stage-8/provider-interface.md §6; payment-provider-interfaces §11.1 adapted for Stage 8).
const (
	ScSuccess             = "success"
	ScSuccessHandset      = "success_handset"
	ScDecline             = "decline"
	ScTimeoutThenSuccess  = "timeout_then_success"
	ScTimeoutThenFailure  = "timeout_then_failure"
	ScTimeoutNotCreated   = "timeout_not_created"
	ScDuplicateWebhook    = "duplicate_webhook"
	ScOutOfOrderWebhook   = "out_of_order_webhook"
	ScLateSuccessAfterExp = "late_success_after_expiry"
	ScBadSignature        = "bad_signature"
	ScReplayedOld         = "replayed_old"
	ScWebhookNever        = "webhook_never"
	ScUnmappedStatus      = "unmapped_status"
	ScAmountMismatch      = "amount_mismatch"
	ScProviderUnavailable = "provider_unavailable"
	ScProvider5xx         = "provider_5xx_then_success"
	ScExpired             = "expired"
	ScFailureAfterPending = "failure_after_pending"
	ScDelayedConfirmation = "delayed_confirmation"
	ScPending             = "pending_forever"
	ScPreviousKey         = "previous_key"
)

// Hook is one scheduled simulated webhook (or, with SignWith "none", a provider-side state change without a
// webhook).
type Hook struct {
	Status          string
	After           time.Duration
	Copies          int
	SignWith        string // current | previous | wrong | none
	TimestampOffset int    // seconds added to the signed timestamp (negative = stale)
	Amount          int64  // 0 = the payment amount
}

// Scenario is a deterministic simulator behaviour.
type Scenario struct {
	Name     string
	Handset  bool // the donor must approve on a handset (REQUIRES_ACTION)
	Webhooks []Hook
}

// scenarios by the amount's last two minor-unit digits. 09–13 (refund/dispute scenarios in the Stage 2 table)
// are not simulated in Stage 8: refunds are never executed and disputes are deferred (ADR-039 §4); those
// suffixes behave as success.
var scenarios = map[int64]Scenario{
	0:  {Name: ScSuccess, Webhooks: []Hook{{Status: "paid", After: time.Second}}},
	1:  {Name: ScSuccessHandset, Handset: true, Webhooks: []Hook{{Status: "paid", After: 5 * time.Second}}},
	2:  {Name: ScDecline},
	3:  {Name: ScTimeoutThenSuccess, Webhooks: []Hook{{Status: "paid", After: 3 * time.Second}}},
	4:  {Name: ScTimeoutThenFailure, Webhooks: []Hook{{Status: "failed", After: 3 * time.Second}}},
	5:  {Name: ScTimeoutNotCreated},
	6:  {Name: ScDuplicateWebhook, Webhooks: []Hook{{Status: "paid", After: time.Second, Copies: 3}}},
	7:  {Name: ScOutOfOrderWebhook, Webhooks: []Hook{{Status: "refunded", After: time.Second}, {Status: "paid", After: 3 * time.Second}, {Status: "pending", After: 5 * time.Second}}},
	8:  {Name: ScLateSuccessAfterExp, Webhooks: []Hook{{Status: "expired", After: time.Second}, {Status: "paid", After: 4 * time.Second}}},
	14: {Name: ScBadSignature, Webhooks: []Hook{{Status: "paid", After: time.Second, SignWith: "wrong"}, {Status: "paid", After: time.Second, SignWith: "none"}}},
	15: {Name: ScReplayedOld, Webhooks: []Hook{{Status: "paid", After: time.Second, TimestampOffset: -3600}, {Status: "paid", After: time.Second, SignWith: "none"}}},
	16: {Name: ScWebhookNever, Webhooks: []Hook{{Status: "paid", After: 2 * time.Second, SignWith: "none"}}},
	17: {Name: ScUnmappedStatus, Webhooks: []Hook{{Status: "limbo", After: time.Second}}},
	18: {Name: ScAmountMismatch, Webhooks: []Hook{{Status: "paid", After: time.Second}}},
	19: {Name: ScProviderUnavailable},
	20: {Name: ScProvider5xx, Webhooks: []Hook{{Status: "paid", After: 3 * time.Second}}},
	21: {Name: ScExpired, Webhooks: []Hook{{Status: "expired", After: time.Second}}},
	22: {Name: ScFailureAfterPending, Webhooks: []Hook{{Status: "failed", After: time.Second}}},
	23: {Name: ScDelayedConfirmation, Webhooks: []Hook{{Status: "paid", After: 12 * time.Second}}},
	24: {Name: ScPending},
	25: {Name: ScPreviousKey, Webhooks: []Hook{{Status: "paid", After: time.Second, SignWith: "previous"}}},
}

var scenarioNameRE = regexp.MustCompile(`^[a-z_]{3,40}$`)

// ScenarioFor selects the scenario: an explicit metadata name (non-production only; the sandbox never runs in
// production), else the amount's last two minor-unit digits, else success.
func ScenarioFor(amountMinor int64, explicit string) Scenario {
	if explicit != "" && scenarioNameRE.MatchString(explicit) {
		for _, sc := range scenarios {
			if sc.Name == explicit {
				return sc
			}
		}
	}
	if sc, ok := scenarios[amountMinor%100]; ok {
		return sc
	}
	return scenarios[0]
}

// Scenarios returns the scenario table keyed by amount suffix (documentation and tests).
func Scenarios() map[int64]Scenario {
	out := make(map[int64]Scenario, len(scenarios))
	for k, v := range scenarios {
		out[k] = v
	}
	return out
}
