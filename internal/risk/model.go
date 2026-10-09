package risk

import (
	"errors"
	"sort"
	"strings"
)

// The risk model is a versioned, explainable rule-weighted sum (aml-risk-framework §3.2): each rule counts
// signals of given types for one subject inside a time window, fires when the count reaches its threshold
// and then contributes its weight. Weights, thresholds, the window and the rating bands are risk.limits rows
// (INTERNAL_RISK); the code references limit keys only. The model output is a routing aid for humans: a score
// is never proof of fraud, and no rule or decision is ever named as a fraud finding.

// ModelVersion and PolicyVersion are recorded with every assessment and decision. Changing a rule, a key or
// the decision policy requires a new version.
const (
	ModelVersion  = "risk-model-v1"
	PolicyVersion = "risk-policy-v1"
	MaxScore      = 1000
)

// Ratings (ADR-034 §2: internal categories, not legal ones).
const (
	RatingLow        = "LOW"
	RatingStandard   = "STANDARD"
	RatingEnhanced   = "ENHANCED"
	RatingRestricted = "RESTRICTED"
)

// Decisions only route a subject to humans.
const (
	DecisionNoAction     = "NO_ACTION"
	DecisionManualReview = "MANUAL_REVIEW"
	DecisionEscalate     = "ESCALATE"
)

// ReasonLimitsUnavailable is the fail-closed reason: the model could not run without effective limits.
const ReasonLimitsUnavailable = "MODEL_LIMITS_UNAVAILABLE"

// Signal types recorded from domain events.
const (
	SigDocumentRejected     = "DOCUMENT_REJECTED"
	SigKYCRejected          = "KYC_REJECTED"
	SigKYBRejected          = "KYB_REJECTED"
	SigDuplicateIdentity    = "DUPLICATE_IDENTITY"
	SigBeneficiaryRejected  = "BENEFICIARY_REJECTED"
	SigBeneficiaryEscalated = "BENEFICIARY_ESCALATED"
	SigDestinationChanged   = "DESTINATION_CHANGED"
	SigDestinationRejected  = "DESTINATION_REJECTED"
)

// Limit keys of model v1 (seeded as INTERNAL_RISK MIGRATION_BASELINE rows by migration 20261009150500).
const (
	KeySignalWindow   = "risk.model_v1.signal_window"
	KeyStandardMin    = "risk.rating.standard_min"
	KeyEnhancedMin    = "risk.rating.enhanced_min"
	KeyRestrictedMin  = "risk.rating.restricted_min"
	keyWeightPrefix   = "risk.model_v1.weight."
	keyThreshldPrefix = "risk.model_v1.threshold."
)

// Rule is one model rule. A rule without a threshold key fires on the first matching signal.
type Rule struct {
	Code         string
	Signals      []string
	WeightKey    string
	ThresholdKey string
}

// RulesV1 is model v1. Codes describe observations, never conclusions.
var RulesV1 = []Rule{
	{Code: "KYC_REJECTED", Signals: []string{SigKYCRejected, SigKYBRejected}, WeightKey: keyWeightPrefix + "kyc_rejected"},
	{Code: "DOCUMENT_REJECTED_REPEAT", Signals: []string{SigDocumentRejected}, WeightKey: keyWeightPrefix + "document_rejected_repeat",
		ThresholdKey: keyThreshldPrefix + "document_rejected_repeat"},
	{Code: "DUPLICATE_IDENTITY", Signals: []string{SigDuplicateIdentity}, WeightKey: keyWeightPrefix + "duplicate_identity"},
	{Code: "DESTINATION_CHANGED_REPEAT", Signals: []string{SigDestinationChanged}, WeightKey: keyWeightPrefix + "destination_changed_repeat",
		ThresholdKey: keyThreshldPrefix + "destination_changed_repeat"},
	{Code: "DESTINATION_REJECTED", Signals: []string{SigDestinationRejected}, WeightKey: keyWeightPrefix + "destination_rejected"},
	{Code: "BENEFICIARY_REJECTED", Signals: []string{SigBeneficiaryRejected}, WeightKey: keyWeightPrefix + "beneficiary_rejected"},
	{Code: "BENEFICIARY_ESCALATED", Signals: []string{SigBeneficiaryEscalated}, WeightKey: keyWeightPrefix + "beneficiary_escalated"},
}

// RequiredKeys lists every limit key model v1 needs; missing any of them fails closed.
func RequiredKeys() []string {
	keys := []string{KeySignalWindow, KeyStandardMin, KeyEnhancedMin, KeyRestrictedMin}
	for _, r := range RulesV1 {
		keys = append(keys, r.WeightKey)
		if r.ThresholdKey != "" {
			keys = append(keys, r.ThresholdKey)
		}
	}
	return keys
}

// Limits is a snapshot of effective limit values (key -> value) and the limit version ids they came from.
type Limits struct {
	Values map[string]int64
	IDs    map[string]string
}

// ErrLimitsUnavailable means a required limit is missing or the limit set is inconsistent (fail closed).
var ErrLimitsUnavailable = errors.New("risk: required limits unavailable or inconsistent")

func (l Limits) get(key string) (int64, bool) {
	v, ok := l.Values[key]
	return v, ok
}

// RuleResult explains one rule's contribution.
type RuleResult struct {
	Rule      string `json:"rule"`
	Observed  int    `json:"observed"`
	Threshold int64  `json:"threshold"`
	Weight    int64  `json:"weight"`
	Fired     bool   `json:"fired"`
}

// Result is the model output for one subject.
type Result struct {
	Score             int
	Rating            string
	ContributingRules []string
	Explanation       []RuleResult
	LimitIDs          []string
}

// Evaluate scores signal counts (signal type -> count inside the window) with model v1. It is pure and
// deterministic: the same limits and counts always give the same result.
func Evaluate(lim Limits, counts map[string]int) (Result, error) {
	var res Result
	used := map[string]bool{}
	use := func(key string) (int64, error) {
		v, ok := lim.get(key)
		if !ok || v < 0 {
			return 0, ErrLimitsUnavailable
		}
		used[key] = true
		return v, nil
	}
	total := int64(0)
	for _, r := range RulesV1 {
		w, err := use(r.WeightKey)
		if err != nil {
			return Result{}, err
		}
		th := int64(1)
		if r.ThresholdKey != "" {
			if th, err = use(r.ThresholdKey); err != nil {
				return Result{}, err
			}
			if th < 1 {
				return Result{}, ErrLimitsUnavailable
			}
		}
		n := 0
		for _, sig := range r.Signals {
			n += counts[sig]
		}
		fired := int64(n) >= th
		res.Explanation = append(res.Explanation, RuleResult{Rule: r.Code, Observed: n, Threshold: th, Weight: w, Fired: fired})
		if fired {
			res.ContributingRules = append(res.ContributingRules, r.Code)
			total += w
		}
	}
	if total > MaxScore {
		total = MaxScore
	}
	res.Score = int(total)
	std, err := use(KeyStandardMin)
	if err != nil {
		return Result{}, err
	}
	enh, err := use(KeyEnhancedMin)
	if err != nil {
		return Result{}, err
	}
	rst, err := use(KeyRestrictedMin)
	if err != nil {
		return Result{}, err
	}
	if !(std <= enh && enh <= rst) {
		return Result{}, ErrLimitsUnavailable
	}
	switch s := int64(res.Score); {
	case s >= rst:
		res.Rating = RatingRestricted
	case s >= enh:
		res.Rating = RatingEnhanced
	case s >= std:
		res.Rating = RatingStandard
	default:
		res.Rating = RatingLow
	}
	if _, ok := lim.get(KeySignalWindow); ok {
		used[KeySignalWindow] = true
	}
	for k := range used {
		if id := lim.IDs[k]; id != "" {
			res.LimitIDs = append(res.LimitIDs, id)
		}
	}
	sort.Strings(res.LimitIDs)
	if res.ContributingRules == nil {
		res.ContributingRules = []string{}
	}
	return res, nil
}

// Decide is decision policy v1: ENHANCED routes to manual review, RESTRICTED recommends a compliance case,
// anything lower needs no action. The reason codes describe the rating, never a conclusion about the subject.
func Decide(rating string) (decision, reason string) {
	switch rating {
	case RatingRestricted:
		return DecisionEscalate, "RATING_RESTRICTED"
	case RatingEnhanced:
		return DecisionManualReview, "RATING_ENHANCED"
	case RatingLow, RatingStandard:
		return DecisionNoAction, "RATING_" + rating
	}
	// unknown rating: route to a human (fail closed)
	return DecisionManualReview, "RATING_UNKNOWN"
}

// mostRestrictive combines effective values of one key across limit types (REGULATORY / PROVIDER /
// INTERNAL_RISK, aml-risk-framework §5 rule 1). For the model "more restrictive" means "routes to humans
// sooner": the larger weight or window, the smaller threshold or rating floor.
func mostRestrictive(key string, a, b int64) int64 {
	larger := strings.Contains(key, ".weight.") || key == KeySignalWindow
	if larger == (a > b) {
		return a
	}
	return b
}
