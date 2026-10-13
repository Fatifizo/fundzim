package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/psp"
)

type mapSecrets map[string]string

func (m mapSecrets) Resolve(_ context.Context, ref string) ([]byte, error) {
	if v, ok := m[ref]; ok {
		return []byte(v), nil
	}
	return nil, psp.ErrSecretUnavailable
}

var (
	curSecret  = strings.Repeat("c", 64)
	prevSecret = strings.Repeat("p", 64)
	acct       = psp.Account{WebhookSecretRef: "secretref://fundzim/sandbox/webhook-current",
		WebhookSecretRefPrevious: "secretref://fundzim/sandbox/webhook-previous", ReplayWindow: 300 * time.Second}
)

func adapter() *Adapter {
	return &Adapter{Secrets: mapSecrets{acct.WebhookSecretRef: curSecret, acct.WebhookSecretRefPrevious: prevSecret}}
}

func payload() []byte {
	b, _ := json.Marshal(Payload{ID: "sbx_evt_0123456789abcdef0123", Type: "payment.paid", Created: 1, Simulated: true,
		Data: PayloadData{PaymentReference: "01a12f58-7f81-732c-ae0d-7b4b57a3db51", ProviderPaymentID: "sbx_pay_x", Status: "paid",
			AmountMinor: "1000", Currency: "USD", PayerMasked: "****1234"}})
	return b
}

func inbound(body []byte, sig string, at time.Time) psp.Inbound {
	h := http.Header{}
	if sig != "" {
		h.Set(SignatureHeader, sig)
	}
	return psp.Inbound{Body: body, Header: h, ReceivedAt: at}
}

func TestVerify(t *testing.T) {
	a, now, body := adapter(), time.Now(), payload()
	cases := []struct {
		name string
		sig  string
		in   []byte
		at   time.Time
		want error
		slot string
	}{
		{"current key", Sign([]byte(curSecret), "current", now, body), body, now, nil, "CURRENT"},
		{"previous key (rotation overlap)", Sign([]byte(prevSecret), "previous", now, body), body, now, nil, "PREVIOUS"},
		{"missing signature", "", body, now, psp.ErrSignatureMissing, ""},
		{"wrong secret", Sign([]byte("x"+curSecret), "current", now, body), body, now, psp.ErrSignatureInvalid, ""},
		{"previous secret under the current kid", Sign([]byte(prevSecret), "current", now, body), body, now, psp.ErrSignatureInvalid, ""},
		{"unknown key id", Sign([]byte(curSecret), "other", now, body), body, now, psp.ErrUnknownKey, ""},
		{"tampered body", Sign([]byte(curSecret), "current", now, body), append([]byte{}, append(body, ' ')...), now, psp.ErrSignatureInvalid, ""},
		{"stale timestamp", Sign([]byte(curSecret), "current", now.Add(-301*time.Second), body), body, now, psp.ErrTimestamp, ""},
		{"future timestamp", Sign([]byte(curSecret), "current", now.Add(301*time.Second), body), body, now, psp.ErrTimestamp, ""},
		{"malformed header", "t=1,v1=zz", body, now, psp.ErrSignatureInvalid, ""},
		{"timestamp swapped after signing", strings.Replace(Sign([]byte(curSecret), "current", now, body), "t=", "t=1", 1), body, now,
			psp.ErrSignatureInvalid, ""},
	}
	for _, c := range cases {
		vb, err := a.Verify(context.Background(), acct, inbound(c.in, c.sig, c.at))
		if !errors.Is(err, c.want) || (c.want == nil && vb.KeySlot != c.slot) {
			t.Errorf("%s: err %v slot %q", c.name, err, vb.KeySlot)
		}
	}
	// without a previous secret configured the previous slot is unknown
	noPrev := acct
	noPrev.WebhookSecretRefPrevious = ""
	if _, err := a.Verify(context.Background(), noPrev, inbound(body, Sign([]byte(prevSecret), "previous", now, body), now)); !errors.Is(err, psp.ErrUnknownKey) {
		t.Fatalf("previous without config: %v", err)
	}
}

func TestParseRedactsAndValidates(t *testing.T) {
	a := adapter()
	evs, err := a.Parse(context.Background(), inbound(payload(), "", time.Now()))
	if err != nil || len(evs) != 1 {
		t.Fatal(err)
	}
	e := evs[0]
	if e.Class != "PAYMENT" || e.RawStatus != "paid" || e.ReportedAmount == nil || *e.ReportedAmount != 1000 || !e.Simulated {
		t.Fatalf("event %+v", e)
	}
	raw, _ := json.Marshal(e.Redacted)
	if !strings.Contains(string(raw), "****1234") || strings.Contains(string(raw), "msisdn") {
		t.Fatalf("redaction: %s", raw)
	}
	for _, bad := range []string{`{}`, `not json`, `{"id":"evt","type":"payment.paid","data":{"status":"paid"}}`,
		`{"id":"sbx_evt_0123456789abcdef0123","type":"payment.paid","created":1,"simulated":true,"data":{"status":"paid","amount_minor":"1.5"}}`,
		`{"id":"sbx_evt_0123456789abcdef0123","type":"payment.paid","created":1,"extra":1,"data":{"status":"paid"}}`} {
		if _, err := a.Parse(context.Background(), inbound([]byte(bad), "", time.Now())); !errors.Is(err, psp.ErrMalformed) {
			t.Errorf("accepted %s", bad)
		}
	}
	// a "livemode" event is never treated as simulated
	live := strings.Replace(string(payload()), `"livemode":false`, `"livemode":true`, 1)
	evs, _ = a.Parse(context.Background(), inbound([]byte(live), "", time.Now()))
	if len(evs) == 1 && evs[0].Simulated {
		t.Fatal("livemode event marked simulated")
	}
	other := strings.Replace(string(payload()), `"payment.paid"`, `"account.updated"`, 1)
	evs, _ = a.Parse(context.Background(), inbound([]byte(other), "", time.Now()))
	if len(evs) != 1 || evs[0].Class != "OTHER" {
		t.Fatal("unknown event type must be kept as OTHER, never dropped")
	}
}

func TestMappingNeverGuesses(t *testing.T) {
	a := adapter()
	for raw, want := range map[string]string{"paid": psp.StatusSucceeded, "pending": psp.StatusPending, "declined": psp.StatusFailed,
		"action_required": psp.StatusRequiresAction} {
		if c, ok, v := a.MapPaymentStatus(raw); !ok || c != want || v != MappingVersion {
			t.Errorf("%s → %s %v", raw, c, ok)
		}
	}
	for _, raw := range []string{"limbo", "PAID", "", "processing"} {
		if c, ok, _ := a.MapPaymentStatus(raw); ok || c != psp.StatusUnknown {
			t.Errorf("unmapped %q → %s", raw, c)
		}
	}
}

func TestScenarioTable(t *testing.T) {
	want := map[int64]string{0: ScSuccess, 2: ScDecline, 3: ScTimeoutThenSuccess, 5: ScTimeoutNotCreated, 6: ScDuplicateWebhook,
		7: ScOutOfOrderWebhook, 8: ScLateSuccessAfterExp, 14: ScBadSignature, 15: ScReplayedOld, 16: ScWebhookNever, 17: ScUnmappedStatus,
		18: ScAmountMismatch, 19: ScProviderUnavailable, 23: ScDelayedConfirmation, 25: ScPreviousKey, 99: ScSuccess}
	for suffix, name := range want {
		if got := ScenarioFor(1000+suffix, "").Name; got != name {
			t.Errorf("suffix %02d: %s, want %s", suffix, got, name)
		}
	}
	if ScenarioFor(1000, "decline").Name != ScDecline {
		t.Fatal("explicit scenario name ignored")
	}
	if ScenarioFor(1000, "DROP TABLE").Name != ScSuccess {
		t.Fatal("malformed scenario name must fall back")
	}
	if dup := ScenarioFor(1006, ""); len(dup.Webhooks) != 1 || dup.Webhooks[0].Copies != 3 {
		t.Fatal("duplicate scenario")
	}
}

func TestLimitsAreSandboxContractOnly(t *testing.T) {
	a := adapter()
	if l, ok := a.Limits("ECOCASH", "USD"); !ok || l.MinMinor != 100 || l.MaxMinor != 1_000_000 {
		t.Fatalf("USD limits %+v", l)
	}
	if _, ok := a.Limits("ECOCASH", "ZWG"); ok {
		t.Fatal("no ZWG limits: ZWG is not offered (LR-043)")
	}
}
