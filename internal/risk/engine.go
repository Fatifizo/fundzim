package risk

// The Stage 13 rule engine (ADR-045 §3): configurable rule versions are data — a JSON condition tree over whitelisted
// condition types — evaluated by a pure function over a subject's signals inside a bounded window and a small risk
// state. No regular expression, SQL or executable code is ever accepted. The same rules, signals, state and time always
// give the same result. A score is a routing aid for humans, never proof of fraud.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EngineVersion is recorded with every rule version and evaluation.
const EngineVersion = "risk-engine-v2"

// Limits of a rule definition (ADR-045 §3).
const (
	MaxWindow       = 90 * 24 * time.Hour
	MaxDepth        = 3
	MaxConditions   = 12
	MaxSequenceLen  = 5
	MaxSignalTypes  = 10
	MaxSignalsInput = 10_000 // the evaluation loads at most this many signals (bounded query)
)

// Classifications and their engineering-default bands (overridable by approved risk.limits rows).
const (
	ClassLow      = "LOW"
	ClassMedium   = "MEDIUM"
	ClassHigh     = "HIGH"
	ClassCritical = "CRITICAL"
)

// DefaultBands are the engineering defaults: MEDIUM from 25, HIGH from 50, CRITICAL from 75 (not regulatory values).
var DefaultBands = Bands{Medium: 25, High: 50, Critical: 75, Source: "ENGINEERING_DEFAULT"}

// Bands are classification thresholds on the 0–100 score.
type Bands struct {
	Medium, High, Critical int
	Source                 string
}

// Actions, in increasing order of protection.
const (
	ActAllow           = "ALLOW"
	ActMonitor         = "ALLOW_WITH_MONITORING"
	ActReview          = "REVIEW_REQUIRED"
	ActTemporaryHold   = "TEMPORARY_HOLD"
	ActEscalate        = "ESCALATE"
	ActBlock           = "BLOCK"
	maxScoreV2         = 100
	conditionsMaxValue = 1_000_000_000_000_000
)

var actionRank = map[string]int{ActAllow: 0, ActMonitor: 1, ActReview: 2, ActTemporaryHold: 3, ActEscalate: 4, ActBlock: 5}

// Facts a rule may read from a signal (ids, codes and amounts only; never personal data).
var ruleFacts = map[string]bool{"amount_minor": true, "campaign_id": true, "destination_id": true, "currency": true, "reason_code": true,
	"title_hash": true, "ua_hash": true, "payout_request_id": true, "beneficiary_id": true, "method": true}

// State attributes a rule may read.
var stateAttrs = map[string]bool{"ACTIVE_HOLDS": true, "OPEN_CASES": true, "SCREENING_STATUS": true, "PRIOR_CLASSIFICATION": true}

var ops = map[string]bool{">=": true, ">": true, "<=": true, "<": true, "=": true, "!=": true}

// Condition is one node of a rule definition. Exactly one field is set.
type Condition struct {
	All           []Condition      `json:"all,omitempty"`
	Any           []Condition      `json:"any,omitempty"`
	SignalCount   *SignalCountCond `json:"signal_count,omitempty"`
	DistinctCount *DistinctCond    `json:"distinct_count,omitempty"`
	FactCompare   *FactCompareCond `json:"fact_compare,omitempty"`
	Sequence      *SequenceCond    `json:"sequence,omitempty"`
	State         *StateCond       `json:"state,omitempty"`
}

// SignalCountCond: how many signals of the types were observed within the window (counts and velocity).
type SignalCountCond struct {
	SignalTypes   []string `json:"signal_types"`
	WindowSeconds int64    `json:"window_seconds"`
	Op            string   `json:"op"`
	Value         int64    `json:"value"`
}

// DistinctCond: how many distinct values of a fact the signals carry within the window.
type DistinctCond struct {
	SignalTypes   []string `json:"signal_types"`
	Fact          string   `json:"fact"`
	WindowSeconds int64    `json:"window_seconds"`
	Op            string   `json:"op"`
	Value         int64    `json:"value"`
}

// FactCompareCond: at least one signal of the types within the window has the fact compared true (numbers compare
// numerically; strings only with = and !=).
type FactCompareCond struct {
	SignalTypes   []string `json:"signal_types"`
	Fact          string   `json:"fact"`
	WindowSeconds int64    `json:"window_seconds"`
	Op            string   `json:"op"`
	Value         any      `json:"value"`
}

// SequenceCond: the signal types occurred in this order (each after the previous) within the window — cross-event
// correlation by the subject's stable identifier (never by IP address).
type SequenceCond struct {
	SignalTypes   []string `json:"signal_types"`
	WindowSeconds int64    `json:"window_seconds"`
}

// StateCond compares a risk-state attribute.
type StateCond struct {
	Attribute string `json:"attribute"`
	Op        string `json:"op"`
	Value     any    `json:"value"`
}

// Definition is a rule version's condition tree.
type Definition struct {
	When Condition `json:"when"`
}

// ErrRule marks an invalid rule definition.
var ErrRule = errors.New("risk: invalid rule definition")

func ruleErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRule}, a...)...)
}

// ParseDefinition decodes and validates a definition (unknown fields refused).
func ParseDefinition(raw []byte, knownSignal func(string) bool) (Definition, error) {
	var d Definition
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return Definition{}, ruleErr("%v", err)
	}
	if err := d.Validate(knownSignal); err != nil {
		return Definition{}, err
	}
	return d, nil
}

// Validate checks bounds, whitelists and shape.
func (d Definition) Validate(knownSignal func(string) bool) error {
	n := 0
	return validateCond(d.When, 1, &n, knownSignal)
}

func validateTypes(ts []string, known func(string) bool) error {
	if len(ts) == 0 || len(ts) > MaxSignalTypes {
		return ruleErr("1 to %d signal types", MaxSignalTypes)
	}
	for _, t := range ts {
		if known != nil && !known(t) {
			return ruleErr("unknown signal type %q", t)
		}
	}
	return nil
}

func validateWindow(w int64) error {
	if w <= 0 || time.Duration(w)*time.Second > MaxWindow {
		return ruleErr("window_seconds must be between 1 and %d", int64(MaxWindow/time.Second))
	}
	return nil
}

func validateCond(c Condition, depth int, n *int, known func(string) bool) error {
	if depth > MaxDepth {
		return ruleErr("nesting deeper than %d", MaxDepth)
	}
	set := 0
	for _, b := range []bool{c.All != nil, c.Any != nil, c.SignalCount != nil, c.DistinctCount != nil, c.FactCompare != nil, c.Sequence != nil, c.State != nil} {
		if b {
			set++
		}
	}
	if set != 1 {
		return ruleErr("each condition has exactly one kind")
	}
	if c.All != nil || c.Any != nil {
		kids := c.All
		if c.Any != nil {
			kids = c.Any
		}
		if len(kids) == 0 {
			return ruleErr("empty group")
		}
		for _, k := range kids {
			if err := validateCond(k, depth+1, n, known); err != nil {
				return err
			}
		}
		return nil
	}
	*n++
	if *n > MaxConditions {
		return ruleErr("more than %d conditions", MaxConditions)
	}
	switch {
	case c.SignalCount != nil:
		x := c.SignalCount
		if err := validateTypes(x.SignalTypes, known); err != nil {
			return err
		}
		if err := validateWindow(x.WindowSeconds); err != nil {
			return err
		}
		if !ops[x.Op] || x.Value < 0 || x.Value > MaxSignalsInput {
			return ruleErr("signal_count op/value")
		}
	case c.DistinctCount != nil:
		x := c.DistinctCount
		if err := validateTypes(x.SignalTypes, known); err != nil {
			return err
		}
		if err := validateWindow(x.WindowSeconds); err != nil {
			return err
		}
		if !ruleFacts[x.Fact] || !ops[x.Op] || x.Value < 0 || x.Value > MaxSignalsInput {
			return ruleErr("distinct_count fact/op/value")
		}
	case c.FactCompare != nil:
		x := c.FactCompare
		if err := validateTypes(x.SignalTypes, known); err != nil {
			return err
		}
		if err := validateWindow(x.WindowSeconds); err != nil {
			return err
		}
		if !ruleFacts[x.Fact] || !ops[x.Op] {
			return ruleErr("fact_compare fact/op")
		}
		if err := validateValue(x.Op, x.Value); err != nil {
			return err
		}
	case c.Sequence != nil:
		x := c.Sequence
		if len(x.SignalTypes) < 2 || len(x.SignalTypes) > MaxSequenceLen {
			return ruleErr("a sequence has 2 to %d steps", MaxSequenceLen)
		}
		if err := validateTypes(x.SignalTypes, known); err != nil {
			return err
		}
		if err := validateWindow(x.WindowSeconds); err != nil {
			return err
		}
	case c.State != nil:
		x := c.State
		if !stateAttrs[x.Attribute] || !ops[x.Op] {
			return ruleErr("state attribute/op")
		}
		if err := validateValue(x.Op, x.Value); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(op string, v any) error {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		if err != nil || n < 0 || n > conditionsMaxValue {
			return ruleErr("numeric values are non-negative integers")
		}
	case float64:
		if t != float64(int64(t)) || t < 0 {
			return ruleErr("numeric values are non-negative integers")
		}
	case int, int64:
	case string:
		if (op != "=" && op != "!=") || len(t) > 64 || t == "" {
			return ruleErr("strings compare with = or != only, 1 to 64 characters")
		}
	default:
		return ruleErr("value must be an integer or a string")
	}
	return nil
}

// ---- evaluation --------------------------------------------------------------------------------------------------

// SignalFact is a signal as the engine sees it.
type SignalFact struct {
	ID         string
	Type       string
	ObservedAt time.Time
	Confidence int
	Facts      map[string]any
}

// State is the subject's risk state at evaluation time.
type State struct {
	ActiveHolds         int64
	OpenCases           int64
	ScreeningStatus     string
	PriorClassification string
}

// RuleInput is an ACTIVE rule version.
type RuleInput struct {
	VersionID    string
	RuleCode     string
	RuleName     string
	Definition   Definition
	Contribution int
	Severity     string
	Action       string
	HoldType     string
	HoldDuration time.Duration
	Priority     string
}

// RuleOutcome explains one rule's evaluation.
type RuleOutcome struct {
	VersionID    string   `json:"rule_version_id"`
	RuleCode     string   `json:"rule_code"`
	Fired        bool     `json:"fired"`
	Contribution int      `json:"contribution"`
	Action       string   `json:"action"`
	Conditions   []string `json:"conditions"`
	SignalIDs    []string `json:"signal_ids"`
}

// Factor is one line of the explanation ("Recent payout destination change: +30").
type Factor struct {
	RuleCode     string `json:"rule_code"`
	RuleName     string `json:"rule_name"`
	Contribution int    `json:"contribution"`
	Severity     string `json:"severity"`
	Summary      string `json:"summary"`
}

// EngineResult is the engine's output.
type EngineResult struct {
	Score             int
	Classification    string
	BandSource        string
	Confidence        int
	RecommendedAction string
	Outcomes          []RuleOutcome
	Factors           []Factor
	Fired             []RuleInput
	InputsHash        [32]byte
}

// EvaluateRules runs the rules over the signals and state at `at` (pure, deterministic).
func EvaluateRules(at time.Time, signals []SignalFact, state State, rules []RuleInput, bands Bands) EngineResult {
	sigs := append([]SignalFact(nil), signals...)
	sort.Slice(sigs, func(i, j int) bool {
		if !sigs[i].ObservedAt.Equal(sigs[j].ObservedAt) {
			return sigs[i].ObservedAt.Before(sigs[j].ObservedAt)
		}
		return sigs[i].ID < sigs[j].ID
	})
	rs := append([]RuleInput(nil), rules...)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].RuleCode != rs[j].RuleCode {
			return rs[i].RuleCode < rs[j].RuleCode
		}
		return rs[i].VersionID < rs[j].VersionID
	})
	ev := EngineResult{Confidence: 100, RecommendedAction: ActAllow, BandSource: bands.Source}
	total := 0
	for _, r := range rs {
		ctx := &evalCtx{at: at, sigs: sigs, state: state, matched: map[string]bool{}}
		fired := ctx.eval(r.Definition.When)
		o := RuleOutcome{VersionID: r.VersionID, RuleCode: r.RuleCode, Fired: fired, Action: r.Action, Conditions: ctx.notes, SignalIDs: []string{}}
		if fired {
			o.Contribution = r.Contribution
			total += r.Contribution
			for _, s := range sigs {
				if ctx.matched[s.ID] {
					o.SignalIDs = append(o.SignalIDs, s.ID)
					if s.Confidence < ev.Confidence {
						ev.Confidence = s.Confidence
					}
				}
			}
			if actionRank[r.Action] > actionRank[ev.RecommendedAction] {
				ev.RecommendedAction = r.Action
			}
			ev.Fired = append(ev.Fired, r)
			ev.Factors = append(ev.Factors, Factor{RuleCode: r.RuleCode, RuleName: r.RuleName, Contribution: r.Contribution, Severity: r.Severity,
				Summary: strings.Join(ctx.notes, "; ")})
		}
		ev.Outcomes = append(ev.Outcomes, o)
	}
	if total > maxScoreV2 {
		total = maxScoreV2
	}
	ev.Score = total
	ev.Classification = Classify(total, bands)
	// a high classification always reaches a human, whatever the rules recommend
	floor := map[string]string{ClassHigh: ActReview, ClassCritical: ActEscalate}[ev.Classification]
	if floor != "" && actionRank[floor] > actionRank[ev.RecommendedAction] {
		ev.RecommendedAction = floor
	}
	if ev.Factors == nil {
		ev.Factors = []Factor{}
	}
	ev.InputsHash = inputsHash(at, sigs, state, rs, bands)
	return ev
}

// Classify maps a score to a classification.
func Classify(score int, b Bands) string {
	switch {
	case score >= b.Critical:
		return ClassCritical
	case score >= b.High:
		return ClassHigh
	case score >= b.Medium:
		return ClassMedium
	}
	return ClassLow
}

// ValidBands reports whether bands are usable (0 < medium < high < critical ≤ 100).
func ValidBands(b Bands) bool {
	return b.Medium > 0 && b.Medium < b.High && b.High < b.Critical && b.Critical <= 100
}

type evalCtx struct {
	at      time.Time
	sigs    []SignalFact
	state   State
	matched map[string]bool
	notes   []string
}

func (c *evalCtx) inWindow(types []string, window int64) []SignalFact {
	from := c.at.Add(-time.Duration(window) * time.Second)
	want := map[string]bool{}
	for _, t := range types {
		want[t] = true
	}
	var out []SignalFact
	for _, s := range c.sigs {
		if want[s.Type] && !s.ObservedAt.Before(from) && !s.ObservedAt.After(c.at) {
			out = append(out, s)
		}
	}
	return out
}

func (c *evalCtx) eval(cond Condition) bool {
	switch {
	case cond.All != nil:
		ok := true
		for _, k := range cond.All {
			if !c.eval(k) { // evaluate every child so the explanation is complete and deterministic
				ok = false
			}
		}
		return ok
	case cond.Any != nil:
		ok := false
		for _, k := range cond.Any {
			if c.eval(k) {
				ok = true
			}
		}
		return ok
	case cond.SignalCount != nil:
		x := cond.SignalCount
		got := c.inWindow(x.SignalTypes, x.WindowSeconds)
		ok := compareInt(int64(len(got)), x.Op, x.Value)
		c.note(ok, "%s count %d %s %d within %s", strings.Join(x.SignalTypes, "|"), len(got), x.Op, x.Value, dur(x.WindowSeconds))
		if ok {
			c.mark(got)
		}
		return ok
	case cond.DistinctCount != nil:
		x := cond.DistinctCount
		got := c.inWindow(x.SignalTypes, x.WindowSeconds)
		seen := map[string]bool{}
		var used []SignalFact
		for _, s := range got {
			if v, ok := factString(s.Facts[x.Fact]); ok && !seen[v] {
				seen[v] = true
				used = append(used, s)
			}
		}
		ok := compareInt(int64(len(seen)), x.Op, x.Value)
		c.note(ok, "distinct %s over %s: %d %s %d within %s", x.Fact, strings.Join(x.SignalTypes, "|"), len(seen), x.Op, x.Value, dur(x.WindowSeconds))
		if ok {
			c.mark(used)
		}
		return ok
	case cond.FactCompare != nil:
		x := cond.FactCompare
		var hit []SignalFact
		for _, s := range c.inWindow(x.SignalTypes, x.WindowSeconds) {
			if compareAny(s.Facts[x.Fact], x.Op, x.Value) {
				hit = append(hit, s)
			}
		}
		ok := len(hit) > 0
		c.note(ok, "%s %s %v on %s within %s (%d matching)", x.Fact, x.Op, x.Value, strings.Join(x.SignalTypes, "|"), dur(x.WindowSeconds), len(hit))
		if ok {
			c.mark(hit)
		}
		return ok
	case cond.Sequence != nil:
		x := cond.Sequence
		got := c.inWindow(x.SignalTypes, x.WindowSeconds)
		step := 0
		var used []SignalFact
		for _, s := range got { // chronological: the earliest match of each step, in order
			if step < len(x.SignalTypes) && s.Type == x.SignalTypes[step] {
				used = append(used, s)
				step++
			}
		}
		ok := step == len(x.SignalTypes)
		c.note(ok, "sequence %s within %s: %d of %d steps", strings.Join(x.SignalTypes, " → "), dur(x.WindowSeconds), step, len(x.SignalTypes))
		if ok {
			c.mark(used)
		}
		return ok
	case cond.State != nil:
		x := cond.State
		var v any
		switch x.Attribute {
		case "ACTIVE_HOLDS":
			v = c.state.ActiveHolds
		case "OPEN_CASES":
			v = c.state.OpenCases
		case "SCREENING_STATUS":
			v = c.state.ScreeningStatus
		case "PRIOR_CLASSIFICATION":
			v = c.state.PriorClassification
		}
		ok := compareAny(v, x.Op, x.Value)
		c.note(ok, "state %s (%v) %s %v", x.Attribute, v, x.Op, x.Value)
		return ok
	}
	return false
}

func (c *evalCtx) mark(sigs []SignalFact) {
	for _, s := range sigs {
		c.matched[s.ID] = true
	}
}

func (c *evalCtx) note(ok bool, format string, a ...any) {
	mark := "✗"
	if ok {
		mark = "✓"
	}
	c.notes = append(c.notes, mark+" "+fmt.Sprintf(format, a...))
}

func dur(seconds int64) string { return (time.Duration(seconds) * time.Second).String() }

func compareInt(a int64, op string, b int64) bool {
	switch op {
	case ">=":
		return a >= b
	case ">":
		return a > b
	case "<=":
		return a <= b
	case "<":
		return a < b
	case "=":
		return a == b
	case "!=":
		return a != b
	}
	return false
}

func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case float64:
		if t == float64(int64(t)) {
			return int64(t), true
		}
	case int:
		return int64(t), true
	case int64:
		return t, true
	case string:
		n, err := strconv.ParseInt(t, 10, 64) // amounts travel as digit strings (MONEY.md)
		return n, err == nil
	}
	return 0, false
}

func factString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case json.Number:
		return t.String(), true
	case float64, int, int64:
		return fmt.Sprint(t), true
	}
	return "", false
}

func compareAny(got any, op string, want any) bool {
	if ws, ok := want.(string); ok {
		gs, _ := factString(got)
		if op == "=" {
			return gs == ws
		}
		if op == "!=" {
			return gs != ws
		}
		return false
	}
	w, ok := asInt(want)
	if !ok {
		return false
	}
	g, ok := asInt(got)
	if !ok {
		return false
	}
	return compareInt(g, op, w)
}

func inputsHash(at time.Time, sigs []SignalFact, st State, rules []RuleInput, b Bands) [32]byte {
	type sig struct {
		ID, Type, At string
	}
	type rule struct {
		Version string
		Def     Definition
		Contrib int
		Action  string
	}
	doc := struct {
		Engine string
		At     string
		Sigs   []sig
		State  State
		Rules  []rule
		Bands  Bands
	}{Engine: EngineVersion, At: at.UTC().Format(time.RFC3339Nano), State: st, Bands: b}
	for _, s := range sigs {
		doc.Sigs = append(doc.Sigs, sig{s.ID, s.Type, s.ObservedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, r := range rules {
		doc.Rules = append(doc.Rules, rule{r.VersionID, r.Definition, r.Contribution, r.Action})
	}
	b2, _ := json.Marshal(doc)
	return sha256.Sum256(b2)
}

// MaxWindowOf returns the longest window a set of rules reads (bounds the signal query).
func MaxWindowOf(rules []RuleInput) time.Duration {
	var maxW int64
	var walk func(Condition)
	walk = func(c Condition) {
		for _, k := range append(append([]Condition{}, c.All...), c.Any...) {
			walk(k)
		}
		for _, w := range []int64{winOf(c)} {
			if w > maxW {
				maxW = w
			}
		}
	}
	for _, r := range rules {
		walk(r.Definition.When)
	}
	return time.Duration(maxW) * time.Second
}

func winOf(c Condition) int64 {
	switch {
	case c.SignalCount != nil:
		return c.SignalCount.WindowSeconds
	case c.DistinctCount != nil:
		return c.DistinctCount.WindowSeconds
	case c.FactCompare != nil:
		return c.FactCompare.WindowSeconds
	case c.Sequence != nil:
		return c.Sequence.WindowSeconds
	}
	return 0
}
