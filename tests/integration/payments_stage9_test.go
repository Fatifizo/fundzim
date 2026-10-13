//go:build integration

package integration

// Stage 9 (ADR-040, ADR-041): secret manager (real OpenBao from compose), execution-environment isolation, the
// production startup gate, provider evidence with contradiction escalation, error categories, provider controls and
// derived health, the public method-availability endpoint, audit events, the adapter conformance suite against the
// simulator, and concurrency. No real provider exists: the PROVIDER_SANDBOX path is exercised with an in-test fake
// provider adapter registered on a non-simulator provider row, which proves the isolation, not any integration.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/secretstore"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/psptest"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// ---- OpenBao helpers (root token from .env; dev mode from compose) ----------------------------------------

func bao(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	need(t, "SECRETS_KV2_ADDR", "SECRETS_KV2_TOKEN")
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimRight(os.Getenv("SECRETS_KV2_ADDR"), "/")+path, rd)
	req.Header.Set("X-Vault-Token", os.Getenv("SECRETS_KV2_TOKEN"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("openbao %s %s: %v (is fundzim-secrets running?)", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func baoPut(t *testing.T, path, value string) {
	t.Helper()
	if st, out := bao(t, "POST", "/v1/secret/data/"+path, map[string]any{"data": map[string]string{"value": value}}); st != 200 {
		t.Fatalf("put %s: %d %v", path, st, out)
	}
}

func strongValue() string {
	return "it-" + strings.ReplaceAll(ids.New(), "-", "") + strings.ReplaceAll(ids.New(), "-", "")
}

func kvClient(t *testing.T, mut func(*secretstore.Config)) *secretstore.KV2 {
	t.Helper()
	need(t, "SECRETS_KV2_ADDR", "SECRETS_KV2_TOKEN")
	cfg := secretstore.Config{Addr: os.Getenv("SECRETS_KV2_ADDR"), Mount: "secret", Token: os.Getenv("SECRETS_KV2_TOKEN"),
		CacheTTL: time.Minute, Timeout: 3 * time.Second}
	if mut != nil {
		mut(&cfg)
	}
	k, err := secretstore.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestPaymentsSecretManagerOpenBao: KV v2 resolution, rotation audit, AppRole with policy separation between
// environments, and fail-closed behaviour, against the real OpenBao service.
func TestPaymentsSecretManagerOpenBao(t *testing.T) {
	p := newPayIT(t)
	run := strings.ReplaceAll(ids.New()[:8], "-", "")
	sandboxPath, prodPath := "fundzim/sandbox/it-"+run+"/api", "fundzim/production/it-"+run+"/api"
	v1 := strongValue()
	baoPut(t, sandboxPath, v1)
	baoPut(t, prodPath, strongValue())

	// token client: resolve, cache, rotation → audit through the psp callback (versions only)
	var rotations atomic.Int32
	k := kvClient(t, func(c *secretstore.Config) {
		c.CacheTTL = 200 * time.Millisecond
		c.OnRotate = func(ref string, from, to int) {
			rotations.Add(1)
			p.s.d.Verification.PSP.RecordCredentialRotation(ref, from, to)
		}
	})
	ref := "secretref://" + sandboxPath
	got, err := k.Resolve(ctx(t), ref)
	if err != nil || string(got) != v1 {
		t.Fatalf("resolve: %v", err)
	}
	v2 := strongValue()
	baoPut(t, sandboxPath, v2)
	time.Sleep(300 * time.Millisecond)
	if got, err = k.Resolve(ctx(t), ref); err != nil || string(got) != v2 {
		t.Fatalf("rotated value: %v", err)
	}
	if rotations.Load() != 1 || k.Version(ref) != 2 {
		t.Fatalf("rotation not detected: %d, version %d", rotations.Load(), k.Version(ref))
	}
	var md string
	if err := p.mig.QueryRow(ctx(t), `SELECT metadata::text FROM audit.security_audit_events WHERE action = 'psp.provider.credential_rotated'
		AND metadata->>'reference' = $1`, ref).Scan(&md); err != nil {
		t.Fatalf("PROVIDER_CREDENTIAL_ROTATED not audited: %v", err)
	}
	if strings.Contains(md, v1) || strings.Contains(md, v2) || !strings.Contains(md, `"to_version": 2`) {
		t.Fatalf("rotation audit metadata: %s", md)
	}

	// AppRole with a sandbox-only policy: reads the sandbox path, is refused the production path
	_, _ = bao(t, "POST", "/v1/sys/auth/approle", map[string]string{"type": "approle"}) // 400 when already enabled
	policy := `path "secret/data/fundzim/sandbox/*" { capabilities = ["read"] }`
	if st, out := bao(t, "PUT", "/v1/sys/policies/acl/fundzim-it-sandbox", map[string]string{"policy": policy}); st != 204 {
		t.Fatalf("policy: %d %v", st, out)
	}
	role := "fundzim-it-" + run
	if st, out := bao(t, "POST", "/v1/auth/approle/role/"+role, map[string]any{"token_policies": []string{"fundzim-it-sandbox"},
		"token_ttl": "10m", "secret_id_num_uses": 0}); st != 204 {
		t.Fatalf("role: %d %v", st, out)
	}
	_, rid := bao(t, "GET", "/v1/auth/approle/role/"+role+"/role-id", nil)
	_, sid := bao(t, "POST", "/v1/auth/approle/role/"+role+"/secret-id", nil)
	roleID := rid["data"].(map[string]any)["role_id"].(string)
	secretID := sid["data"].(map[string]any)["secret_id"].(string)
	ar := kvClient(t, func(c *secretstore.Config) { c.Token, c.RoleID, c.SecretID = "", roleID, secretID })
	if _, err := ar.Resolve(ctx(t), ref); err != nil {
		t.Fatalf("AppRole sandbox read: %v", err)
	}
	if _, err := ar.Resolve(ctx(t), "secretref://"+prodPath); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("sandbox AppRole read a production secret: %v", err)
	}
	if err := ar.Health(ctx(t)); err != nil {
		t.Fatalf("health: %v", err)
	}

	// fail closed: a wrong token and an unreachable store resolve nothing
	bad := kvClient(t, func(c *secretstore.Config) { c.Token = "not-a-valid-token" })
	if _, err := bad.Resolve(ctx(t), ref); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("wrong token: %v", err)
	}
	down := kvClient(t, func(c *secretstore.Config) { c.Addr = "http://127.0.0.1:1" })
	if _, err := down.Resolve(ctx(t), ref); !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("unreachable store: %v", err)
	}

	// the application resolves the simulator's credentials from the secret manager (compose seeds them)
	if p.s.cfg.Secrets.Provider == "kv2" {
		fin := p.s.staff(t, p.f.finance2)
		prov := fin.do("GET", "/admin/payments/providers", nil)
		if !strings.Contains(string(prov.Data), `"secrets_resolvable":true`) || !strings.Contains(string(prov.Data), `"credential_scope_ok":true`) {
			t.Fatalf("simulator credentials do not resolve from OpenBao: %.400s", prov.Data)
		}
	}

	// secret manager outage inside the app: credentials do not resolve → no route, no intent, health UNAVAILABLE
	s2 := newITServer(t, func(c *config.Config) {
		c.Payments.SandboxEnabled = true
		c.Secrets = config.Secrets{Provider: "kv2", KV2Addr: os.Getenv("SECRETS_KV2_ADDR"), KV2Mount: "secret",
			KV2Token: "wrong-token-for-outage-test", KV2CacheTTL: time.Second, KV2Timeout: time.Second}
	})
	donor := verifiedUser(t, s2, "donor9")
	r := donor.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1024", "currency": "USD"},
		"Idempotency-Key", idemKey())
	donor.expect(r, 422, "PAYMENT_METHOD_UNAVAILABLE")
	hv, err := s2.d.Verification.PSP.AdminHealth(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range hv.Items {
		if it.Provider.Code == "sandbox" && (it.Health.Status != psp.HealthUnavailable || it.Health.Reason != "CREDENTIALS_UNAVAILABLE") {
			t.Fatalf("simulator health with unresolvable credentials: %+v", it.Health)
		}
	}
}

// ---- conformance suite against the simulator -----------------------------------------------------------

type simHarness struct {
	a    *sandbox.Adapter
	acct psp.Account
	sec  psp.SecretResolver
}

func (h simHarness) Name() string         { return "sandbox simulator (LOCAL_SIMULATOR)" }
func (h simHarness) Adapter() psp.Adapter { return h.a }
func (h simHarness) Account() psp.Account { return h.acct }
func (h simHarness) PaidStatus() string   { return "paid" }
func (h simHarness) Request(sc string) (psp.CreatePaymentRequest, bool) {
	// amounts select simulator scenarios without scheduled webhooks (nothing is delivered for these references)
	amount := map[string]int64{psptest.ScCreateSuccess: 1024, psptest.ScCreateDecline: 1002, psptest.ScCreateTimeout: 1005,
		psptest.ScCreateNotSent: 1019, psptest.ScProviderDown: 1019}[sc]
	if amount == 0 {
		return psp.CreatePaymentRequest{}, false
	}
	return psp.CreatePaymentRequest{PaymentID: ids.New(), AmountMinor: amount, Currency: "USD", Method: psp.MethodEcoCash,
		Description: "conformance", ReturnURL: "https://example.test/return"}, true
}
func (h simHarness) Webhook(paymentID, ref, raw, slot string, at time.Time) (psp.Inbound, bool) {
	evt := "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20]
	body, _ := json.Marshal(sandbox.Payload{ID: evt, Type: "payment." + raw, Created: at.Unix(), Simulated: true,
		Data: sandbox.PayloadData{PaymentReference: paymentID, ProviderPaymentID: ref, Status: raw, AmountMinor: "1024", Currency: "USD"}})
	hdr := http.Header{"Content-Type": {"application/json"}}
	ctx := context.Background()
	switch slot {
	case psptest.SlotCurrent, psptest.SlotPrevious:
		r := h.acct.WebhookSecretRef
		if slot == psptest.SlotPrevious {
			r = h.acct.WebhookSecretRefPrevious
		}
		sec, err := h.sec.Resolve(ctx, r)
		if err != nil {
			return psp.Inbound{}, false
		}
		hdr.Set(sandbox.SignatureHeader, sandbox.Sign(sec, slot, at, body))
	case psptest.SlotWrong:
		hdr.Set(sandbox.SignatureHeader, sandbox.Sign([]byte("wrong-key-wrong-key-wrong-key-wrong-key"), "current", at, body))
	}
	return psp.Inbound{Body: body, Header: hdr, ReceivedAt: time.Now()}, true
}
func (h simHarness) Secrets(ctx context.Context) [][]byte {
	var out [][]byte
	for _, r := range []string{h.acct.APISecretRef, h.acct.WebhookSecretRef, h.acct.WebhookSecretRefPrevious} {
		if v, err := h.sec.Resolve(ctx, r); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func TestPaymentsConformanceSuiteSimulator(t *testing.T) {
	p := newPayIT(t)
	m := p.s.d.Verification
	prov, err := m.PSP.ProviderByCode(ctx(t), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	acct, err := m.PSP.ActiveAccount(ctx(t), prov)
	if err != nil {
		t.Fatal(err)
	}
	rep := psptest.Run(ctx(t), simHarness{a: m.Sandbox, acct: acct, sec: m.PSP.Secrets})
	for _, r := range rep.Results {
		t.Logf("conformance %-36s %-10s %s", r.Scenario, r.Status, r.Detail)
	}
	if out := os.Getenv("FUNDZIM_CONFORMANCE_REPORT"); out != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		_ = os.WriteFile(out, b, 0o600)
	}
	if rep.Count(psptest.Fail) != 0 || rep.Count(psptest.Pass) < len(psptest.Scenarios)-1 {
		t.Fatalf("simulator conformance: %d PASS, %d FAIL, %d NOT_TESTED", rep.Count(psptest.Pass), rep.Count(psptest.Fail), rep.Count(psptest.NotTested))
	}
	// a provider without sandbox access is BLOCKED, never PASS
	if b := psptest.BlockedReport("any real provider", "no sandbox credentials"); b.Count(psptest.Pass) != 0 || b.Count(psptest.Blocked) != len(psptest.Scenarios) {
		t.Fatal("blocked report")
	}
}

// ---- execution-environment isolation --------------------------------------------------------------------

// fakeProvider is an IN-TEST double standing in for a real provider's adapter in PROVIDER_SANDBOX. It is not an
// integration with any provider and never leaves the test process.
type fakeProvider struct {
	code   string
	status atomic.Value // raw status returned by GetPayment
	sec    psp.SecretResolver
}

func (f *fakeProvider) Code() string { return f.code }
func (f *fakeProvider) CreatePayment(_ context.Context, _ psp.CallMeta, r psp.CreatePaymentRequest) psp.Observation {
	return psp.Observation{Class: psp.ClassAccepted, ProviderRef: "fake-" + r.PaymentID[:8], RawStatus: "pending", Canonical: psp.StatusPending,
		Mapped: true, MappingVersion: "fake-1", Next: psp.NextAction{Type: psp.NextNone}, HTTPStatus: 201}
}
func (f *fakeProvider) GetPayment(_ context.Context, _ psp.CallMeta, id string) psp.Observation {
	raw := f.status.Load().(string)
	c, ok, v := f.MapPaymentStatus(raw)
	amt := int64(1024)
	return psp.Observation{Class: psp.ClassAccepted, ProviderRef: "fake-" + id[:8], RawStatus: raw, Canonical: c, Mapped: ok, MappingVersion: v,
		ReportedAmount: &amt, ReportedCcy: "USD", HTTPStatus: 200}
}
func (f *fakeProvider) CancelPayment(context.Context, psp.CallMeta, string) psp.Observation {
	return psp.Observation{Class: psp.ClassNotSupported}
}
func (f *fakeProvider) Verify(ctx context.Context, acct psp.Account, in psp.Inbound) (psp.VerifiedBy, error) {
	sec, err := f.sec.Resolve(ctx, acct.WebhookSecretRef)
	if err != nil {
		return psp.VerifiedBy{}, psp.ErrUnknownKey
	}
	mac := hmac.New(sha256.New, sec)
	mac.Write(in.Body)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(in.Header.Get("X-Fake-Signature"))) {
		return psp.VerifiedBy{}, psp.ErrSignatureInvalid
	}
	return psp.VerifiedBy{Method: "SIGNATURE", KeySlot: "CURRENT"}, nil
}
func (f *fakeProvider) Parse(context.Context, psp.Inbound) ([]psp.WebhookEvent, error) {
	return nil, psp.ErrMalformed
}
func (f *fakeProvider) MapPaymentStatus(raw string) (string, bool, string) {
	switch raw {
	case "pending":
		return psp.StatusPending, true, "fake-1"
	case "paid":
		return psp.StatusSucceeded, true, "fake-1"
	}
	return psp.StatusUnknown, false, "fake-1"
}
func (f *fakeProvider) Limits(method, currency string) (psp.Limits, bool) {
	if method == psp.MethodEcoCash && currency == "USD" {
		return psp.Limits{MinMinor: 100, MaxMinor: 100_000}, true
	}
	return psp.Limits{}, false
}
func (f *fakeProvider) RedirectHosts() []string { return nil }

// registerFakeProvider inserts a NON-simulator provider row (never selected, production status DISABLED), a
// SANDBOX account whose secrets live in OpenBao, and one verified test capability.
func registerFakeProvider(t *testing.T, p *payIT) (code, id string) {
	t.Helper()
	code = "itfake_" + strings.ReplaceAll(ids.New()[:8], "-", "")
	id = ids.New()
	baoPut(t, "fundzim/sandbox/"+code+"/api", strongValue())
	baoPut(t, "fundzim/sandbox/"+code+"/webhook", strongValue())
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := p.mig.Exec(ctx(t), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql[:40], err)
		}
	}
	exec(`INSERT INTO app.payment_providers (id, code, display_name, is_sandbox, selection_status, status, confirmation_mode, sandbox_status)
		VALUES ($1, $2, 'IT fake provider (test double)', false, 'PENDING', 'DISABLED', 'WEBHOOK_SIGNED', 'ENABLED')`, id, code)
	exec(`INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref, webhook_secret_ref, status)
		VALUES ($1, $2, 'SANDBOX', 'it', $3, $4, 'ENABLED')`, ids.New(), id, "secretref://fundzim/sandbox/"+code+"/api",
		"secretref://fundzim/sandbox/"+code+"/webhook")
	exec(`INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
		status_api, evidence_status, evidence_source, recorded_by) VALUES ($1, $2, 1, 'ECOCASH', 'USD', 'USD', 'PSP_POOL', true, 'VERIFIED',
		'integration-test double (not a provider)', '00000000-0000-0000-0000-000000000001')`, ids.New(), id)
	t.Cleanup(func() {
		_, _ = p.mig.Exec(context.Background(), `UPDATE app.payment_providers SET sandbox_status = 'DISABLED', status = 'RETIRED' WHERE id = $1`, id)
	})
	return code, id
}

func TestPaymentsProviderSandboxIsolation(t *testing.T) {
	p := newPayIT(t)
	code, provID := registerFakeProvider(t, p)
	newSrv := func(allow ...string) (*itServer, *fakeProvider) {
		s := newITServer(t, func(c *config.Config) {
			c.Payments.SandboxEnabled = false
			c.Payments.ExecutionEnvironment = config.ExecProviderSandbox
			c.Payments.ProviderAllowlist = allow
		})
		f := &fakeProvider{code: code, sec: s.d.Verification.PSP.Secrets}
		f.status.Store("pending")
		s.d.Verification.PSP.Register(f)
		return s, f
	}
	s, fake := newSrv(code)
	donor := verifiedUser(t, s, "donorps")
	r := donor.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1024", "currency": "USD"},
		"Idempotency-Key", idemKey())
	donor.expect(r, 201, "")
	var di struct {
		ID          string `json:"id"`
		Simulated   bool   `json:"simulated"`
		Env         string `json:"execution_environment"`
		TestPayment bool   `json:"test_payment"`
	}
	_ = json.Unmarshal(r.Data, &di)
	if di.Simulated || di.Env != "PROVIDER_SANDBOX" || !di.TestPayment {
		t.Fatalf("provider-sandbox intent must be a non-simulated TEST payment: %s", r.Data)
	}
	ar := donor.do("POST", "/donation-intents/"+di.ID+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", idemKey())
	donor.expect(ar, 201, "")
	var a attemptView
	_ = json.Unmarshal(ar.Data, &a)
	if a.Provider.Code != code || a.Provider.Simulated {
		t.Fatalf("routed to %+v", a.Provider)
	}
	fake.status.Store("paid")
	if _, err := s.d.Verification.Payments.PollPayment(ctx(t), a.ID); err != nil {
		t.Fatal(err)
	}
	p.waitStatus(t, a.ID, "SUCCEEDED", 10*time.Second)
	var payload string
	if err := p.mig.QueryRow(ctx(t), `SELECT payload::text FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded'
		AND aggregate_id = $1`, a.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"test_payment": true`) || !strings.Contains(payload, `"execution_environment": "PROVIDER_SANDBOX"`) {
		t.Fatalf("success event does not mark a test payment: %s", payload)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE payment_intent_id = $1 AND execution_environment = 'PROVIDER_SANDBOX'`, a.ID); n < 2 {
		t.Fatalf("evidence rows: %d", n)
	}
	// the simulator is not usable in PROVIDER_SANDBOX
	sim, _ := s.d.Verification.PSP.ProviderByCode(ctx(t), "sandbox")
	if ok, _ := s.d.Verification.PSP.Usable(ctx(t), sim); ok {
		t.Fatal("simulator usable in PROVIDER_SANDBOX")
	}
	// not allow-listed → no route at all
	s3, _ := newSrv("sandbox")
	d3 := verifiedUser(t, s3, "donorpa")
	d3.expect(d3.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1024", "currency": "USD"},
		"Idempotency-Key", idemKey()), 422, "PAYMENT_METHOD_UNAVAILABLE")
	// production status untouched: the fake provider can never be ENABLED for production without selection (DB)
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.payment_providers SET status = 'ENABLED' WHERE id = $1`, provID); err == nil {
		t.Fatal("an unselected provider was enabled for production")
	}
}

func TestPaymentsExecutionIsolationDatabase(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	mustFail := func(what, want, sql string, args ...any) {
		t.Helper()
		_, err := p.mig.Exec(ctx(t), sql, args...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want %q, got %v", what, want, err)
		}
	}
	mustFail("change environment", "execution_environment is immutable",
		`UPDATE app.payment_intents SET execution_environment = 'PROVIDER_SANDBOX' WHERE id = $1`, a.ID)
	cols := `donation_intent_id, campaign_id, currency, amount_minor, method, provider_id, provider_account_id, capability_id, custody_model,
		capability_routable, routing_reason, status, status_rank, correlation_id`
	mustFail("production donation intent", "ck_donation_intents_no_production_stage9",
		`INSERT INTO app.donation_intents (id, campaign_id, donor_user_id, currency, amount_minor, is_anonymous, is_simulated, status, expires_at,
		 correlation_id, idempotency_key, execution_environment) SELECT $2, campaign_id, donor_user_id, currency, amount_minor, false, false,
		 'OPEN', now() + interval '1 hour', 'it', $3, 'PRODUCTION' FROM app.donation_intents WHERE id = (SELECT donation_intent_id
		 FROM app.payment_intents WHERE id = $1)`, a.ID, ids.New(), ids.New())
	mustFail("simulated provider-sandbox donation intent", "ck_donation_intents_simulated_environment",
		`INSERT INTO app.donation_intents (id, campaign_id, donor_user_id, currency, amount_minor, is_anonymous, is_simulated, status, expires_at,
		 correlation_id, idempotency_key, execution_environment) SELECT $2, campaign_id, donor_user_id, currency, amount_minor, false, true,
		 'OPEN', now() + interval '1 hour', 'it', $3, 'PROVIDER_SANDBOX' FROM app.donation_intents WHERE id = (SELECT donation_intent_id
		 FROM app.payment_intents WHERE id = $1)`, a.ID, ids.New(), ids.New())
	// a PRODUCTION payment is refused by the environment guard or the Stage 9 CHECK (both check_violation)
	mustFail("production payment", "SQLSTATE 23514",
		`INSERT INTO app.payment_intents (id, idempotency_key, attempt_seq, is_simulated, execution_environment, `+cols+`)
		 SELECT $2, $3, 99, false, 'PRODUCTION', `+cols+` FROM app.payment_intents WHERE id = $1`, a.ID, ids.New(), ids.New())
	mustFail("simulated provider-sandbox payment", "SQLSTATE 23514",
		`INSERT INTO app.payment_intents (id, idempotency_key, attempt_seq, is_simulated, execution_environment, `+cols+`)
		 SELECT $2, $3, 98, true, 'PROVIDER_SANDBOX', `+cols+` FROM app.payment_intents WHERE id = $1`, a.ID, ids.New(), ids.New())
	mustFail("cross-environment secret", "ck_provider_accounts_secret_env",
		`INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref)
		 SELECT $1, provider_id, 'SANDBOX', 'cross-env', 'secretref://fundzim/production/x/api' FROM app.payment_intents WHERE id = $2`, ids.New(), a.ID)
	mustFail("evidence from another environment", "evidence execution environment differs",
		`INSERT INTO app.payment_provider_evidence (id, payment_intent_id, provider_id, execution_environment, source, raw_status, mapped_status,
		 mapped, mapping_version, observed_at, consistency) SELECT $1, id, provider_id, 'PROVIDER_SANDBOX', 'POLL', 'paid', 'SUCCEEDED', true, 'x',
		 now(), 'FIRST' FROM app.payment_intents WHERE id = $2`, ids.New(), a.ID)
	p.waitStatus(t, a.ID, "PENDING", 5*time.Second)
	if p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE payment_intent_id = $1`, a.ID) < 1 {
		t.Fatal("no evidence recorded for the synchronous response")
	}
	mustFail("evidence is append-only", "append-only", `UPDATE app.payment_provider_evidence SET raw_status = 'x' WHERE payment_intent_id = $1`, a.ID)
	mustFail("evidence cannot be deleted", "append-only", `DELETE FROM app.payment_provider_evidence WHERE payment_intent_id = $1`, a.ID)
	mustFail("error category set once", "append-only",
		`UPDATE app.payment_attempts SET error_category = 'RATE_LIMITED' WHERE payment_intent_id = $1 AND classification <> 'IN_FLIGHT'`, a.ID)
}

// ---- startup gate ---------------------------------------------------------------------------------------

func TestPaymentsStartupGateRefusesProduction(t *testing.T) {
	p := newPayIT(t)
	need(t, "DATABASE_URL", "REDIS_URL")
	for _, tc := range []struct {
		name, env, secrets, want string
	}{
		{"production while live execution is disabled in code", config.ExecProduction, "kv2", "live execution is disabled in code"},
		{"provider sandbox without the secret manager", config.ExecProviderSandbox, "env", "requires the secret manager"},
	} {
		cfg, err := config.FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Payments.SandboxEnabled = false
		cfg.Payments.ExecutionEnvironment = tc.env
		cfg.Secrets.Provider = tc.secrets
		d, err := app.NewDeps(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err == nil {
			d.Close()
			t.Fatalf("%s: the API started", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "startup refused") {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if p.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'psp.execution_gate.evaluated' AND outcome = 'denied'
		AND metadata->>'execution_environment' = 'PRODUCTION' AND occurred_at > now() - interval '5 minutes'`) < 1 {
		t.Fatal("refused startup not audited")
	}
	// the kill switch flag alone cannot enable execution: the code constant is false
	if live, err := p.s.d.Verification.PSP.LiveExecution(ctx(t)); err != nil || live {
		t.Fatalf("live execution: %v %v", live, err)
	}
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'psp.provider.configured'`) < 1 {
		t.Fatal("PROVIDER_CONFIGURED never audited")
	}
}

// ---- evidence and contradiction --------------------------------------------------------------------------

func TestPaymentsContradictoryEvidenceIsEscalated(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	m := p.s.d.Verification
	if err := m.Sandbox.Schedule(ctx(t), a.ID, sandbox.Hook{Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	p.pump(t)
	p.waitStatus(t, a.ID, "SUCCEEDED", 20*time.Second)
	// the provider later reports FAILED for the same payment (contradicts the success evidence)
	var ref string
	if err := p.mig.QueryRow(ctx(t), `SELECT provider_payment_ref FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	sec := []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT"))
	body, sig := signedWebhook(t, "current", sec, time.Now(), sandbox.Payload{ID: "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20],
		Type: "payment.failed", Created: time.Now().Unix(), Simulated: true, Data: sandbox.PayloadData{PaymentReference: a.ID,
			ProviderPaymentID: ref, Status: "failed", AmountMinor: "1024", Currency: "USD"}})
	if st := postWebhook(t, p.s, "sandbox", "application/json", body, sig); st != 200 {
		t.Fatalf("webhook: %d", st)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE payment_intent_id = $1
		AND consistency = 'CONTRADICTS'`, a.ID) == 0 {
		p.pump(t)
		time.Sleep(300 * time.Millisecond)
	}
	if p.status(t, a.ID) != "SUCCEEDED" {
		t.Fatal("contradictory evidence changed the payment status")
	}
	if p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE payment_intent_id = $1 AND consistency = 'CONTRADICTS'
		AND mapped_status = 'FAILED' AND contradicts_evidence_id IS NOT NULL`, a.ID) != 1 ||
		p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = 'CONTRADICTORY_EVIDENCE'
		AND status = 'OPEN'`, a.ID) != 1 ||
		p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.evidence_contradiction' AND aggregate_id = $1`, a.ID) != 1 {
		t.Fatal("contradiction not recorded and escalated exactly once")
	}
	// the not-applied FAILED observation is still in the payment history
	if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND NOT applied AND to_status = 'FAILED'`, a.ID) != 1 {
		t.Fatal("late contradictory evidence missing from the history")
	}
	fin := p.s.staff(t, p.f.finance2)
	ev := fin.do("GET", "/admin/payments/attempts/"+a.ID+"/evidence", nil)
	fin.expect(ev, 200, "")
	if !strings.Contains(string(ev.Data), `"consistency":"CONTRADICTS"`) {
		t.Fatalf("evidence view: %.400s", ev.Data)
	}
}

// ---- error categories, audit events -----------------------------------------------------------------------

func TestPaymentsErrorCategoriesAndAuditEvents(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	dec := p.attempt(t, u, p.intent(t, u, "1002"), "ECOCASH", 201, "")
	tmo := p.attempt(t, u, p.intent(t, u, "1003"), "ECOCASH", 201, "")
	una := p.attempt(t, u, p.intent(t, u, "1019"), "ECOCASH", 201, "")
	for id, want := range map[string]string{dec.ID: "PAYMENT_DECLINED", tmo.ID: "PROVIDER_TIMEOUT", una.ID: "PROVIDER_UNAVAILABLE"} {
		if p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'CREATE' AND error_category = $2`, id, want) != 1 {
			t.Fatalf("%s: error category %s not recorded", id, want)
		}
		if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.provider.response_received' AND target_id = $1
			AND metadata->>'error_category' = $2 AND metadata->>'audit_event' = 'PROVIDER_RESPONSE_RECEIVED'`, id, want) < 1 {
			t.Fatalf("%s: PROVIDER_RESPONSE_RECEIVED not audited with %s", id, want)
		}
	}
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.attempt.created' AND target_id = $1
		AND metadata->>'audit_event' = 'PAYMENT_INITIATED_SANDBOX'`, dec.ID) != 1 {
		t.Fatal("PAYMENT_INITIATED_SANDBOX not audited")
	}
	// timeout_then_success: UNKNOWN resolved by the provider → PAYMENT_STATUS_RECONCILED
	p.waitStatus(t, tmo.ID, "SUCCEEDED", 40*time.Second)
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.payment.status_reconciled' AND target_id = $1
		AND metadata->>'from' = 'UNKNOWN'`, tmo.ID) != 1 {
		t.Fatal("PAYMENT_STATUS_RECONCILED not audited")
	}
	// WEBHOOK_VERIFIED for stored deliveries; WEBHOOK_REJECTED for a bad signature against a known provider
	if p.count(t, `SELECT count(*) FROM audit.audit_events e JOIN app.provider_webhook_inbox i ON i.id = e.target_id
		WHERE e.action = 'psp.webhook.verified' AND i.our_reference = $1`, tmo.ID) < 1 {
		t.Fatal("WEBHOOK_VERIFIED not audited")
	}
	body, sig := signedWebhook(t, "current", []byte("not-the-key-not-the-key-not-the-key-0000"), time.Now(), sandbox.Payload{
		ID: "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20], Type: "payment.paid", Created: time.Now().Unix(), Simulated: true,
		Data: sandbox.PayloadData{PaymentReference: tmo.ID, ProviderPaymentID: "x", Status: "paid"}})
	before := p.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'psp.webhook.rejected' AND metadata->>'reason_code' = 'SIGNATURE_INVALID'`)
	if st := postWebhook(t, p.s, "sandbox", "application/json", body, sig); st != 401 {
		t.Fatalf("bad signature: %d", st)
	}
	if p.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'psp.webhook.rejected' AND metadata->>'reason_code' = 'SIGNATURE_INVALID'`) != before+1 {
		t.Fatal("WEBHOOK_REJECTED not audited")
	}
	// unknown endpoints are not audited (rejection log only)
	before = p.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'psp.webhook.rejected'`)
	_ = postWebhook(t, p.s, "nosuchprovider", "application/json", []byte(`{}`), "")
	if p.count(t, `SELECT count(*) FROM audit.security_audit_events WHERE action = 'psp.webhook.rejected'`) != before {
		t.Fatal("unknown-provider rejections must not flood the audit chain")
	}
	// REFUND_REQUESTED_SANDBOX on a succeeded simulated payment (never executed)
	fin := p.s.staff(t, p.f.finance2)
	rr := fin.do("POST", "/admin/payments/attempts/"+tmo.ID+"/refund-requests", map[string]any{"amount_minor": "1003", "reason_code": "DONOR_REQUEST",
		"reason": "integration test refund request"}, "Idempotency-Key", idemKey())
	if rr.Status == 201 && p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.refund.requested'
		AND metadata->>'audit_event' = 'REFUND_REQUESTED_SANDBOX' AND metadata->>'payment_id' = $1`, tmo.ID) != 1 {
		t.Fatal("REFUND_REQUESTED_SANDBOX not audited")
	} else if rr.Status != 201 && rr.Status != 403 {
		t.Fatalf("refund request: %d %s", rr.Status, rr.Error.Code)
	}
}

// ---- provider controls, health, availability ----------------------------------------------------------------

func TestPaymentsProviderControlsHealthAndAvailability(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	m := p.s.d.Verification
	sim, err := m.PSP.ProviderByCode(ctx(t), "sandbox")
	if err != nil {
		t.Fatal(err)
	}
	fin := p.s.freshStaff(t, p.f.finance2) // fresh login = fresh step-up (finance1's login budget belongs to the Stage 8 tests)
	t.Cleanup(func() {
		// leave the shared simulator enabled and outage-free for the following tests
		_, _ = m.PSP.EnableSandbox(context.Background(), p.f.finance1.id, sim.ID, "restore after the controls test")
		_ = m.PSP.ResolveOutage(context.Background(), p.f.finance1.id, sim.ID, "restore after the controls test")
	})
	base := "/admin/payments/providers/" + sim.ID
	det := fin.do("GET", base, nil)
	fin.expect(det, 200, "")
	for _, want := range []string{`"basis":"SIMULATED"`, `"sandbox_status":"ENABLED"`, `"allowlisted":true`, `"error_categories_24h":{`} {
		if !strings.Contains(string(det.Data), want) {
			t.Fatalf("detail missing %s: %.600s", want, det.Data)
		}
	}
	fin.expect(fin.do("GET", "/admin/payments/providers/"+ids.New(), nil), 404, "PAYMENT_PROVIDER_NOT_FOUND")
	h := fin.do("GET", "/admin/payments/health", nil)
	fin.expect(h, 200, "")
	if !strings.Contains(string(h.Data), `"execution_environment":"LOCAL_SIMULATOR"`) || !strings.Contains(string(h.Data), `"secret_manager":`) {
		t.Fatalf("health view: %.400s", h.Data)
	}

	// reason required; staff without payment.operate refused
	fin.expect(fin.do("POST", base+"/outage", map[string]string{"reason": ""}), 422, "")
	support := p.s.staff(t, p.f.support)
	support.expect(support.do("POST", base+"/outage", map[string]string{"reason": "support cannot declare"}), 403, "")

	// declared outage: health UNAVAILABLE, routing refuses, second declaration conflicts, resolve restores
	fin.expect(fin.do("POST", base+"/outage", map[string]string{"reason": "provider status page reports an outage"}), 200, "")
	fin.expect(fin.do("POST", base+"/outage", map[string]string{"reason": "provider status page reports an outage"}), 409, "PROVIDER_OUTAGE_STATE")
	hs, _ := m.PSP.ProviderHealth(ctx(t), sim)
	if hs.Status != psp.HealthUnavailable || hs.Reason != "OUTAGE_DECLARED" {
		t.Fatalf("health during outage: %+v", hs)
	}
	p.attempt(t, u, p.intentAllowUnavailable(t, u), "ECOCASH", 503, "PAYMENT_PROVIDER_UNAVAILABLE")
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'psp.provider.unavailable' AND target_id = $1
		AND metadata->>'audit_event' = 'PROVIDER_UNAVAILABLE' AND metadata->>'source' = 'STAFF_DECLARED'`, sim.ID) < 1 {
		t.Fatal("PROVIDER_UNAVAILABLE not audited")
	}
	fin.expect(fin.do("POST", base+"/outage/resolve", map[string]string{"reason": "provider recovered"}), 200, "")

	// disable: no method available; enable-sandbox restores; both audited
	fin.expect(fin.do("POST", base+"/disable", map[string]string{"reason": "pause the simulator for this test"}), 200, "")
	av := u.do("GET", "/payment-methods/available?currency=USD", nil)
	u.expect(av, 200, "")
	if strings.Contains(string(av.Data), `"available":true`) {
		t.Fatalf("methods available while the only provider is disabled: %s", av.Data)
	}
	fin.expect(fin.do("POST", base+"/enable-sandbox", map[string]string{"reason": "resume the simulator"}), 200, "")
	for _, ev := range []string{"PROVIDER_DISABLED", "PROVIDER_ENABLED_SANDBOX"} {
		if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE target_id = $1 AND metadata->>'audit_event' = $2`, sim.ID, ev) < 1 {
			t.Fatalf("%s not audited", ev)
		}
	}

	// public availability (anonymous): no provider names, test mode, simulated methods only
	anon := p.s.browser(t)
	av = anon.do("GET", "/payment-methods/available", nil)
	anon.expect(av, 200, "")
	var view struct {
		Currency     string `json:"currency"`
		Env          string `json:"execution_environment"`
		LivePayments bool   `json:"live_payments"`
		TestMode     bool   `json:"test_mode"`
		Methods      []struct {
			Method    string  `json:"method"`
			Available bool    `json:"available"`
			Simulated bool    `json:"simulated"`
			Reason    *string `json:"reason"`
		} `json:"methods"`
	}
	_ = json.Unmarshal(av.Data, &view)
	got := map[string]bool{}
	for _, mm := range view.Methods {
		got[mm.Method] = mm.Available
		if mm.Available && !mm.Simulated {
			t.Fatalf("%s available but not simulated", mm.Method)
		}
		if !mm.Available && (mm.Reason == nil || *mm.Reason != "NOT_AVAILABLE") {
			t.Fatalf("%s: reason %v", mm.Method, mm.Reason)
		}
	}
	if view.Currency != "USD" || view.Env != "LOCAL_SIMULATOR" || view.LivePayments || !view.TestMode || !got["ECOCASH"] || got["BANK_TRANSFER"] ||
		strings.Contains(string(av.Data), "sandbox") {
		t.Fatalf("availability view: %s", av.Data)
	}
	anon.expect(anon.do("GET", "/payment-methods/available?currency=usd", nil), 422, "")
	zwg := anon.do("GET", "/payment-methods/available?currency=ZWG", nil)
	if strings.Contains(string(zwg.Data), `"available":true`) {
		t.Fatalf("ZWG methods available: %s", zwg.Data)
	}
	// a deployment with execution DISABLED says so
	off := newITServer(t, func(c *config.Config) {
		c.Payments.SandboxEnabled, c.Payments.ExecutionEnvironment = false, config.ExecDisabled
	})
	ob := off.browser(t)
	r := ob.do("GET", "/payment-methods/available", nil)
	if !strings.Contains(string(r.Data), `"reason":"EXECUTION_DISABLED"`) || strings.Contains(string(r.Data), `"available":true`) {
		t.Fatalf("disabled deployment: %s", r.Data)
	}

	// no administrator route can fabricate a payment outcome or enable production
	for _, path := range []string{base + "/enable-production", base + "/enable", "/admin/payments/attempts/" + ids.New() + "/status"} {
		if rr := fin.do("POST", path, map[string]string{"reason": "attempt to bypass"}); rr.Status != 404 && rr.Status != 405 {
			t.Fatalf("%s: %d", path, rr.Status)
		}
	}
}

// intentAllowUnavailable creates an intent while the provider may be unavailable: intent creation checks method
// availability, so during a declared outage it must be created by the database fixture path (the API refuses).
func (p *payIT) intentAllowUnavailable(t *testing.T, u vUser) string {
	t.Helper()
	r := u.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1024", "currency": "USD"},
		"Idempotency-Key", idemKey())
	if r.Status == 201 {
		return r.field(t, "id")
	}
	if r.Status != 422 || r.Error.Code != "PAYMENT_METHOD_UNAVAILABLE" {
		t.Fatalf("intent during outage: %d %s", r.Status, r.Error.Code)
	}
	// the API refused (correct). Create the intent row directly so the attempt path's outage refusal is tested too.
	id := ids.New()
	// one statement: the deferred require-event trigger sees the intent and its first event together
	if _, err := p.mig.Exec(ctx(t), `WITH d AS (INSERT INTO app.donation_intents (id, campaign_id, donor_user_id, currency, amount_minor,
		is_anonymous, is_simulated, status, expires_at, correlation_id, idempotency_key, execution_environment)
		VALUES ($1, $2, $3, 'USD', 1024, false, true, 'OPEN', now() + interval '1 hour', $4, $5, 'LOCAL_SIMULATOR') RETURNING id)
		INSERT INTO app.payment_events (id, subject_type, donation_intent_id, to_status, applied, source, actor_type, actor_id)
		SELECT $6, 'DONATION_INTENT', d.id, 'OPEN', true, 'DONOR', 'DONOR', $3 FROM d`,
		id, p.campaign, u.id, "it-"+id[:8], ids.New(), ids.New()); err != nil {
		t.Fatal(err)
	}
	return id
}

// ---- concurrency ------------------------------------------------------------------------------------------

func TestPaymentsStage9Concurrency(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	m := p.s.d.Verification
	sim, _ := m.PSP.ProviderByCode(ctx(t), "sandbox")
	// concurrent outage declarations: exactly one wins, the rest conflict; then resolve
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := m.PSP.DeclareOutage(context.Background(), p.f.finance1.id, sim.ID, "concurrent declaration test")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, psp.ErrOutageState):
				conflicts.Add(1)
			default:
				t.Errorf("declare: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := m.PSP.ResolveOutage(ctx(t), p.f.finance1.id, sim.ID, "concurrent declaration test done"); err != nil {
		t.Fatal(err)
	}
	if wins.Load() != 1 || conflicts.Load() != 7 {
		t.Fatalf("outage declarations: %d won, %d conflicted", wins.Load(), conflicts.Load())
	}
	// the same signed delivery posted concurrently: one inbox row, one evidence row from it, one transition
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	var ref string
	_ = p.mig.QueryRow(ctx(t), `SELECT provider_payment_ref FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&ref)
	evt := "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20]
	body, sig := signedWebhook(t, "current", []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT")), time.Now(), sandbox.Payload{
		ID: evt, Type: "payment.paid", Created: time.Now().Unix(), Simulated: true,
		Data: sandbox.PayloadData{PaymentReference: a.ID, ProviderPaymentID: ref, Status: "paid", AmountMinor: "1024", Currency: "USD"}})
	var ok200 atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if postWebhook(t, p.s, "sandbox", "application/json", body, sig) == 200 {
				ok200.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok200.Load() != 10 {
		t.Fatalf("duplicate deliveries acknowledged: %d/10", ok200.Load())
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.pump(t) }()
	}
	wg.Wait()
	p.waitStatus(t, a.ID, "SUCCEEDED", 20*time.Second)
	if n := p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE provider_event_id = $1`, evt); n != 1 {
		t.Fatalf("inbox rows for one event: %d", n)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_provider_evidence WHERE provider_event_id = $1`, evt); n != 1 {
		t.Fatalf("evidence rows for one event: %d", n)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND applied AND to_status = 'SUCCEEDED'`, a.ID); n != 1 {
		t.Fatalf("SUCCEEDED transitions: %d", n)
	}
}
