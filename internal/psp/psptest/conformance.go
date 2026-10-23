// Package psptest is the provider adapter conformance suite (Stage 9). Every adapter — the FundZim simulator
// today, a real provider's adapter later — must pass it against its sandbox before the provider can be certified
// (docs/stage-9/sandbox-certification.md). It checks the adapter contract only: classification of every outcome,
// error normalisation, webhook authentication and parsing, status mapping that never guesses, limits that fail
// closed, redirect hosts, and that no secret value leaks into an observation. End-to-end behaviour (state machine,
// inbox dedupe, polling, reconciliation) is covered by the integration suite against the same scenarios.
//
// A scenario the harness cannot produce in the provider's sandbox is NOT_TESTED, never PASS. Without sandbox
// access the whole suite is BLOCKED.
package psptest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/psp"
)

// Result statuses (certification checklist vocabulary).
const (
	Pass      = "PASS"
	Fail      = "FAIL"
	Blocked   = "BLOCKED"
	NotTested = "NOT_TESTED"
)

// Scenario codes. The harness maps each create scenario onto something its sandbox can be made to do.
const (
	ScCreateSuccess      = "create_success"
	ScCreateDecline      = "create_decline"
	ScCreateTimeout      = "create_timeout_after_send"
	ScCreateNotSent      = "create_not_sent"
	ScProviderDown       = "provider_unavailable"
	ScIdempotentCreate   = "idempotent_create"
	ScStatusKnown        = "status_query_known"
	ScStatusUnknownRef   = "status_query_unknown_reference"
	ScWebhookCurrent     = "webhook_valid_current_key"
	ScWebhookPrevious    = "webhook_valid_previous_key"
	ScWebhookTampered    = "webhook_tampered_body"
	ScWebhookUnsigned    = "webhook_missing_signature"
	ScWebhookWrongKey    = "webhook_wrong_key"
	ScWebhookStale       = "webhook_stale_timestamp"
	ScWebhookStableID    = "webhook_dedupe_key_stable"
	ScUnmappedStatus     = "unmapped_status_not_guessed"
	ScLimitsFailClosed   = "limits_unknown_method_fail_closed"
	ScRedirectHosts      = "redirect_hosts_well_formed"
	ScNoSecretInResponse = "no_secret_in_observation" // #nosec G101 -- a scenario name, not a credential
)

// Scenarios lists every scenario in report order.
var Scenarios = []string{ScCreateSuccess, ScCreateDecline, ScCreateTimeout, ScCreateNotSent, ScProviderDown, ScIdempotentCreate,
	ScStatusKnown, ScStatusUnknownRef, ScWebhookCurrent, ScWebhookPrevious, ScWebhookTampered, ScWebhookUnsigned, ScWebhookWrongKey,
	ScWebhookStale, ScWebhookStableID, ScUnmappedStatus, ScLimitsFailClosed, ScRedirectHosts, ScNoSecretInResponse}

// Webhook key slots a harness can sign with.
const (
	SlotCurrent  = "current"
	SlotPrevious = "previous"
	SlotWrong    = "wrong"
	SlotNone     = "none"
)

// Harness drives one adapter against its sandbox.
type Harness interface {
	Name() string
	Adapter() psp.Adapter
	Account() psp.Account
	// Request returns a create request that makes the sandbox behave as the scenario (fresh payment id each
	// call), or false when the sandbox cannot produce it.
	Request(scenario string) (psp.CreatePaymentRequest, bool)
	// Webhook returns a delivery the provider would send about a payment, signed with slot at time at, or false
	// when the harness cannot build one (for example a provider without a timestamp has no stale scenario).
	Webhook(paymentID, providerRef, rawStatus, slot string, at time.Time) (psp.Inbound, bool)
	// PaidStatus is a raw status the provider documents for a completed payment.
	PaidStatus() string
	// Secrets lists secret values the adapter uses; none may appear in any observation.
	Secrets(ctx context.Context) [][]byte
}

// Result is one scenario outcome.
type Result struct {
	Scenario string
	Status   string
	Detail   string
}

// Report is a harness run.
type Report struct {
	Harness string
	Results []Result
}

// Count returns how many results have a status.
func (r Report) Count(status string) int {
	n := 0
	for _, x := range r.Results {
		if x.Status == status {
			n++
		}
	}
	return n
}

// Blocked returns a report with every scenario BLOCKED (no sandbox access).
func BlockedReport(name, reason string) Report {
	r := Report{Harness: name}
	for _, sc := range Scenarios {
		r.Results = append(r.Results, Result{Scenario: sc, Status: Blocked, Detail: reason})
	}
	return r
}

// Run executes every scenario. It never panics on adapter misbehaviour: a panic is a FAIL.
func Run(ctx context.Context, h Harness) Report {
	rep := Report{Harness: h.Name()}
	state := &runState{h: h, ctx: ctx}
	for _, sc := range Scenarios {
		rep.Results = append(rep.Results, state.run(sc))
	}
	return rep
}

type runState struct {
	h          Harness
	ctx        context.Context
	successObs *psp.Observation
	successReq psp.CreatePaymentRequest
	observed   []psp.Observation
}

func meta(acct psp.Account, id string) psp.CallMeta {
	now := time.Now()
	return psp.CallMeta{CorrelationID: "conformance", AttemptID: id, Deadline: now.Add(20 * time.Second), Account: acct, ReferenceCreatedAt: now}
}

func (s *runState) run(sc string) (res Result) {
	res.Scenario = sc
	defer func() {
		if p := recover(); p != nil {
			res.Status, res.Detail = Fail, fmt.Sprintf("panic: %v", p)
		}
	}()
	pass := func(format string, a ...any) Result { return Result{sc, Pass, fmt.Sprintf(format, a...)} }
	fail := func(format string, a ...any) Result { return Result{sc, Fail, fmt.Sprintf(format, a...)} }
	notTested := func(why string) Result { return Result{sc, NotTested, why} }
	a, acct := s.h.Adapter(), s.h.Account()
	create := func(scenario string) (psp.CreatePaymentRequest, psp.Observation, bool) {
		req, ok := s.h.Request(scenario)
		if !ok {
			return req, psp.Observation{}, false
		}
		obs := a.CreatePayment(s.ctx, meta(acct, req.PaymentID), req)
		s.observed = append(s.observed, obs)
		return req, obs, true
	}
	switch sc {
	case ScCreateSuccess:
		req, obs, ok := create(sc)
		if !ok {
			return notTested("sandbox cannot produce a successful creation")
		}
		if obs.Class != psp.ClassAccepted || obs.ProviderRef == "" || !obs.Mapped {
			return fail("want ACCEPTED with a provider reference and a mapped status, got %s ref=%q mapped=%v", obs.Class, obs.ProviderRef, obs.Mapped)
		}
		switch obs.Canonical {
		case psp.StatusPending, psp.StatusRequiresAction, psp.StatusAuthorised, psp.StatusSucceeded:
		default:
			return fail("unexpected canonical status after create: %s", obs.Canonical)
		}
		if psp.NormaliseError(obs) != "" {
			return fail("success normalised to error %s", psp.NormaliseError(obs))
		}
		s.successObs, s.successReq = &obs, req
		return pass("ACCEPTED %s (%s)", obs.Canonical, obs.RawStatus)
	case ScCreateDecline:
		_, obs, ok := create(sc)
		if !ok {
			return notTested("sandbox cannot produce a decline")
		}
		if obs.Class != psp.ClassDefiniteFailure || psp.NormaliseError(obs) != psp.ErrCatPaymentDeclined {
			return fail("want DEFINITE_FAILURE/PAYMENT_DECLINED, got %s/%s", obs.Class, psp.NormaliseError(obs))
		}
		return pass("DEFINITE_FAILURE → PAYMENT_DECLINED")
	case ScCreateTimeout:
		_, obs, ok := create(sc)
		if !ok {
			return notTested("sandbox cannot produce a timeout after the request was sent")
		}
		cat := psp.NormaliseError(obs)
		if obs.Class != psp.ClassIndeterminate || psp.CategoryRetry(cat) != psp.RetryStatusQueryOnly {
			return fail("a timeout after send must be INDETERMINATE (status query only), got %s/%s", obs.Class, cat)
		}
		return pass("INDETERMINATE → %s (status query only)", cat)
	case ScCreateNotSent, ScProviderDown:
		_, obs, ok := create(sc)
		if !ok {
			return notTested("sandbox cannot produce this failure")
		}
		if obs.Class == psp.ClassAccepted || obs.Class == psp.ClassDefiniteFailure {
			return fail("an unavailable provider must never be ACCEPTED or a definite failure, got %s", obs.Class)
		}
		return pass("%s → %s", obs.Class, psp.NormaliseError(obs))
	case ScIdempotentCreate:
		if s.successObs == nil {
			return notTested("no successful creation to repeat")
		}
		obs := a.CreatePayment(s.ctx, meta(acct, s.successReq.PaymentID), s.successReq)
		s.observed = append(s.observed, obs)
		if obs.Class != psp.ClassAccepted || obs.ProviderRef != s.successObs.ProviderRef {
			return fail("repeating the same reference created a different payment (%s vs %s)", obs.ProviderRef, s.successObs.ProviderRef)
		}
		return pass("same reference returns the same provider payment")
	case ScStatusKnown:
		if s.successObs == nil {
			return notTested("no successful creation to query")
		}
		obs := a.GetPayment(s.ctx, meta(acct, s.successReq.PaymentID), s.successReq.PaymentID)
		s.observed = append(s.observed, obs)
		if obs.Class != psp.ClassAccepted || !obs.Mapped {
			return fail("status query of a known payment: %s mapped=%v", obs.Class, obs.Mapped)
		}
		return pass("ACCEPTED %s", obs.Canonical)
	case ScStatusUnknownRef:
		req, ok := s.h.Request(ScCreateSuccess)
		if !ok {
			return notTested("no reference generator")
		}
		obs := a.GetPayment(s.ctx, meta(acct, req.PaymentID), req.PaymentID)
		s.observed = append(s.observed, obs)
		if obs.Class == psp.ClassDefiniteFailure || obs.Class == psp.ClassAccepted {
			return fail("an unknown reference must be NOT_FOUND or INDETERMINATE, got %s", obs.Class)
		}
		return pass("%s (final=%v)", obs.Class, obs.NotFoundFinal)
	case ScWebhookCurrent, ScWebhookPrevious:
		slot, want := SlotCurrent, "CURRENT"
		if sc == ScWebhookPrevious {
			slot, want = SlotPrevious, "PREVIOUS"
		}
		in, ok := s.webhook(slot, time.Now())
		if !ok {
			return notTested("harness cannot sign with the " + slot + " key")
		}
		vb, err := a.Verify(s.ctx, acct, in)
		if err != nil || vb.KeySlot != want {
			return fail("verify: err=%v slot=%s", err, vb.KeySlot)
		}
		evs, err := a.Parse(s.ctx, in)
		if err != nil || len(evs) != 1 || evs[0].ProviderEventID == "" || evs[0].RawStatus == "" {
			return fail("parse: err=%v events=%d", err, len(evs))
		}
		return pass("verified (%s) and parsed", vb.Method)
	case ScWebhookTampered:
		in, ok := s.webhook(SlotCurrent, time.Now())
		if !ok {
			return notTested("harness cannot build a webhook")
		}
		in.Body = bytes.Replace(in.Body, []byte(s.h.PaidStatus()), []byte(strings.ToUpper(s.h.PaidStatus())+"x"), 1)
		if _, err := a.Verify(s.ctx, acct, in); err == nil {
			return fail("a tampered body verified")
		}
		return pass("rejected")
	case ScWebhookUnsigned, ScWebhookWrongKey:
		slot := SlotNone
		if sc == ScWebhookWrongKey {
			slot = SlotWrong
		}
		in, ok := s.webhook(slot, time.Now())
		if !ok {
			return notTested("harness cannot build this delivery")
		}
		if _, err := a.Verify(s.ctx, acct, in); err == nil {
			return fail("delivery verified without a valid signature")
		}
		return pass("rejected")
	case ScWebhookStale:
		in, ok := s.webhook(SlotCurrent, time.Now().Add(-2*time.Hour))
		if !ok {
			return notTested("the provider's webhooks carry no timestamp (replay protection must come from status confirmation)")
		}
		in.ReceivedAt = time.Now()
		_, err := a.Verify(s.ctx, acct, in)
		if !errors.Is(err, psp.ErrTimestamp) {
			return fail("a stale delivery must fail with ErrTimestamp, got %v", err)
		}
		return pass("rejected as stale")
	case ScWebhookStableID:
		in, ok := s.webhook(SlotCurrent, time.Now())
		if !ok {
			return notTested("harness cannot build a webhook")
		}
		e1, err1 := a.Parse(s.ctx, in)
		e2, err2 := a.Parse(s.ctx, in)
		if err1 != nil || err2 != nil || len(e1) != 1 || len(e2) != 1 || e1[0].ProviderEventID != e2[0].ProviderEventID {
			return fail("the dedupe key is not stable across re-deliveries")
		}
		return pass("stable event id")
	case ScUnmappedStatus:
		c, mapped, v := a.MapPaymentStatus("conformance_status_that_no_provider_documents")
		if mapped || c != psp.StatusUnknown || v == "" {
			return fail("an undocumented status was mapped to %s (mapped=%v, version=%q)", c, mapped, v)
		}
		return pass("UNKNOWN, unmapped (mapping %s)", v)
	case ScLimitsFailClosed:
		if _, ok := a.Limits("NOT_A_METHOD", "USD"); ok {
			return fail("limits returned for an unknown method")
		}
		if _, ok := a.Limits(psp.MethodEcoCash, "XXX"); ok {
			return fail("limits returned for an unknown currency")
		}
		return pass("unknown method/currency excluded")
	case ScRedirectHosts:
		for _, host := range a.RedirectHosts() {
			if host == "" || strings.ContainsAny(host, "/:*@ ") {
				return fail("malformed redirect host %q", host)
			}
		}
		return pass("%d host(s)", len(a.RedirectHosts()))
	case ScNoSecretInResponse:
		if len(s.observed) == 0 {
			return notTested("no observation to inspect")
		}
		for _, sec := range s.h.Secrets(s.ctx) {
			if len(sec) == 0 {
				continue
			}
			for _, o := range s.observed {
				if strings.Contains(fmt.Sprintf("%+v", o), string(sec)) {
					return fail("a secret value appears in an observation")
				}
			}
		}
		return pass("%d observation(s) clean", len(s.observed))
	}
	return notTested("unknown scenario")
}

// webhook builds a delivery about the successful payment (or a synthetic reference when none exists).
func (s *runState) webhook(slot string, at time.Time) (psp.Inbound, bool) {
	id, ref := "", ""
	if s.successObs != nil {
		id, ref = s.successReq.PaymentID, s.successObs.ProviderRef
	} else if req, ok := s.h.Request(ScCreateSuccess); ok {
		id, ref = req.PaymentID, "conformance-ref"
	} else {
		return psp.Inbound{}, false
	}
	in, ok := s.h.Webhook(id, ref, s.h.PaidStatus(), slot, at)
	if ok && in.Header == nil {
		in.Header = http.Header{}
	}
	if ok && in.ReceivedAt.IsZero() {
		in.ReceivedAt = time.Now()
	}
	return in, ok
}
