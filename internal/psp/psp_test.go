package psp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLiveExecutionIsDisabled(t *testing.T) {
	if LiveExecutionEnabled {
		t.Fatal("LiveExecutionEnabled must stay false until an ADR, legal sign-off and stage acceptance (ADR-039 §6)")
	}
}

func TestEnvVarFor(t *testing.T) {
	cases := map[string]string{
		"secretref://fundzim/sandbox/webhook-current": "SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT",
		"secretref://a1/b.c":                          "SECRETREF_A1_B_C",
	}
	for ref, want := range cases {
		if got, ok := EnvVarFor(ref); !ok || got != want {
			t.Errorf("%s → %s", ref, got)
		}
	}
	for _, bad := range []string{"sk_live_123", "secretref://", "secretref://UP/case", "https://x", "secretref://a"} {
		if _, ok := EnvVarFor(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestEnvResolver(t *testing.T) {
	env := map[string]string{"SECRETREF_FUNDZIM_SANDBOX_API": strings.Repeat("k", 64), "SECRETREF_FUNDZIM_WEAK": "short"}
	r := EnvResolver{Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok }}
	if v, err := r.Resolve(context.Background(), "secretref://fundzim/sandbox/api"); err != nil || len(v) != 64 {
		t.Fatal("resolve")
	}
	for _, ref := range []string{"secretref://fundzim/weak", "secretref://fundzim/missing", "not-a-ref"} {
		if _, err := r.Resolve(context.Background(), ref); !errors.Is(err, ErrSecretUnavailable) {
			t.Errorf("%s: %v", ref, err)
		}
	}
	if err := ErrSecretUnavailable.Error(); strings.Contains(err, "kkkk") {
		t.Fatal("error leaks a value")
	}
	if !Resolvable(context.Background(), r, "secretref://fundzim/sandbox/api") || Resolvable(context.Background(), nil, "x") {
		t.Fatal("Resolvable")
	}
}

func TestMethodCategories(t *testing.T) {
	want := map[string]string{MethodEcoCash: CategoryMobileMoney, MethodOneMoney: CategoryMobileMoney, MethodInnBucks: CategoryMobileMoney,
		MethodOMari: CategoryMobileMoney, MethodZimSwitch: CategoryAccountToAccount, MethodBankTransfer: CategoryBankTransfer, MethodCard: CategoryCard}
	for m, c := range want {
		if MethodCategory(m) != c || !ValidMethod(m) {
			t.Errorf("%s", m)
		}
	}
	if ValidMethod("ZIPIT") || MethodCategory("ZIPIT") != CategoryOther {
		t.Fatal("ZIPIT has no method code until a provider documents it")
	}
}

func TestCursor(t *testing.T) {
	at := time.Date(2026, 10, 12, 9, 0, 0, 123456789, time.UTC)
	next := NextCursor(50, 50, at, "01a12f58-7f81-732c-ae0d-7b4b57a3db51")
	if next == nil {
		t.Fatal("full page must have a cursor")
	}
	ts, id, err := ParseCursor(*next)
	if err != nil || !ts.Equal(at) || id != "01a12f58-7f81-732c-ae0d-7b4b57a3db51" {
		t.Fatalf("round trip %v %s %v", ts, id, err)
	}
	if NextCursor(3, 50, at, "x") != nil {
		t.Fatal("short page has no cursor")
	}
	for _, bad := range []string{"x", "2026-10-12|nope", "|01a12f58-7f81-732c-ae0d-7b4b57a3db51"} {
		if _, _, err := ParseCursor(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if PageLimit(0) != 50 || PageLimit(500) != 50 || PageLimit(10) != 10 {
		t.Fatal("page limit")
	}
}

func TestVerifyReasonMapping(t *testing.T) {
	for err, reason := range map[error]string{ErrSignatureMissing: "SIGNATURE_MISSING", ErrUnknownKey: "UNKNOWN_KEY",
		ErrTimestamp: "TIMESTAMP_OUT_OF_WINDOW", ErrMalformed: "MALFORMED_PAYLOAD", ErrSignatureInvalid: "SIGNATURE_INVALID"} {
		if got, _ := verifyReason(err); got != reason {
			t.Errorf("%v → %s", err, got)
		}
	}
	if h := headerSubset(map[string][]string{"Fundzim-Sandbox-Signature": {"secret"}, "Authorization": {"x"}, "Content-Type": {"application/json"}}); len(h) != 1 {
		t.Fatalf("header subset keeps sensitive headers: %v", h)
	}
}
