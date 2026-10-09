package risk

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// baseline mirrors the MIGRATION_BASELINE values of migration 20261009150500 (checked below).
func baseline() Limits {
	v := map[string]int64{
		"risk.model_v1.weight.kyc_rejected": 250, "risk.model_v1.weight.document_rejected_repeat": 150,
		"risk.model_v1.threshold.document_rejected_repeat": 3, "risk.model_v1.weight.duplicate_identity": 600,
		"risk.model_v1.weight.destination_changed_repeat": 200, "risk.model_v1.threshold.destination_changed_repeat": 3,
		"risk.model_v1.weight.destination_rejected": 200, "risk.model_v1.weight.beneficiary_rejected": 200,
		"risk.model_v1.weight.beneficiary_escalated": 300, KeySignalWindow: 15552000,
		KeyStandardMin: 100, KeyEnhancedMin: 300, KeyRestrictedMin: 600,
	}
	idm := map[string]string{}
	for k := range v {
		idm[k] = "id-" + k
	}
	return Limits{Values: v, IDs: idm}
}

func TestEvaluateBaselineRatings(t *testing.T) {
	cases := []struct {
		name   string
		counts map[string]int
		score  int
		rating string
		rules  []string
	}{
		{"no signals", nil, 0, RatingLow, []string{}},
		{"one kyc rejection", map[string]int{SigKYCRejected: 1}, 250, RatingStandard, []string{"KYC_REJECTED"}},
		{"kyb counts as kyc rule", map[string]int{SigKYBRejected: 2}, 250, RatingStandard, []string{"KYC_REJECTED"}},
		{"two document rejections below threshold", map[string]int{SigDocumentRejected: 2}, 0, RatingLow, []string{}},
		{"three document rejections", map[string]int{SigDocumentRejected: 3}, 150, RatingStandard, []string{"DOCUMENT_REJECTED_REPEAT"}},
		{"kyc + destination rejected", map[string]int{SigKYCRejected: 1, SigDestinationRejected: 1}, 450, RatingEnhanced,
			[]string{"KYC_REJECTED", "DESTINATION_REJECTED"}},
		{"duplicate identity", map[string]int{SigDuplicateIdentity: 1}, 600, RatingRestricted, []string{"DUPLICATE_IDENTITY"}},
		{"everything is capped", map[string]int{SigKYCRejected: 1, SigDuplicateIdentity: 1, SigBeneficiaryEscalated: 1,
			SigDestinationChanged: 5}, 1000, RatingRestricted, []string{"KYC_REJECTED", "DUPLICATE_IDENTITY", "DESTINATION_CHANGED_REPEAT", "BENEFICIARY_ESCALATED"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Evaluate(baseline(), c.counts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Score != c.score || res.Rating != c.rating || !reflect.DeepEqual(res.ContributingRules, c.rules) {
				t.Fatalf("got score %d rating %s rules %v; want %d %s %v", res.Score, res.Rating, res.ContributingRules, c.score, c.rating, c.rules)
			}
			if len(res.Explanation) != len(RulesV1) {
				t.Fatalf("explanation must cover every rule: %+v", res.Explanation)
			}
		})
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	counts := map[string]int{SigKYCRejected: 1, SigDestinationChanged: 4, SigDocumentRejected: 1}
	first, err := Evaluate(baseline(), counts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		again, err := Evaluate(baseline(), counts)
		if err != nil || !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs: %+v vs %+v (%v)", i, first, again, err)
		}
	}
	if len(first.LimitIDs) != len(RequiredKeys()) {
		t.Fatalf("every used limit version is recorded: %d of %d", len(first.LimitIDs), len(RequiredKeys()))
	}
}

func TestEvaluateFailsClosed(t *testing.T) {
	for _, key := range RequiredKeys() {
		if key == KeySignalWindow {
			continue // the window is applied by the caller before counting
		}
		lim := baseline()
		delete(lim.Values, key)
		if _, err := Evaluate(lim, map[string]int{SigKYCRejected: 1}); !errors.Is(err, ErrLimitsUnavailable) {
			t.Errorf("missing %s: %v", key, err)
		}
	}
	lim := baseline()
	lim.Values[KeyEnhancedMin] = 700 // above restricted_min: inconsistent bands
	if _, err := Evaluate(lim, nil); !errors.Is(err, ErrLimitsUnavailable) {
		t.Errorf("inconsistent bands: %v", err)
	}
	lim = baseline()
	lim.Values["risk.model_v1.threshold.document_rejected_repeat"] = 0
	if _, err := Evaluate(lim, nil); !errors.Is(err, ErrLimitsUnavailable) {
		t.Errorf("zero threshold: %v", err)
	}
}

func TestDecidePolicy(t *testing.T) {
	want := map[string]string{RatingLow: DecisionNoAction, RatingStandard: DecisionNoAction, RatingEnhanced: DecisionManualReview,
		RatingRestricted: DecisionEscalate, "SOMETHING_ELSE": DecisionManualReview}
	for rating, d := range want {
		got, reason := Decide(rating)
		if got != d || !regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`).MatchString(reason) {
			t.Errorf("Decide(%s) = %s %s, want %s", rating, got, reason, d)
		}
	}
}

// A score is never proof of fraud: no rule code, signal type, decision or reason names fraud.
func TestNeverClaimsFraud(t *testing.T) {
	var words []string
	for _, r := range RulesV1 {
		words = append(words, r.Code)
		words = append(words, r.Signals...)
	}
	for _, rating := range []string{RatingLow, RatingStandard, RatingEnhanced, RatingRestricted, "X"} {
		d, reason := Decide(rating)
		words = append(words, d, reason)
	}
	words = append(words, ReasonLimitsUnavailable, EvEscalationRecommended)
	for _, w := range words {
		if strings.Contains(strings.ToUpper(w), "FRAUD") {
			t.Errorf("%q claims fraud", w)
		}
	}
}

func TestMostRestrictive(t *testing.T) {
	if mostRestrictive("risk.model_v1.weight.kyc_rejected", 100, 200) != 200 || mostRestrictive(KeySignalWindow, 10, 5) != 10 {
		t.Error("weights and windows: larger wins")
	}
	if mostRestrictive(KeyEnhancedMin, 300, 200) != 200 || mostRestrictive("risk.model_v1.threshold.x", 2, 3) != 2 {
		t.Error("thresholds and floors: smaller wins")
	}
}

// The migration seeds exactly the keys the model needs, as INTERNAL_RISK baseline rows with these values.
func TestMigrationSeedsModelKeys(t *testing.T) {
	sql, err := os.ReadFile("../../migrations/20261009150500_risk.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('(risk\.[a-z0-9_.]+)',\s+'(COUNT|DURATION)',\s+(NULL|[0-9]+)(?:::bigint)?,\s+(NULL|[0-9]+)(?:::bigint)?\)`)
	seeded := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(sql), -1) {
		v := m[3]
		if m[2] == "DURATION" {
			v = m[4]
		}
		seeded[m[1]] = v
	}
	b := baseline()
	if len(seeded) != len(RequiredKeys()) {
		t.Fatalf("seeded %d keys, model needs %d: %v", len(seeded), len(RequiredKeys()), seeded)
	}
	for _, k := range RequiredKeys() {
		v, ok := seeded[k]
		if !ok {
			t.Errorf("migration does not seed %s", k)
			continue
		}
		if want := b.Values[k]; v != itoa(want) {
			t.Errorf("%s seeded %s, test baseline %d", k, v, want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func delivery(evType string, payload map[string]any) outbox.Delivery {
	b, _ := json.Marshal(payload)
	return outbox.Delivery{EventID: ids.New(), EventType: evType, Payload: b, OccurredAt: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
}

func TestSignalFromEvent(t *testing.T) {
	user, org, obj, kc, ben, dest := ids.New(), ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	ok := []struct {
		d       outbox.Delivery
		typ, st string
		sid     string
	}{
		{delivery(EvStorageObjectScanned, map[string]any{"object_id": obj, "status": "REJECTED", "owner_module": "kyc", "purpose": "KYC_DOCUMENT",
			"uploaded_by": user}), SigDocumentRejected, "USER", user},
		{delivery(EvKYCCaseRejected, map[string]any{"case_id": kc, "kind": "KYC", "subject_type": "USER", "subject_id": user, "reason_code": "DOCUMENT_UNREADABLE"}),
			SigKYCRejected, "USER", user},
		{delivery(EvKYCCaseRejected, map[string]any{"case_id": kc, "kind": "KYB", "subject_type": "ORGANISATION", "subject_id": org}),
			SigKYBRejected, "ORGANISATION", org},
		{delivery(EvKYCDuplicateIdentity, map[string]any{"case_id": kc, "profile_id": ids.New(), "other_profile_count": 2}),
			SigDuplicateIdentity, "KYC_CASE", kc},
		{delivery(EvKYCDuplicateIdentity, map[string]any{"case_id": kc, "subject_type": "USER", "subject_id": user}),
			SigDuplicateIdentity, "USER", user},
		{delivery(EvBeneficiaryRejected, map[string]any{"beneficiary_id": ben, "owner_type": "user", "owner_id": user}),
			SigBeneficiaryRejected, "USER", user},
		{delivery(EvBeneficiaryEscalated, map[string]any{"beneficiary_id": ben, "owner_type": "ORGANISATION", "owner_id": org}),
			SigBeneficiaryEscalated, "ORGANISATION", org},
		{delivery(EvPayoutDestinationChanged, map[string]any{"destination_id": dest, "owner_type": "USER", "owner_id": user}),
			SigDestinationChanged, "USER", user},
		{delivery(EvPayoutDestinationRejected, map[string]any{"destination_id": dest, "owner_type": "USER", "owner_id": user}),
			SigDestinationRejected, "USER", user},
	}
	for _, c := range ok {
		sig, yes, err := SignalFromEvent(c.d)
		if err != nil || !yes || sig.Type != c.typ || sig.SubjectType != c.st || sig.SubjectID != c.sid || sig.SourceEventID != c.d.EventID {
			t.Errorf("%s: got %+v %v %v", c.d.EventType, sig, yes, err)
			continue
		}
		if err := validateSignal(sig); err != nil {
			t.Errorf("%s: produced invalid signal: %v", c.d.EventType, err)
		}
		for k := range sig.Facts {
			if strings.Contains(k, "number") || strings.Contains(k, "name") || strings.Contains(k, "account") {
				t.Errorf("%s: fact %q looks like personal data", c.d.EventType, k)
			}
		}
	}
	// no risk fact
	for _, d := range []outbox.Delivery{
		delivery(EvStorageObjectScanned, map[string]any{"object_id": obj, "status": "CLEAN"}),
		delivery("kyc.case_approved", map[string]any{"case_id": kc}),
	} {
		if _, yes, err := SignalFromEvent(d); yes || err != nil {
			t.Errorf("%s: want no signal, got %v %v", d.EventType, yes, err)
		}
	}
	// unusable payloads are reported, never turned into signals
	bad := []outbox.Delivery{
		delivery(EvStorageObjectScanned, map[string]any{"object_id": obj, "status": "REJECTED"}), // no uploader
		delivery(EvKYCCaseRejected, map[string]any{"case_id": kc, "subject_type": "DEVICE", "subject_id": user}),
		delivery(EvKYCCaseRejected, map[string]any{"case_id": "nope", "subject_type": "USER", "subject_id": user}),
		delivery(EvKYCDuplicateIdentity, map[string]any{"profile_id": ids.New()}),
		delivery(EvBeneficiaryRejected, map[string]any{"beneficiary_id": ben, "owner_type": "USER", "owner_id": "x"}),
		delivery(EvPayoutDestinationChanged, map[string]any{"owner_type": "USER", "owner_id": user}),
		{EventID: ids.New(), EventType: EvKYCCaseRejected, Payload: json.RawMessage(`[1,2]`), OccurredAt: time.Now()},
		{EventID: ids.New(), EventType: EvKYCCaseRejected, Payload: json.RawMessage(`{"case_id":"` + kc + `","subject_type":"USER","subject_id":"` + user + `"}`)},
	}
	for i, d := range bad {
		if _, yes, err := SignalFromEvent(d); yes || !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("bad[%d] %s: want ErrInvalidPayload, got %v %v", i, d.EventType, yes, err)
		}
	}
	// an unknown reason code is replaced, never copied
	sig, _, _ := SignalFromEvent(delivery(EvKYCCaseRejected, map[string]any{"case_id": kc, "subject_type": "USER", "subject_id": user,
		"reason_code": "free text with a name"}))
	if sig.Facts["reason_code"] != defaultReasonCode {
		t.Errorf("reason code: %v", sig.Facts["reason_code"])
	}
}

func TestConsumersCoverRiskEvents(t *testing.T) {
	s := New(nil, nil, nil)
	got := map[string]bool{}
	for _, c := range s.Consumers() {
		if c.Name != ConsumerName || c.Handle == nil {
			t.Fatalf("bad consumer %+v", c)
		}
		got[c.EventType] = true
	}
	for _, ev := range []string{"storage.object_scanned", "kyc.case_rejected", "kyc.duplicate_identity_detected",
		"beneficiaries.verification_rejected", "beneficiaries.verification_escalated", "payouts.destination_changed", "payouts.destination_rejected"} {
		if !got[ev] {
			t.Errorf("risk does not consume %s", ev)
		}
	}
}
