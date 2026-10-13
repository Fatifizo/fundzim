package psp

import (
	"context"
	"testing"
)

func TestNormaliseErrorCoversEveryClassAndNeverFailsAnUnknown(t *testing.T) {
	cases := []struct {
		obs  Observation
		want string
	}{
		{Observation{Class: ClassAccepted, RawStatus: "Paid", Mapped: true}, ""},
		{Observation{Class: ClassAccepted, RawStatus: "Weird", Mapped: false}, ErrCatReconciliationRequired},
		{Observation{Class: ClassNotFound}, ""},
		{Observation{Class: ClassAuthConfig}, ErrCatAuthenticationFailed},
		{Observation{Class: ClassInvalidRequest}, ErrCatInvalidRequest},
		{Observation{Class: ClassInvalidRequest, FailureCode: FailureUnsupportedCurrency}, ErrCatUnsupportedCurrency},
		{Observation{Class: ClassNotSupported}, ErrCatUnsupportedMethod},
		{Observation{Class: ClassNotSupported, FailureCode: FailureUnsupportedCurrency}, ErrCatUnsupportedCurrency},
		{Observation{Class: ClassRateLimited}, ErrCatRateLimited},
		{Observation{Class: ClassRetryableBeforeSend}, ErrCatProviderUnavailable},
		{Observation{Class: ClassIndeterminate, UnknownReason: UnknownTimeout}, ErrCatProviderTimeout},
		{Observation{Class: ClassIndeterminate, UnknownReason: UnknownHTTP5xx}, ErrCatUnknownOutcome},
		{Observation{Class: ClassDefiniteFailure, FailureCode: FailureDeclined}, ErrCatPaymentDeclined},
		{Observation{Class: ClassDefiniteFailure, FailureCode: "OTHER"}, ErrCatInvalidRequest},
		{Observation{Class: "SOMETHING_NEW"}, ErrCatUnknownOutcome},
	}
	for _, c := range cases {
		if got := NormaliseError(c.obs); got != c.want {
			t.Errorf("%s/%s/%s: got %q want %q", c.obs.Class, c.obs.FailureCode, c.obs.UnknownReason, got, c.want)
		}
	}
	for _, c := range ErrorCategories {
		if !ValidErrorCategory(c) {
			t.Fatalf("%s has no retry policy", c)
		}
	}
	// an outcome that may have reached the provider is never retried blindly
	for _, c := range []string{ErrCatUnknownOutcome, ErrCatProviderTimeout} {
		if CategoryRetry(c) != RetryStatusQueryOnly {
			t.Fatalf("%s must be STATUS_QUERY_ONLY, got %s", c, CategoryRetry(c))
		}
	}
	if CategoryRetry("NOT_A_CATEGORY") != RetryOperatorReview {
		t.Fatal("unknown category must fail safe")
	}
}

func TestCredentialScope(t *testing.T) {
	ok := Account{Environment: EnvSandbox, APISecretRef: "secretref://fundzim/sandbox/pesepay/api",
		WebhookSecretRef: "secretref://fundzim/sandbox/pesepay/webhook"}
	if !CredentialScopeOK(ok) {
		t.Fatal("sandbox refs under fundzim/sandbox must pass")
	}
	for _, a := range []Account{
		{Environment: EnvSandbox, APISecretRef: "secretref://fundzim/production/pesepay/api"},
		{Environment: EnvProduction, APISecretRef: "secretref://fundzim/sandbox/pesepay/api"},
		{Environment: EnvSandbox, APISecretRef: "secretref://fundzim/sandbox/x/api", WebhookSecretRefPrevious: "secretref://fundzim/production/x/w"},
		{Environment: EnvSandbox, APISecretRef: ""},
		{Environment: EnvSandbox, APISecretRef: "secretref://fundzim/sandboxed/x"},
	} {
		if CredentialScopeOK(a) {
			t.Fatalf("cross-environment credential accepted: %+v", a)
		}
	}
}

func TestExecutionEnvironmentGates(t *testing.T) {
	ctx := context.Background()
	sim := Provider{Code: "sandbox", IsSandbox: true, Status: "ENABLED", SandboxStatus: "ENABLED"}
	real := Provider{Code: "testpsp", Status: "DISABLED", SandboxStatus: "ENABLED", SelectionStatus: "PENDING"}
	cases := []struct {
		name    string
		cfg     Config
		p       Provider
		want    bool
		acctEnv string
	}{
		{"simulator in LOCAL_SIMULATOR", Config{SandboxEnabled: true}, sim, true, EnvSandbox},
		{"simulator not allow-listed", Config{SandboxEnabled: true, Allowlist: map[string]bool{"other": true}}, sim, false, EnvSandbox},
		{"simulator in PROVIDER_SANDBOX", Config{SandboxEnabled: true, ExecEnv: ExecProviderSandbox, Allowlist: map[string]bool{"sandbox": true}}, sim, false, EnvSandbox},
		{"simulator sandbox disabled by staff", Config{SandboxEnabled: true}, Provider{Code: "sandbox", IsSandbox: true, Status: "ENABLED", SandboxStatus: "DISABLED"}, false, EnvSandbox},
		{"real provider in PROVIDER_SANDBOX", Config{ExecEnv: ExecProviderSandbox, Allowlist: map[string]bool{"testpsp": true}}, real, true, EnvSandbox},
		{"real provider not allow-listed", Config{ExecEnv: ExecProviderSandbox, Allowlist: map[string]bool{}}, real, false, EnvSandbox},
		{"real provider in LOCAL_SIMULATOR", Config{SandboxEnabled: true, Allowlist: map[string]bool{"sandbox": true, "testpsp": true}}, real, false, ""},
		{"real provider when DISABLED", Config{ExecEnv: ExecDisabled, Allowlist: map[string]bool{"testpsp": true}}, real, false, ""},
		// PRODUCTION: not selected, so refused before the kill switch is even read (no DB needed)
		{"real provider in PRODUCTION, not selected", Config{ExecEnv: ExecProduction, Allowlist: map[string]bool{"testpsp": true}}, real, false, EnvProduction},
	}
	for _, c := range cases {
		s := &Service{Cfg: normaliseConfig(c.cfg)}
		got, err := s.Usable(ctx, c.p)
		if err != nil || got != c.want {
			t.Errorf("%s: usable=%v err=%v, want %v", c.name, got, err, c.want)
		}
		if env := s.accountEnv(c.p); env != c.acctEnv {
			t.Errorf("%s: account env %q, want %q", c.name, env, c.acctEnv)
		}
	}
	if LiveExecutionEnabled {
		t.Fatal("LiveExecutionEnabled must stay false (CLAUDE.md: ADR + legal sign-off + acceptance)")
	}
}
