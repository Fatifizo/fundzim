package risk

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func sig(id, typ string, ago time.Duration, facts map[string]any) SignalFact {
	return SignalFact{ID: id, Type: typ, ObservedAt: t0.Add(-ago), Confidence: 100, Facts: facts}
}

func def(t *testing.T, js string) Definition {
	t.Helper()
	d, err := ParseDefinition([]byte(js), func(s string) bool { return SignalMetaOf(s).Known })
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	return d
}

func rule(code string, d Definition, contribution int, action string) RuleInput {
	return RuleInput{VersionID: "v-" + code, RuleCode: code, RuleName: code, Definition: d, Contribution: contribution, Severity: "MEDIUM",
		Action: action, Priority: "P3"}
}

func TestConditionKinds(t *testing.T) {
	sigs := []SignalFact{
		sig("1", SigMFAChanged, 3*time.Hour, nil),
		sig("2", SigDestinationChanged, 2*time.Hour, map[string]any{"destination_id": "d1"}),
		sig("3", SigWithdrawalRequested, time.Hour, map[string]any{"amount_minor": "250000", "campaign_id": "c1"}),
		sig("4", SigDonationIntentCreated, 10*time.Minute, map[string]any{"campaign_id": "c1"}),
		sig("5", SigDonationIntentCreated, 9*time.Minute, map[string]any{"campaign_id": "c2"}),
		sig("6", SigDonationIntentCreated, 8*time.Minute, map[string]any{"campaign_id": "c2"}),
		sig("7", SigDonationIntentCreated, 48*time.Hour, map[string]any{"campaign_id": "c3"}), // outside a 1-day window
	}
	cases := []struct {
		name string
		js   string
		want bool
	}{
		{"velocity: 3 intents in 1 hour", `{"when":{"signal_count":{"signal_types":["DONATION_INTENT_CREATED"],"window_seconds":3600,"op":">=","value":3}}}`, true},
		{"velocity: 4 intents in 1 day (one is older)", `{"when":{"signal_count":{"signal_types":["DONATION_INTENT_CREATED"],"window_seconds":86400,"op":">=","value":4}}}`, false},
		{"distinct campaigns per donor", `{"when":{"distinct_count":{"signal_types":["DONATION_INTENT_CREATED"],"fact":"campaign_id","window_seconds":86400,"op":">=","value":2}}}`, true},
		{"large withdrawal (digit-string amount)", `{"when":{"fact_compare":{"signal_types":["WITHDRAWAL_REQUESTED"],"fact":"amount_minor","window_seconds":86400,"op":">","value":100000}}}`, true},
		{"account takeover sequence", `{"when":{"sequence":{"signal_types":["MFA_CHANGED","DESTINATION_CHANGED","WITHDRAWAL_REQUESTED"],"window_seconds":86400}}}`, true},
		{"sequence in the wrong order", `{"when":{"sequence":{"signal_types":["WITHDRAWAL_REQUESTED","MFA_CHANGED"],"window_seconds":86400}}}`, false},
		{"state", `{"when":{"state":{"attribute":"ACTIVE_HOLDS","op":">=","value":1}}}`, true},
		{"all", `{"when":{"all":[{"state":{"attribute":"SCREENING_STATUS","op":"=","value":"POTENTIAL_MATCH"}},{"signal_count":{"signal_types":["MFA_CHANGED"],"window_seconds":86400,"op":">=","value":1}}]}}`, false},
		{"any", `{"when":{"any":[{"state":{"attribute":"SCREENING_STATUS","op":"=","value":"POTENTIAL_MATCH"}},{"signal_count":{"signal_types":["MFA_CHANGED"],"window_seconds":86400,"op":">=","value":1}}]}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := EvaluateRules(t0, sigs, State{ActiveHolds: 1}, []RuleInput{rule("R", def(t, c.js), 30, ActReview)}, DefaultBands)
			if res.Outcomes[0].Fired != c.want {
				t.Fatalf("fired %v, want %v: %v", res.Outcomes[0].Fired, c.want, res.Outcomes[0].Conditions)
			}
		})
	}
}

func TestDefinitionValidation(t *testing.T) {
	known := func(s string) bool { return SignalMetaOf(s).Known }
	bad := []string{
		`{"when":{"signal_count":{"signal_types":["NOT_A_SIGNAL"],"window_seconds":60,"op":">=","value":1}}}`,                      // unknown signal
		`{"when":{"signal_count":{"signal_types":["MFA_CHANGED"],"window_seconds":99999999,"op":">=","value":1}}}`,                 // window > 90 days
		`{"when":{"signal_count":{"signal_types":["MFA_CHANGED"],"window_seconds":60,"op":"~","value":1}}}`,                        // op
		`{"when":{"fact_compare":{"signal_types":["MFA_CHANGED"],"fact":"password","window_seconds":60,"op":"=","value":"x"}}}`,    // fact not whitelisted
		`{"when":{"fact_compare":{"signal_types":["MFA_CHANGED"],"fact":"campaign_id","window_seconds":60,"op":">","value":"x"}}}`, // string with >
		`{"when":{"fact_compare":{"signal_types":["MFA_CHANGED"],"fact":"campaign_id","window_seconds":60,"op":"=","value":1.5}}}`, // float
		`{"when":{"regex":".*"}}`,     // unknown kind
		`{"when":{"sql":"SELECT 1"}}`, // never accepted
		`{"when":{}}`,                 // empty
		`{"when":{"all":[]}}`,         // empty group
		`{"when":{"all":[{"all":[{"all":[{"state":{"attribute":"OPEN_CASES","op":">=","value":1}}]}]}]}}`, // depth 4
		`{"when":{"state":{"attribute":"PASSWORD","op":"=","value":"x"}}}`,                                // attribute
		`{"when":{"sequence":{"signal_types":["MFA_CHANGED"],"window_seconds":60}}}`,                      // 1-step sequence
		`{"when":{"state":{"attribute":"OPEN_CASES","op":">=","value":1}},"extra":1}`,                     // unknown top-level field
	}
	for i, js := range bad {
		if _, err := ParseDefinition([]byte(js), known); !errors.Is(err, ErrRule) {
			t.Errorf("case %d must be refused: %v", i, err)
		}
	}
	var many []string
	for i := 0; i < MaxConditions+1; i++ {
		many = append(many, `{"state":{"attribute":"OPEN_CASES","op":">=","value":1}}`)
	}
	if _, err := ParseDefinition([]byte(`{"when":{"any":[`+strings.Join(many, ",")+`]}}`), known); !errors.Is(err, ErrRule) {
		t.Errorf("more than %d conditions must be refused: %v", MaxConditions, err)
	}
}

func TestScoringExplanationAndActions(t *testing.T) {
	sigs := []SignalFact{
		sig("a", SigDestinationChanged, time.Hour, nil),
		sig("b", SigWithdrawalRequested, 30*time.Minute, map[string]any{"amount_minor": "500000"}),
	}
	sigs[0].Confidence = 70
	rules := []RuleInput{
		rule("RECENT_DESTINATION_CHANGE", def(t, `{"when":{"signal_count":{"signal_types":["DESTINATION_CHANGED"],"window_seconds":86400,"op":">=","value":1}}}`), 30, ActMonitor),
		rule("UNUSUAL_WITHDRAWAL_AMOUNT", def(t, `{"when":{"fact_compare":{"signal_types":["WITHDRAWAL_REQUESTED"],"fact":"amount_minor","window_seconds":86400,"op":">","value":100000}}}`), 25, ActReview),
		rule("NEVER_FIRES", def(t, `{"when":{"signal_count":{"signal_types":["PAYMENT_FAILED"],"window_seconds":86400,"op":">=","value":5}}}`), 90, ActBlock),
	}
	res := EvaluateRules(t0, sigs, State{}, rules, DefaultBands)
	if res.Score != 55 || res.Classification != ClassHigh || res.RecommendedAction != ActReview || res.Confidence != 70 {
		t.Fatalf("score %d class %s action %s confidence %d", res.Score, res.Classification, res.RecommendedAction, res.Confidence)
	}
	if len(res.Factors) != 2 || res.Factors[0].RuleCode != "RECENT_DESTINATION_CHANGE" || res.Factors[0].Contribution != 30 ||
		!strings.Contains(res.Factors[0].Summary, "✓") {
		t.Fatalf("explanation: %+v", res.Factors)
	}
	// a CRITICAL classification always escalates to a human even if the rules only ask for monitoring
	rules[0].Contribution, rules[0].Action = 80, ActMonitor
	res = EvaluateRules(t0, sigs, State{}, rules[:1], DefaultBands)
	if res.Classification != ClassCritical || res.RecommendedAction != ActEscalate {
		t.Fatalf("critical floor: %s %s", res.Classification, res.RecommendedAction)
	}
	// the score is capped at 100; nothing fired ⇒ ALLOW, LOW
	rules[0].Contribution = 100
	rules[1].Contribution = 100
	if res = EvaluateRules(t0, sigs, State{}, rules, DefaultBands); res.Score != 100 {
		t.Fatalf("cap: %d", res.Score)
	}
	if res = EvaluateRules(t0, nil, State{}, rules, DefaultBands); res.Score != 0 || res.Classification != ClassLow || res.RecommendedAction != ActAllow {
		t.Fatalf("empty: %+v", res)
	}
	if !ValidBands(DefaultBands) || ValidBands(Bands{Medium: 50, High: 40, Critical: 90}) {
		t.Fatal("band validation")
	}
}

// ---- property-based tests (reproducible seeds) ------------------------------------------------------------------

func randSignals(r *rand.Rand) []SignalFact {
	types := []string{SigDonationIntentCreated, SigPaymentFailed, SigWithdrawalRequested, SigDestinationChanged, SigMFAChanged, SigSignedIn}
	n := r.IntN(60)
	out := make([]SignalFact, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, SignalFact{ID: fmt.Sprintf("s%03d", i), Type: types[r.IntN(len(types))], ObservedAt: t0.Add(-time.Duration(r.IntN(72*3600)) * time.Second),
			Confidence: 50 + r.IntN(51), Facts: map[string]any{"amount_minor": fmt.Sprint(r.IntN(1_000_000)), "campaign_id": fmt.Sprintf("c%d", r.IntN(5))}})
	}
	return out
}

func randRules(t *testing.T, r *rand.Rand) []RuleInput {
	tmpl := []string{
		`{"when":{"signal_count":{"signal_types":["DONATION_INTENT_CREATED"],"window_seconds":%d,"op":">=","value":%d}}}`,
		`{"when":{"distinct_count":{"signal_types":["DONATION_INTENT_CREATED"],"fact":"campaign_id","window_seconds":%d,"op":">=","value":%d}}}`,
		`{"when":{"fact_compare":{"signal_types":["WITHDRAWAL_REQUESTED"],"fact":"amount_minor","window_seconds":%d,"op":">","value":%d}}}`,
		`{"when":{"any":[{"signal_count":{"signal_types":["PAYMENT_FAILED"],"window_seconds":%d,"op":">=","value":%d}},{"sequence":{"signal_types":["MFA_CHANGED","DESTINATION_CHANGED"],"window_seconds":86400}}]}}`,
	}
	acts := []string{ActMonitor, ActReview, ActTemporaryHold, ActEscalate, ActBlock}
	n := 1 + r.IntN(5)
	out := []RuleInput{}
	for i := 0; i < n; i++ {
		js := fmt.Sprintf(tmpl[r.IntN(len(tmpl))], 3600*(1+r.IntN(72)), r.IntN(20))
		out = append(out, rule(fmt.Sprintf("R%d", i), def(t, js), r.IntN(101), acts[r.IntN(len(acts))]))
	}
	return out
}

func TestPropertyDeterministicBoundedAndOrderIndependent(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 20261015} {
		r := rand.New(rand.NewPCG(seed, seed*7919))
		for i := 0; i < 3_000; i++ {
			sigs, rules := randSignals(r), randRules(t, r)
			st := State{ActiveHolds: int64(r.IntN(3)), OpenCases: int64(r.IntN(3))}
			a := EvaluateRules(t0, sigs, st, rules, DefaultBands)
			// shuffle inputs: the result must not change
			sh := append([]SignalFact(nil), sigs...)
			r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
			rr := append([]RuleInput(nil), rules...)
			r.Shuffle(len(rr), func(i, j int) { rr[i], rr[j] = rr[j], rr[i] })
			b := EvaluateRules(t0, sh, st, rr, DefaultBands)
			if a.Score != b.Score || a.RecommendedAction != b.RecommendedAction || a.InputsHash != b.InputsHash || a.Confidence != b.Confidence {
				t.Fatalf("seed %d #%d: not deterministic", seed, i)
			}
			if a.Score < 0 || a.Score > 100 || a.Confidence < 0 || a.Confidence > 100 {
				t.Fatalf("seed %d #%d: out of bounds %d/%d", seed, i, a.Score, a.Confidence)
			}
			sum := 0
			for _, f := range a.Factors {
				sum += f.Contribution
			}
			if min(sum, 100) != a.Score || a.Classification != Classify(a.Score, DefaultBands) {
				t.Fatalf("seed %d #%d: score %d does not equal its explanation %d", seed, i, a.Score, sum)
			}
			if a.Classification == ClassCritical && actionRank[a.RecommendedAction] < actionRank[ActEscalate] {
				t.Fatalf("seed %d #%d: critical without escalation", seed, i)
			}
		}
	}
}

func TestEvidenceHashIsCanonical(t *testing.T) {
	type factor struct {
		Z string `json:"z"`
		A int    `json:"a"`
	}
	content := map[string]any{"score": 60, "factors": []factor{{Z: "x", A: 1}}, "amount_minor": "1000000000000000001"}
	b, h, err := canonicalJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	// what PostgreSQL jsonb returns: different key order and spacing
	stored := []byte(`{"score": 60, "factors": [{"a": 1, "z": "x"}], "amount_minor": "1000000000000000001"}`)
	if !VerifyEvidence(Evidence{Content: stored, ContentHash: hexs(h[:])}) {
		t.Fatalf("canonical %s does not verify against the stored form", b)
	}
	if VerifyEvidence(Evidence{Content: []byte(`{"score": 61, "factors": [{"a": 1, "z": "x"}], "amount_minor": "1000000000000000001"}`), ContentHash: hexs(h[:])}) {
		t.Fatal("tampered content verified")
	}
}
