package payouts

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// Route is one payouts route. Idempotent routes get the REQUIRED idempotency middleware (scope below). Staff
// permissions requiring step-up (payout.approve, payout.reject, payout.operate) are enforced by the route policy;
// owner step-up is checked in the handler (user routes have no permission).
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
	Scope      string
}

// HTTP adds the request-side dependencies (rate limiting).
type HTTP struct {
	S       *Service
	Limiter ratelimit.Limiter
	Sandbox *sandbox.Adapter // nil unless the simulator runs (development and test only)
}

var policies = ratelimit.DefaultPolicies()

// Routes is the Stage 11 payouts API (the Stage 2 design paths, ADR-043 §9).
func (h *HTTP) Routes() []Route {
	user, perm := httpx.User(), httpx.Permission
	const c = "/api/v1/campaigns/{campaign_id}"
	const pr = "/api/v1/payout-requests"
	const adm = "/api/v1/admin/payout-requests"
	const ops = "/api/v1/admin/payouts"
	rs := []Route{
		{Pattern: "GET " + c + "/payout-eligibility", Policy: user, Handler: h.availability},
		{Pattern: "POST " + c + "/payout-requests", Policy: user, Handler: h.request, Idempotent: true, Scope: "payouts.requests.create"},
		{Pattern: "GET " + c + "/payout-requests", Policy: user, Handler: h.campaignList},
		{Pattern: "GET " + pr, Policy: user, Handler: h.mine},
		{Pattern: "GET " + pr + "/{payout_request_id}", Policy: user, Handler: h.ownerGet},
		{Pattern: "GET " + pr + "/{payout_request_id}/timeline", Policy: user, Handler: h.timeline},
		{Pattern: "POST " + pr + "/{payout_request_id}/cancel", Policy: user, Handler: h.ownerCancel},

		{Pattern: "GET " + adm, Policy: perm("payout.view"), Handler: h.staffList},
		{Pattern: "GET " + adm + "/{payout_request_id}", Policy: perm("payout.view"), Handler: h.staffGet},
		{Pattern: "GET " + adm + "/{payout_request_id}/events", Policy: perm("payout.view"), Handler: h.staffEvents},
		{Pattern: "POST " + adm + "/{payout_request_id}/approve", Policy: perm("payout.approve"), Handler: h.decide("APPROVE")},
		{Pattern: "POST " + adm + "/{payout_request_id}/reject", Policy: perm("payout.reject"), Handler: h.decide("REJECT")},
		{Pattern: "POST " + adm + "/{payout_request_id}/cancel", Policy: perm("payout.operate"), Handler: h.staffCancel},
		{Pattern: "POST " + adm + "/{payout_request_id}/eligibility-checks", Policy: perm("payout.operate"), Handler: h.recheck},
		{Pattern: "POST " + adm + "/{payout_request_id}/status-checks", Policy: perm("payout.operate"), Handler: h.statusCheck},
		{Pattern: "GET " + ops + "/overview", Policy: perm("payout.view"), Handler: h.overview},
		{Pattern: "GET " + ops + "/reservations", Policy: perm("payout.view"), Handler: h.reservations},
		{Pattern: "GET " + ops + "/exceptions", Policy: perm("payout.view"), Handler: h.exceptions},
		{Pattern: "POST " + ops + "/exceptions/{exception_id}/investigate", Policy: perm("payout.operate"), Handler: h.exception("investigate")},
		{Pattern: "POST " + ops + "/exceptions/{exception_id}/resolve", Policy: perm("payout.operate"), Handler: h.exception("resolve")},
		{Pattern: "GET " + ops + "/reconciliation", Policy: perm("payout.view"), Handler: h.reconciliation},
		{Pattern: "POST " + ops + "/reconciliation/run", Policy: perm("payout.operate"), Handler: h.reconcile},
		{Pattern: "GET " + ops + "/providers", Policy: perm("payout.view"), Handler: h.providers},
	}
	if h.Sandbox != nil {
		rs = append(rs,
			Route{Pattern: "GET " + ops + "/sandbox/{payout_request_id}", Policy: perm("payout.sandbox.control"), Handler: h.sandboxInspect},
			Route{Pattern: "POST " + ops + "/sandbox/{payout_request_id}/webhooks", Policy: perm("payout.sandbox.control"), Handler: h.sandboxSchedule})
	}
	return rs
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, err error) {
	noStore(w)
	httpx.WriteError(w, r, h.S.Logger, err)
}

func (h *HTTP) ok(w http.ResponseWriter, r *http.Request, status int, v any) {
	noStore(w)
	httpx.WriteData(w, r, status, v)
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func (h *HTTP) fresh(r *http.Request) bool {
	return principal(r).StepUpFresh(h.S.now(), h.S.StepUpMaxAge)
}

type rateLimited struct {
	after time.Duration
	err   *errs.Error
}

func (e rateLimited) Error() string             { return e.err.Error() }
func (e rateLimited) Unwrap() error             { return e.err }
func (e rateLimited) RetryAfter() time.Duration { return e.after }

func (h *HTTP) throttle(r *http.Request) error {
	if h.Limiter == nil {
		return errors.New("payouts: rate limiter not configured")
	}
	d, err := h.Limiter.Allow(r.Context(), policies.PayoutsRequestUser, principal(r).UserID)
	if err != nil {
		return err
	}
	if !d.Allowed {
		return rateLimited{after: d.RetryAfter, err: errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Try again later.")}
	}
	return nil
}

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 100 {
		n = 50
	}
	return n
}

// ---- owner ---------------------------------------------------------------------------------------------------------

func (h *HTTP) availability(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	amount, _ := strconv.ParseInt(q.Get("amount_minor"), 10, 64)
	v, err := h.S.Availability(r.Context(), principal(r).UserID, r.PathValue("campaign_id"), q.Get("currency"), q.Get("destination_id"), amount)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) request(w http.ResponseWriter, r *http.Request) {
	if err := h.throttle(r); err != nil {
		h.fail(w, r, err)
		return
	}
	var in RequestInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.RequestPayout(r.Context(), principal(r).UserID, h.fresh(r), r.PathValue("campaign_id"), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusAccepted, p.Owner())
}

func (h *HTTP) campaignList(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.OwnerList(r.Context(), principal(r).UserID, r.PathValue("campaign_id"), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) mine(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.OwnerList(r.Context(), principal(r).UserID, "", limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) ownerGet(w http.ResponseWriter, r *http.Request) {
	p, err := h.S.OwnerPayout(r.Context(), principal(r).UserID, r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, p.Owner())
}

func (h *HTTP) timeline(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.OwnerTimeline(r.Context(), principal(r).UserID, r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) ownerCancel(w http.ResponseWriter, r *http.Request) {
	if err := h.throttle(r); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.CancelByOwner(r.Context(), principal(r).UserID, h.fresh(r), r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, p.Owner())
}

// ---- staff ---------------------------------------------------------------------------------------------------------

func (h *HTTP) staffList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := h.S.ListPayouts(r.Context(), StaffFilter{Status: q.Get("status"), CampaignID: q.Get("campaign_id"),
		NeedsReconciliation: q.Get("needs_reconciliation") == "true"}, q.Get("cursor"), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Payout{}
	}
	var nc any
	if next != nil {
		nc = *next
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nc})
}

func (h *HTTP) staffGet(w http.ResponseWriter, r *http.Request) {
	d, err := h.S.StaffDetail(r.Context(), r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, d)
}

func (h *HTTP) staffEvents(w http.ResponseWriter, r *http.Request) {
	d, err := h.S.StaffDetail(r.Context(), r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": d.Events, "next_cursor": nil})
}

func (h *HTTP) decide(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in DecisionInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		p := principal(r)
		var out Payout
		var err error
		if decision == "APPROVE" {
			out, err = h.S.ApprovePayout(r.Context(), p.UserID, p.StepUpAt, r.PathValue("payout_request_id"), in)
		} else {
			out, err = h.S.RejectPayout(r.Context(), p.UserID, p.StepUpAt, r.PathValue("payout_request_id"), in)
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, out)
	}
}

func (h *HTTP) staffCancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.CancelByStaff(r.Context(), principal(r).UserID, r.PathValue("payout_request_id"), in.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, p)
}

func (h *HTTP) recheck(w http.ResponseWriter, r *http.Request) {
	d, err := h.S.Recheck(r.Context(), principal(r).UserID, r.PathValue("payout_request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, d)
}

func (h *HTTP) statusCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("payout_request_id")
	if err := h.S.StatusQuery(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.payout(r.Context(), h.S.Pool, id, false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, p)
}

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.S.StaffOverview(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, o)
}

func (h *HTTP) reservations(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.Reservations(r.Context(), r.URL.Query().Get("status"), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Reservation{}
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) exceptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.Exceptions(r.Context(), ExceptionFilter{Status: q.Get("status"), Severity: q.Get("severity"), PayoutID: q.Get("payout_request_id"),
		All: q.Get("include_resolved") == "true"})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []Exception{}
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) exception(step string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note           string `json:"note"`
			ResolutionCode string `json:"resolution_code"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		var err error
		if step == "investigate" {
			err = h.S.InvestigateException(r.Context(), principal(r).UserID, r.PathValue("exception_id"), in.Note)
		} else {
			err = h.S.ResolveException(r.Context(), principal(r).UserID, r.PathValue("exception_id"), in.ResolutionCode, in.Note)
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, map[string]any{"id": r.PathValue("exception_id"), "status": map[string]string{"investigate": "INVESTIGATING",
			"resolve": "RESOLVED"}[step]})
	}
}

func (h *HTTP) reconciliation(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.ReconReferences(r.Context(), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil,
		"settlement_reconciliation": "NOT_PERFORMED: no provider disbursement statements (PCR-007, PCR-016; Stage 17)"})
}

func (h *HTTP) reconcile(w http.ResponseWriter, r *http.Request) {
	rep, err := h.S.Reconcile(r.Context(), "STAFF:"+principal(r).UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, rep)
}

func (h *HTTP) providers(w http.ResponseWriter, r *http.Request) {
	caps, err := h.S.PSP.PayoutCapabilities(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	type row struct {
		CapabilityID   string   `json:"capability_id"`
		ProviderID     string   `json:"provider_id"`
		ProviderCode   string   `json:"provider_code"`
		IsSandbox      bool     `json:"is_sandbox"`
		Version        int      `json:"capability_version"`
		Currency       string   `json:"currency"`
		Rails          []string `json:"payout_rails"`
		EvidenceStatus string   `json:"evidence_status"`
		Routable       bool     `json:"routable"`
		StatusAPI      bool     `json:"status_api"`
		SignedWebhooks bool     `json:"signed_webhooks"`
		SupportsCancel string   `json:"payout_cancellation"`
		Limits         string   `json:"payout_limits"`
		Settlement     string   `json:"settlement_requirements"`
		Confirmation   string   `json:"confirmation_status"`
	}
	out := []row{}
	for _, c := range caps {
		p, err := h.S.PSP.ProviderByID(r.Context(), c.ProviderID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		rr := row{CapabilityID: c.ID, ProviderID: c.ProviderID, ProviderCode: p.Code, IsSandbox: p.IsSandbox, Version: c.Version, Currency: c.Currency,
			Rails: c.Rails, EvidenceStatus: c.EvidenceStatus, Routable: c.Routable, StatusAPI: c.StatusAPI, SignedWebhooks: c.SignedWebhooks,
			SupportsCancel: "UNKNOWN", Limits: "UNKNOWN", Settlement: "UNKNOWN", Confirmation: "UNKNOWN"}
		if p.IsSandbox {
			rr.SupportsCancel, rr.Limits, rr.Settlement, rr.Confirmation = "NOT_SUPPORTED (simulator)", "simulator contract: USD 1.00 – 10 000.00",
				"NOT_APPLICABLE (simulator)", "SIMULATOR_CONTRACT (not a real provider)"
		}
		out = append(out, rr)
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": out, "next_cursor": nil,
		"note": "No production payout provider is confirmed (ADR-040). Unverified values are UNKNOWN."})
}

func (h *HTTP) sandboxInspect(w http.ResponseWriter, r *http.Request) {
	st, err := h.Sandbox.InspectPayout(r.Context(), r.PathValue("payout_request_id"))
	if errors.Is(err, sandbox.ErrNoSimulatedPayout) {
		h.fail(w, r, ErrPayoutNotFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, st)
}

func (h *HTTP) sandboxSchedule(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status       string `json:"status"`
		DelaySeconds int    `json:"delay_seconds"`
		Copies       int    `json:"copies"`
		SignWith     string `json:"sign_with"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, ok := map[string]bool{"processing": true, "completed": true, "failed": true, "returned": true}[in.Status]; !ok ||
		in.DelaySeconds < 0 || in.DelaySeconds > 3600 || in.Copies < 0 || in.Copies > 5 {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"}))
		return
	}
	if in.SignWith != "" && in.SignWith != "current" && in.SignWith != "previous" && in.SignWith != "wrong" && in.SignWith != "none" {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "sign_with", Code: "INVALID_VALUE"}))
		return
	}
	err := h.Sandbox.SchedulePayout(r.Context(), r.PathValue("payout_request_id"), sandbox.Hook{Status: in.Status,
		After: time.Duration(in.DelaySeconds) * time.Second, Copies: in.Copies, SignWith: in.SignWith})
	if errors.Is(err, sandbox.ErrNoSimulatedPayout) {
		h.fail(w, r, ErrPayoutNotFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusAccepted, map[string]any{"scheduled": true, "simulated": true})
}
