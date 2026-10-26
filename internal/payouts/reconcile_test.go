package payouts

import (
	"testing"

	"github.com/Fatifizo/fundzim/internal/psp"
)

// F-R2-02: only an authoritative provider answer is compared; every inability to obtain one is reported as unverified
// with its cause and never yields a mismatch (no outcome is inferred from a failed lookup).
func TestJudgeProviderOnlyComparesAuthoritativeAnswers(t *testing.T) {
	amt := int64(5000)
	completed := Payout{ID: "p1", Status: StCompleted, AmountMinor: amt, Currency: "USD"}
	processing := Payout{ID: "p2", Status: StProcessing, AmountMinor: amt, Currency: "USD"}
	other := int64(4999)
	cases := []struct {
		name     string
		p        Payout
		o        psp.Observation
		verified bool
		cause    string
		types    []string
	}{
		{"accepted, consistent", completed, psp.Observation{Class: psp.ClassAccepted, RawStatus: "paid", Canonical: psp.PayoutCompleted, Mapped: true,
			ReportedAmount: &amt, ReportedCcy: "USD"}, true, "", nil},
		{"accepted, amount differs", completed, psp.Observation{Class: psp.ClassAccepted, RawStatus: "paid", Canonical: psp.PayoutCompleted, Mapped: true,
			ReportedAmount: &other, ReportedCcy: "USD"}, true, "", []string{"AMOUNT_MISMATCH"}},
		{"accepted, provider says failed", completed, psp.Observation{Class: psp.ClassAccepted, RawStatus: "failed", Canonical: psp.PayoutFailed,
			Mapped: true}, true, "", []string{"PROVIDER_STATUS_MISMATCH"}},
		{"accepted, still processing at the provider", processing, psp.Observation{Class: psp.ClassAccepted, RawStatus: "pending",
			Canonical: psp.PayoutProcessing, Mapped: true}, true, "", nil},
		{"not found, completed payout", completed, psp.Observation{Class: psp.ClassNotFound, HTTPStatus: 404}, true, "", []string{"PROVIDER_STATUS_MISMATCH"}},
		{"not found, processing payout", processing, psp.Observation{Class: psp.ClassNotFound, HTTPStatus: 404}, true, "", nil},
		// inability to obtain the provider's view
		{"timeout", completed, psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout}, false, "INDETERMINATE:TIMEOUT", nil},
		{"connection failure", completed, psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownConnResetAfter}, false,
			"INDETERMINATE:CONN_RESET_AFTER_SEND", nil},
		{"server error", completed, psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownHTTP5xx, HTTPStatus: 500}, false,
			"INDETERMINATE:HTTP_5XX", nil},
		{"malformed response", completed, psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownMalformed}, false,
			"INDETERMINATE:MALFORMED_RESPONSE", nil},
		{"incomplete response (no status)", completed, psp.Observation{Class: psp.ClassAccepted, ReportedAmount: &other}, false, "INCOMPLETE_RESPONSE", nil},
		{"rate limited", completed, psp.Observation{Class: psp.ClassRateLimited, HTTPStatus: 429}, false, "RATE_LIMITED", nil},
		{"retryable before send", completed, psp.Observation{Class: psp.ClassRetryableBeforeSend}, false, "RETRYABLE_BEFORE_SEND", nil},
		{"authentication / configuration", completed, psp.Observation{Class: psp.ClassAuthConfig, HTTPStatus: 401}, false, "AUTH_CONFIG_ERROR", nil},
		{"query not supported", completed, psp.Observation{Class: psp.ClassNotSupported}, false, "NOT_SUPPORTED", nil},
		{"query refused as invalid", completed, psp.Observation{Class: psp.ClassInvalidRequest, HTTPStatus: 400}, false, "INVALID_REQUEST", nil},
		{"definite failure of the query", completed, psp.Observation{Class: psp.ClassDefiniteFailure}, false, "DEFINITE_FAILURE", nil},
		{"no classification", completed, psp.Observation{}, false, "NO_CLASSIFICATION", nil},
	}
	for _, c := range cases {
		ms, ok, cause := judgeProvider(c.p, c.o)
		if ok != c.verified || cause != c.cause {
			t.Errorf("%s: verified %v cause %q, want %v %q", c.name, ok, cause, c.verified, c.cause)
		}
		var types []string
		for _, m := range ms {
			types = append(types, m.typ)
		}
		if len(types) != len(c.types) || (len(types) > 0 && types[0] != c.types[0]) {
			t.Errorf("%s: mismatches %v, want %v", c.name, types, c.types)
		}
		if !ok && len(ms) != 0 {
			t.Errorf("%s: a failed lookup produced mismatches %v", c.name, types)
		}
	}
}
