package risk

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

func v2delivery(evType string, payload map[string]any) outbox.Delivery {
	b, _ := json.Marshal(payload)
	return outbox.Delivery{EventID: ids.New(), EventType: evType, Payload: b, OccurredAt: t0, CorrelationID: "corr-1"}
}

func TestSignalFromV2Events(t *testing.T) {
	user, org, camp, pay, dest, pr := ids.New(), ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	ok := []struct {
		d            outbox.Delivery
		typ, st, sid string
		fact         string // a fact the signal must carry
	}{
		{v2delivery(EvCampaignCreated, map[string]any{"campaign_id": camp, "owner_user_id": user, "title_hash": strings.Repeat("ab", 32)}),
			SigCampaignCreated, "USER", user, "title_hash"},
		{v2delivery(EvCampaignCreated, map[string]any{"campaign_id": camp, "owner_organisation_id": org}), SigCampaignCreated, "ORGANISATION", org, "campaign_id"},
		{v2delivery(EvCampaignReportEscal, map[string]any{"campaign_id": camp, "reason_code": "MISLEADING"}), SigCampaignReported, "CAMPAIGN", camp, "reason_code"},
		{v2delivery(EvDonationIntentCreated, map[string]any{"donation_intent_id": pay, "campaign_id": camp, "donor_user_id": user, "amount_minor": "500",
			"currency": "USD"}), SigDonationIntentCreated, "USER", user, "amount_minor"},
		{v2delivery(EvPaymentStatusChanged, map[string]any{"payment_id": pay, "to": "FAILED", "donor_user_id": user, "campaign_id": camp}),
			SigPaymentFailed, "USER", user, "payment_id"},
		{v2delivery(EvPayoutRequested, map[string]any{"payout_request_id": pr, "campaign_id": camp, "requested_by": user, "amount_minor": "1000",
			"currency": "USD", "destination_id": dest}), SigWithdrawalRequested, "USER", user, "destination_id"},
		{v2delivery(EvPayoutStatusChanged, map[string]any{"payout_request_id": pr, "campaign_id": camp, "to": "REVERSED", "requested_by": user}),
			SigPayoutReturned, "USER", user, "payout_request_id"},
		{v2delivery(EvMFAChanged, map[string]any{"user_id": user, "change": "disabled"}), SigMFAChanged, "USER", user, "reason_code"},
		{v2delivery(EvSignedIn, map[string]any{"user_id": user, "ua_hash": strings.Repeat("0f", 16)}), SigSignedIn, "USER", user, "ua_hash"},
		{v2delivery(EvRestrictionApplied, map[string]any{"subject_type": "campaign", "subject_id": camp, "level": "SUSPENDED"}),
			SigRestrictionApplied, "CAMPAIGN", camp, "reason_code"},
		{v2delivery(EvFeeAdjustmentRequested, map[string]any{"actor_id": user, "amount_minor": "100", "reason_code": "POLICY_CORRECTION"}),
			SigFeeAdjustmentRequested, "USER", user, "amount_minor"},
	}
	for _, c := range ok {
		sig, got, err := SignalFromEvent(c.d)
		if err != nil || !got {
			t.Fatalf("%s: %v %v", c.d.EventType, got, err)
		}
		if sig.Type != c.typ || sig.SubjectType != c.st || sig.SubjectID != c.sid || sig.CorrelationID != "corr-1" {
			t.Fatalf("%s: %+v", c.d.EventType, sig)
		}
		if _, has := sig.Facts[c.fact]; !has {
			t.Fatalf("%s: fact %s missing: %+v", c.d.EventType, c.fact, sig.Facts)
		}
		if err := validateSignal(sig); err != nil {
			t.Fatalf("%s: %v", c.d.EventType, err)
		}
		if err := sig.normalise(); err != nil || sig.Category == "" || sig.Severity == "" || sig.Confidence == 0 || sig.Kind != KindObserved {
			t.Fatalf("%s: normalise %v %+v", c.d.EventType, err, sig)
		}
	}
	// events that are not risk facts produce no signal
	for _, d := range []outbox.Delivery{
		v2delivery(EvPaymentStatusChanged, map[string]any{"payment_id": pay, "to": "SUCCEEDED", "donor_user_id": user}),
		v2delivery(EvPayoutStatusChanged, map[string]any{"payout_request_id": pr, "to": "COMPLETED", "requested_by": user}),
	} {
		if _, got, err := SignalFromEvent(d); got || err != nil {
			t.Fatalf("%s produced a signal: %v %v", d.EventType, got, err)
		}
	}
	// malformed payloads are refused, never guessed
	for _, d := range []outbox.Delivery{
		v2delivery(EvDonationIntentCreated, map[string]any{"donation_intent_id": pay, "campaign_id": camp}),
		v2delivery(EvRestrictionApplied, map[string]any{"subject_type": "DEVICE", "subject_id": camp}),
		v2delivery(EvSignedIn, map[string]any{"user_id": "not-a-uuid"}),
	} {
		if _, _, err := SignalFromEvent(d); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("%s: %v", d.EventType, err)
		}
	}
	// a raw user agent or an IP address is never a fact (only the keyed hash passes)
	sig, _, _ := SignalFromEvent(v2delivery(EvSignedIn, map[string]any{"user_id": user, "ua_hash": "Mozilla/5.0 (X11)", "ip": "10.0.0.1"}))
	if _, has := sig.Facts["ua_hash"]; has || len(sig.Facts) != 0 {
		t.Fatalf("raw device data kept: %+v", sig.Facts)
	}
	// every catalogued type has a category and the events it consumes are subscribed
	for typ := range SignalTypes() {
		if m := SignalMetaOf(typ); !m.Known || m.Category == "" || m.Confidence <= 0 || m.Confidence > 100 {
			t.Fatalf("catalogue %s: %+v", typ, m)
		}
	}
	subscribed := map[string]bool{}
	for _, e := range ConsumedEvents {
		subscribed[e] = true
	}
	for _, e := range []string{EvCampaignCreated, EvDonationIntentCreated, EvPayoutRequested, EvSignedIn, EvFeePolicyChanged, EvFeeAdjustmentRequested} {
		if !subscribed[e] {
			t.Fatalf("%s not consumed", e)
		}
	}
}

func TestScreeningCallbackSignature(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	p := &SandboxScreening{Key: key}
	body := []byte(`{"provider_reference":"SBXS-1","event_id":"e1","sequence":2,"status":"NO_MATCH_REPORTED","matches":[]}`)
	now := time.Unix(1_800_000_000, 0)
	cb, err := p.VerifyScreeningWebhook(SignScreeningCallback(key, now, body), body, now.Add(time.Minute))
	if err != nil || cb.ProviderReference != "SBXS-1" || cb.Sequence != 2 || cb.Status != ScrNoMatch {
		t.Fatalf("valid callback: %+v %v", cb, err)
	}
	bad := []struct {
		name, header string
		body         []byte
		at           time.Time
	}{
		{"other key", SignScreeningCallback([]byte(strings.Repeat("x", 32)), now, body), body, now},
		{"tampered body", SignScreeningCallback(key, now, body), append([]byte{}, strings.Replace(string(body), "NO_MATCH", "POTENTIAL", 1)...), now},
		{"replayed", SignScreeningCallback(key, now, body), body, now.Add(CallbackReplayWin + time.Second)},
		{"malformed header", "v1=abc", body, now},
		{"unknown field", SignScreeningCallback(key, now, []byte(`{"provider_reference":"SBXS-1","name":"A Person"}`)),
			[]byte(`{"provider_reference":"SBXS-1","name":"A Person"}`), now},
	}
	for _, c := range bad {
		if _, err := p.VerifyScreeningWebhook(c.header, c.body, c.at); err == nil {
			t.Fatalf("%s accepted", c.name)
		}
	}
	if _, err := (&SandboxScreening{}).VerifyScreeningWebhook(SignScreeningCallback(nil, now, body), body, now); err == nil {
		t.Fatal("callback accepted without a configured key")
	}
	// every scenario maps to a result; matches are synthetic and labelled TEST_ONLY
	for sc := range SandboxScenarios {
		r := sandboxResult("SBXS-1", sc)
		for _, m := range r.Matches {
			if m.Label != "TEST_ONLY" || !strings.HasPrefix(m.MatchID, "TEST-") || m.List != "TEST_SYNTHETIC_LIST" {
				t.Fatalf("%s: %+v", sc, m)
			}
		}
		if !reportedStatuses[r.Status] || r.Status == ScrPending {
			t.Fatalf("%s: final status %s", sc, r.Status)
		}
	}
	if ScreeningProductionEnabled {
		t.Fatal("production screening must stay disabled")
	}
}

func TestRuleVersionValidation(t *testing.T) {
	good := `{"when":{"signal_count":{"signal_types":["CAMPAIGN_REPORTED"],"window_seconds":3600,"op":">=","value":2}}}`
	hold, dur := "WITHDRAWAL_HOLD", 3600
	in := func(def, action string, h *string, d *int, contribution int) RuleVersionInput {
		return RuleVersionInput{Definition: json.RawMessage(def), Contribution: contribution, Severity: "HIGH", RecommendedAction: action, HoldType: h,
			HoldDurationSeconds: d, CasePriority: "P2"}
	}
	okIn := in(good, ActTemporaryHold, &hold, &dur, 40)
	if err := validateVersion(&okIn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(okIn.Definition), `"signal_count"`) {
		t.Fatalf("definition not canonicalised: %s", okIn.Definition)
	}
	camp, long := "CAMPAIGN_HOLD", 30*24*3600
	for name, v := range map[string]RuleVersionInput{
		"contribution":     in(good, ActReview, nil, nil, 101),
		"hold w/o type":    in(good, ActTemporaryHold, nil, &dur, 10),
		"disruptive hold":  in(good, ActTemporaryHold, &camp, &dur, 10),
		"hold too long":    in(good, ActTemporaryHold, &hold, &long, 10),
		"hold on review":   in(good, ActReview, &hold, &dur, 10),
		"allow is no rule": in(good, ActAllow, nil, nil, 10),
		"code injection":   in(`{"when":{"eval":"rm -rf /"}}`, ActReview, nil, nil, 10),
		"unknown signal":   in(`{"when":{"signal_count":{"signal_types":["X_Y_Z"],"window_seconds":60,"op":">=","value":1}}}`, ActReview, nil, nil, 10),
		"empty":            in(``, ActReview, nil, nil, 10),
	} {
		v := v
		if err := validateVersion(&v); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// the definition hash covers everything that decides behaviour
	a := RuleVersion{Definition: json.RawMessage(good), Contribution: 10, Severity: "HIGH", RecommendedAction: ActReview, CasePriority: "P2", EngineVersion: EngineVersion}
	b := a
	b.Contribution = 11
	if definitionHash(a) == definitionHash(b) {
		t.Fatal("definition hash ignores the contribution")
	}
	if definitionHash(a) != definitionHash(a) {
		t.Fatal("definition hash not deterministic")
	}
}

func TestStateMachinesAreClosed(t *testing.T) {
	for name, edges := range map[string]map[string][]string{"case": CaseEdges, "hold": HoldEdges, "rule": RuleVersionEdges, "screening": ScreeningEdges} {
		known := map[string]bool{"": true}
		for from, tos := range edges {
			known[from] = true
			for _, to := range tos {
				known[to] = true
			}
		}
		for from := range edges {
			if !known[from] {
				t.Fatalf("%s: unknown state %s", name, from)
			}
		}
	}
	// terminal states have no exit (history is never rewritten)
	for _, st := range []string{HoldReleased, HoldExpired, HoldRevoked} {
		if len(HoldEdges[st]) != 0 {
			t.Fatalf("hold %s is not terminal", st)
		}
	}
	for _, st := range []string{RVRejected, RVRetired} {
		if len(RuleVersionEdges[st]) != 0 {
			t.Fatalf("rule %s is not terminal", st)
		}
	}
	for _, st := range []string{ScrConfirmedMatch, ScrCleared, ScrNoMatch} {
		if len(ScreeningEdges[st]) != 0 {
			t.Fatalf("screening %s is not terminal", st)
		}
	}
	// a decision is reached only through DECISION_PENDING (maker-checker)
	for from, tos := range CaseEdges {
		for _, to := range tos {
			if to == CaseResolved && from != CaseDecision {
				t.Fatalf("case reaches RESOLVED from %s", from)
			}
		}
	}
}
