package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

var now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func base(category, channel string) RouteInput {
	return RouteInput{Category: category, Channel: channel, Active: true, ContactKnown: true, ContactVerified: true, ProviderReady: true,
		TemplateHasChannel: true, TemplateProviderApproved: true, Now: now}
}

func granted() *Consent  { return &Consent{Status: "GRANTED", PolicyVersion: ConsentPolicyVersion} }
func boolp(b bool) *bool { return &b }

func TestRouteChannelRules(t *testing.T) {
	cases := []struct {
		name string
		in   func(*RouteInput)
		cat  string
		ch   string
		want string
	}{
		{"campaign email to a verified address", nil, CatCampaign, ChEmail, ""},
		{"campaign in-app", nil, CatCampaign, ChInApp, ""},
		{"campaign email turned off", func(r *RouteInput) { r.Preference = boolp(false) }, CatCampaign, ChEmail, SupPreference},
		{"campaign in-app cannot be turned off", func(r *RouteInput) { r.Preference = boolp(false) }, CatCampaign, ChInApp, ""},
		{"security email cannot be turned off", func(r *RouteInput) { r.Preference = boolp(false) }, CatSecurity, ChEmail, ""},
		{"SMS is off by default", func(r *RouteInput) { r.Consent = granted() }, CatPayment, ChSMS, SupPreference},
		{"SMS on but no consent", func(r *RouteInput) { r.Preference = boolp(true) }, CatPayment, ChSMS, SupConsentMissing},
		{"SMS on with consent", func(r *RouteInput) { r.Preference = boolp(true); r.Consent = granted() }, CatPayment, ChSMS, ""},
		{"SMS consent withdrawn", func(r *RouteInput) {
			r.Preference = boolp(true)
			r.Consent = &Consent{Status: "WITHDRAWN", PolicyVersion: ConsentPolicyVersion}
		},
			CatPayment, ChSMS, SupConsentWithdrawn},
		{"SMS consent expired", func(r *RouteInput) {
			past := now.Add(-time.Hour)
			r.Preference, r.Consent = boolp(true), &Consent{Status: "GRANTED", PolicyVersion: ConsentPolicyVersion, ExpiresAt: &past}
		}, CatPayment, ChSMS, SupConsentExpired},
		{"SMS consent under an older policy", func(r *RouteInput) {
			r.Preference = boolp(true)
			r.Consent = &Consent{Status: "GRANTED", PolicyVersion: "old"}
		},
			CatPayment, ChSMS, SupConsentReconfirm},
		{"WhatsApp needs consent", func(r *RouteInput) { r.Preference = boolp(true) }, CatCampaign, ChWhatsApp, SupConsentMissing},
		{"WhatsApp template not accepted by the provider", func(r *RouteInput) {
			r.Preference, r.Consent, r.TemplateProviderApproved = boolp(true), granted(), false
		}, CatCampaign, ChWhatsApp, SupTemplateProvider},
		{"marketing never (in-app too)", func(r *RouteInput) { r.Preference = boolp(true); r.Consent = granted() }, CatMarketing, ChInApp, SupMarketing},
		{"marketing email never, even with consent", func(r *RouteInput) { r.Preference = boolp(true); r.Consent = granted() }, CatMarketing, ChEmail, SupMarketing},
		{"unverified e-mail", func(r *RouteInput) { r.ContactVerified = false }, CatCampaign, ChEmail, SupContactUnverified},
		{"no phone", func(r *RouteInput) { r.Preference = boolp(true); r.Consent = granted(); r.ContactKnown = false }, CatPayment, ChSMS, SupContactMissing},
		{"suppressed destination", func(r *RouteInput) { r.Suppressed = true }, CatCampaign, ChEmail, SupDestination},
		{"TEST money never leaves the app", func(r *RouteInput) { r.Synthetic = true }, CatPayment, ChEmail, SupSynthetic},
		{"TEST money in-app", func(r *RouteInput) { r.Synthetic = true }, CatPayment, ChInApp, ""},
		{"production: live messaging disabled", func(r *RouteInput) { r.Production = true }, CatSecurity, ChEmail, SupLiveDisabled},
		{"production: in-app still works", func(r *RouteInput) { r.Production = true }, CatSecurity, ChInApp, ""},
		{"no provider", func(r *RouteInput) { r.ProviderReady = false }, CatCampaign, ChEmail, SupNoProvider},
		{"suspended account", func(r *RouteInput) { r.Active = false }, CatCampaign, ChInApp, SupInactive},
		{"risk outcome never reaches a user", nil, CatRisk, ChInApp, SupChannelDisabled},
		{"staff get in-app only", func(r *RouteInput) { r.StaffRecipient = true }, CatAdmin, ChEmail, SupChannelDisabled},
		{"staff in-app", func(r *RouteInput) { r.StaffRecipient = true }, CatAdmin, ChInApp, ""},
		{"staff security e-mail is kept", func(r *RouteInput) { r.StaffRecipient = true }, CatSecurity, ChEmail, ""},
		{"staff get no security SMS", func(r *RouteInput) { r.StaffRecipient = true; r.Preference = boolp(true); r.Consent = granted() }, CatSecurity, ChSMS, SupChannelDisabled},
		{"template without SMS text", func(r *RouteInput) { r.Preference = boolp(true); r.Consent = granted(); r.TemplateHasChannel = false }, CatPayment, ChSMS, SupTemplateChannel},
	}
	for _, c := range cases {
		in := base(c.cat, c.ch)
		if c.in != nil {
			c.in(&in)
		}
		if got := Route(in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNoFallbackAndPreferences(t *testing.T) {
	for cat, p := range Policies {
		if len(p.Fallback) != 0 {
			t.Errorf("%s has a channel fallback: none is approved in Stage 15", cat)
		}
		for _, m := range p.Mandatory {
			if !contains(p.Channels, m) || PreferenceAllowed(cat, m, false) {
				t.Errorf("%s mandatory channel %s can be turned off", cat, m)
			}
		}
	}
	if PreferenceAllowed(CatRisk, ChInApp, true) || PreferenceAllowed(CatSecurity, ChEmail, false) || !PreferenceAllowed(CatCampaign, ChEmail, false) {
		t.Fatal("preference rules")
	}
	if LiveMessagingEnabled || MarketingEnabled {
		t.Fatal("live messaging and marketing must stay disabled in Stage 15")
	}
}

func str(s string) *string { return &s }

func TestRenderIsPlaceholderOnlyAndSafe(t *testing.T) {
	c := Content{EmailSubject: str("Case {{case_reference}}"), EmailText: str("Hello {{display_name}},\n\nCase {{case_reference}} <b>updated</b>."),
		SMSText: str("FundZim: case {{case_reference}}"), InAppTitle: "Case {{case_reference}}", InAppBody: "Updated."}
	vars := map[string]string{"display_name": "<script>alert(1)</script>", "case_reference": "#12\r\nBcc: victim@example.test"}
	r, err := Render(c, vars, ChEmail, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(r.Subject, "\r\n") || strings.Contains(r.Subject, "\nBcc") {
		t.Fatalf("header injection: %q", r.Subject)
	}
	if strings.Contains(r.HTML, "<script>") || !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Fatalf("html not escaped: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "&lt;b&gt;updated") {
		t.Fatalf("template markup must be escaped too (HTML is built from text): %s", r.HTML)
	}
	if !strings.HasSuffix(r.Text, emailFooter) {
		t.Fatal("footer")
	}
	if _, err := Render(c, map[string]string{"display_name": "x"}, ChEmail, false); !errors.Is(err, ErrRender) {
		t.Fatal("a missing variable must fail, never leave a gap")
	}
	in, _ := Render(c, vars, ChInApp, true)
	if !strings.HasPrefix(in.Title, "[TEST] ") {
		t.Fatalf("synthetic label: %q", in.Title)
	}
	if err := ValidateContent(Content{InAppTitle: "{{password}}", InAppBody: "x"}, []string{"case_reference"}); err == nil {
		t.Fatal("unlisted variable accepted")
	}
	if err := ValidateContent(Content{InAppTitle: "{{ case_reference }} {{", InAppBody: "x"}, []string{"case_reference"}); err == nil {
		t.Fatal("stray braces accepted")
	}
	if err := ValidateContent(Content{InAppTitle: "a", InAppBody: "b", EmailSubject: str("x\ny"), EmailText: str("t")}, nil); err == nil {
		t.Fatal("line break in subject accepted")
	}
	if err := ValidateContent(Content{InAppTitle: "{{.Secret}}{{printf}}", InAppBody: "b"}, nil); err == nil {
		t.Fatal("template-language constructs must not be accepted")
	}
}

func TestSMSSegmentsAndPhones(t *testing.T) {
	for _, c := range []struct {
		s    string
		enc  string
		segs int
	}{
		{strings.Repeat("a", 160), "GSM7", 1}, {strings.Repeat("a", 161), "GSM7", 2}, {strings.Repeat("€", 80), "GSM7", 1}, {strings.Repeat("€", 81), "GSM7", 2},
		{strings.Repeat("ü", 160), "GSM7", 1}, {strings.Repeat("ŵ", 70), "UCS2", 1}, {strings.Repeat("ŵ", 71), "UCS2", 2}, {"Mhoro 😀", "UCS2", 1}, {"", "GSM7", 0},
	} {
		e := EstimateSMS(c.s)
		if e.Encoding != c.enc || e.Segments != c.segs {
			t.Errorf("%q: %+v, want %s/%d", c.s[:min(len(c.s), 10)], e, c.enc, c.segs)
		}
	}
	for raw, want := range map[string]string{"0771234567": "+263771234567", "+263 77 123 4567": "+263771234567", "+44 20 7946 0958": "+442079460958"} {
		if got, err := NormalizePhone(raw); err != nil || got != want {
			t.Errorf("%s → %s %v", raw, got, err)
		}
	}
	for _, bad := range []string{"", "12", "+263 1", "not a number", strings.Repeat("9", 40)} {
		if _, err := NormalizePhone(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBackoffGrowsCapsAndHonoursRetryAfter(t *testing.T) {
	st := DefaultSettings()
	mid := func() float64 { return 0.5 }
	if d := Backoff(st, 1, 0, mid); d != st.BackoffBase {
		t.Fatalf("attempt 1: %s", d)
	}
	if d := Backoff(st, 3, 0, mid); d != 4*st.BackoffBase {
		t.Fatalf("attempt 3: %s", d)
	}
	if d := Backoff(st, 30, 0, mid); d != st.BackoffCap {
		t.Fatalf("cap: %s", d)
	}
	if d := Backoff(st, 1, time.Hour, mid); d != time.Hour {
		t.Fatalf("retry-after: %s", d)
	}
	lo, hi := Backoff(st, 2, 0, func() float64 { return 0 }), Backoff(st, 2, 0, func() float64 { return 1 })
	if lo >= hi || lo < time.Duration(float64(2*st.BackoffBase)*0.79) || hi > time.Duration(float64(2*st.BackoffBase)*1.21) {
		t.Fatalf("jitter bounds: %s %s", lo, hi)
	}
}

func TestCallbackPrecedenceIsDeterministic(t *testing.T) {
	cases := []struct {
		cur, rep, next, reason string
		conflict               bool
	}{
		{"ACCEPTED", "DELIVERED", "DELIVERED", "", false},
		{"SUBMITTED", "READ", "READ", "", false},
		{"DELIVERED", "ACCEPTED", "", NotLowerRank, false}, // out of order: never regresses
		{"DELIVERED", "DELIVERED", "", NotNoChange, false}, // duplicate status
		{"READ", "DELIVERED", "", NotLowerRank, false},
		{"ACCEPTED", "BOUNCED", "FAILED_PERMANENT", "", false},
		{"DELIVERED", "BOUNCED", "", NotConflict, true}, // contradiction: evidence + flag
		{"FAILED_PERMANENT", "DELIVERED", "", NotConflictFailure, true},
		{"PROCESSING", "DELIVERED", "", NotInFlight, false}, // applied once the send result is recorded
		{"UNKNOWN", "DELIVERED", "DELIVERED", "", false},
		{"SUPPRESSED", "DELIVERED", "", NotTerminal, false},
		{"ACCEPTED", "TEMPORARY_FAILURE", "", NotInformational, false},
	}
	for _, c := range cases {
		next, reason, conflict := ApplyCallback(c.cur, c.rep)
		if next != c.next || reason != c.reason || conflict != c.conflict {
			t.Errorf("%s + %s: got (%q, %q, %v)", c.cur, c.rep, next, reason, conflict)
		}
		if next != "" && !contains(DeliveryEdges[c.cur], next) {
			t.Errorf("%s → %s is not a machine edge", c.cur, next)
		}
	}
	// any order of the same reports ends in the same state
	reports := []string{"ACCEPTED", "DELIVERED", "READ", "DELIVERED"}
	final := func(order []string) string {
		st := "SUBMITTED"
		for _, r := range order {
			if n, _, _ := ApplyCallback(st, r); n != "" {
				st = n
			}
		}
		return st
	}
	if final(reports) != "READ" || final([]string{"READ", "ACCEPTED", "DELIVERED"}) != "READ" {
		t.Fatal("order dependence")
	}
}

// The Go edge list must equal the database machine (migration 20261018090000).
func TestDeliveryEdgesMatchMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/20261018090000_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	db := map[string][]string{}
	for _, m := range regexp.MustCompile(`\('notification_delivery', '([A-Z_]*)',\s*'([A-Z_]+)'\)`).FindAllStringSubmatch(string(raw), -1) {
		db[m[1]] = append(db[m[1]], m[2])
	}
	if len(db) == 0 {
		t.Fatal("no edges parsed")
	}
	norm := func(m map[string][]string) string {
		keys := []string{}
		for k, v := range m {
			s := append([]string{}, v...)
			sort.Strings(s)
			keys = append(keys, k+">"+strings.Join(s, ","))
		}
		sort.Strings(keys)
		return strings.Join(keys, ";")
	}
	if norm(db) != norm(DeliveryEdges) {
		t.Fatalf("edges differ:\n db %s\n go %s", norm(db), norm(DeliveryEdges))
	}
}

func TestSimulatorScenariosAndWebhookVerification(t *testing.T) {
	if ScenarioFor(ChEmail, "alice+sim-bounce@example.test") != ScBounced || ScenarioFor(ChEmail, "alice@example.test") != ScDelivered ||
		ScenarioFor(ChEmail, "it-n+sim-tempfail-0a1b@example.test") != ScTempFailure ||
		ScenarioFor(ChSMS, "+263771234591") != ScUndeliverable || ScenarioFor(ChWhatsApp, "+263771234597") != ScInvalidHook {
		t.Fatal("scenario selection")
	}
	key := []byte(strings.Repeat("k", 32))
	sim := &Simulator{code: "email_simulator", channel: ChEmail, Key: key}
	h, body := sim.Signed("0192a000-0000-7000-8000-000000000001", "sim_abc", "DELIVERED", now, key)
	cb, err := sim.VerifyWebhook(h, body, now)
	if err != nil || cb.Status != "DELIVERED" || cb.ProviderMessageID != "sim_abc" {
		t.Fatalf("valid callback: %+v %v", cb, err)
	}
	if _, err := sim.VerifyWebhook(h, append(body[:len(body)-1:len(body)-1], ' '), now); !errors.Is(err, ErrWebhookSignature) {
		t.Fatal("tampered body accepted")
	}
	h2, body2 := sim.Signed("0192a000-0000-7000-8000-000000000001", "sim_abc", "DELIVERED", now, []byte(strings.Repeat("x", 32)))
	if _, err := sim.VerifyWebhook(h2, body2, now); !errors.Is(err, ErrWebhookSignature) {
		t.Fatal("forged signature accepted")
	}
	if _, err := sim.VerifyWebhook(h, body, now.Add(10*time.Minute)); !errors.Is(err, ErrWebhookTimestamp) {
		t.Fatal("replayed (stale) callback accepted")
	}
	h3, body3 := sim.Signed("not-a-uuid", "sim_abc", "DELIVERED", now, key)
	if _, err := sim.VerifyWebhook(h3, body3, now); !errors.Is(err, ErrWebhookPayload) {
		t.Fatal("invalid payload accepted")
	}
	h4, body4 := sim.Signed("0192a000-0000-7000-8000-000000000002", "sim_abc", "PAID", now, key)
	if _, err := sim.VerifyWebhook(h4, body4, now); !errors.Is(err, ErrWebhookPayload) {
		t.Fatal("unknown status accepted")
	}
}

type fakeDir struct{ staff map[string][]string }

func (f fakeDir) OrganisationAdmins(context.Context, string) ([]string, error) {
	return []string{"0192a000-0000-7000-8000-0000000000a1", "0192a000-0000-7000-8000-0000000000a2"}, nil
}
func (f fakeDir) StaffWithPermission(_ context.Context, p string) ([]string, error) {
	return f.staff[p], nil
}

func deliv(t *testing.T, ev string, p map[string]any) outbox.Delivery {
	t.Helper()
	raw, _ := json.Marshal(p)
	return outbox.Delivery{EventID: "0192a000-0000-7000-8000-00000000e001", EventType: ev, Payload: raw, OccurredAt: now}
}

const donor = "0192a000-0000-7000-8000-0000000000d1"

// Payment success safety: only payments.payment_succeeded produces a confirmation; intents, UNKNOWN and failures never do.
func TestFinancialEventMapping(t *testing.T) {
	s := &Service{Directory: fakeDir{}}
	ctx := context.Background()
	keys := func(ev string, p map[string]any) []string {
		in, err := s.intentsFor(ctx, deliv(t, ev, p))
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, i := range in {
			out = append(out, i.TemplateKey)
			if !i.Synthetic && (strings.HasPrefix(ev, "payments.") || strings.HasPrefix(ev, "payouts.")) {
				t.Errorf("%s: a TEST payment/payout must be synthetic", ev)
			}
		}
		return out
	}
	testPay := map[string]any{"donor_user_id": donor, "payment_id": donor, "amount_minor": "1250", "currency": "USD", "test_payment": true,
		"execution_environment": "LOCAL_SIMULATOR"}
	if k := keys("payments.donation_intent_created", testPay); len(k) != 0 {
		t.Fatalf("an intent produced %v", k)
	}
	for _, to := range []string{"SUCCEEDED", "AUTHORISED", "PENDING", "CREATED"} {
		p := map[string]any{"to": to}
		for k, v := range testPay {
			p[k] = v
		}
		if k := keys("payments.payment_status_changed", p); len(k) != 0 {
			t.Fatalf("status %s produced %v (success comes only from payments.payment_succeeded)", to, k)
		}
	}
	unknown := map[string]any{"to": "UNKNOWN"}
	for k, v := range testPay {
		unknown[k] = v
	}
	if k := keys("payments.payment_status_changed", unknown); len(k) != 1 || k[0] != "PAYMENT_PENDING_CONFIRMATION" {
		t.Fatalf("unknown: %v", k)
	}
	if k := keys("payments.payment_succeeded", testPay); len(k) != 1 || k[0] != "DONATION_CONFIRMED" {
		t.Fatalf("succeeded: %v", k)
	}
	in, _ := s.intentsFor(ctx, deliv(t, "payments.payment_succeeded", testPay))
	if in[0].Variables["amount"] != "USD 12.50" {
		t.Fatalf("amount: %q", in[0].Variables["amount"])
	}
	for to, want := range map[string]string{"UNKNOWN": "PAYOUT_OUTCOME_UNKNOWN", "FAILED": "PAYOUT_FAILED", "COMPLETED": "PAYOUT_COMPLETED", "PROCESSING": ""} {
		k := keys("payouts.status_changed", map[string]any{"to": to, "requested_by": donor, "payout_request_id": donor, "test_payout": true})
		if (want == "" && len(k) != 0) || (want != "" && (len(k) != 1 || k[0] != want)) {
			t.Fatalf("payout %s: %v", to, k)
		}
	}
	// staff-only categories resolve staff by permission; STR compliance cases are never surfaced
	s.Directory = fakeDir{staff: map[string][]string{"compliance.case.view": {donor}}}
	if k := keys("compliance.case_opened", map[string]any{"case_type": "RESTRICTED_STR"}); len(k) != 0 {
		t.Fatal("an STR case was surfaced")
	}
	if k := keys("risk.case_opened", map[string]any{"priority": "P4"}); len(k) != 0 {
		t.Fatal("low-priority risk case notified")
	}
	in, _ = s.intentsFor(ctx, deliv(t, "campaigns.approved", map[string]any{"owner_organisation_id": donor, "campaign_id": donor}))
	if len(in) != 1 || len(in[0].Recipients) != 2 || in[0].DeepLink != "/dashboard/campaigns/"+donor {
		t.Fatalf("organisation campaign: %+v", in)
	}
}
