package fees

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// Route is one fees route. Idempotent routes get the REQUIRED idempotency middleware (scope below). Staff
// permissions that require step-up (fee.config.request, fee.config.approve, fee.adjust.request, fee.adjust.approve)
// are enforced by the route policy; handlers pass the principal's step-up time to the service, which records it.
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
}

// Routes lists the fee API. There is no free-form posting endpoint: fee journals come only from TEST fixtures and
// TEST payouts.
func (h *HTTP) Routes() []Route {
	user, perm := httpx.User(), httpx.Permission
	const adm = "/api/v1/admin/fees"
	const pv = adm + "/policy-versions/{version_id}"
	return []Route{
		{Pattern: "GET /api/v1/fees/policies/active", Policy: user, Handler: h.activePolicies},
		{Pattern: "POST /api/v1/fees/quotes", Policy: user, Handler: h.createQuote, Idempotent: true, Scope: "fees.quotes.create"},
		{Pattern: "GET /api/v1/fees/quotes/{quote_id}", Policy: user, Handler: h.getQuote},
		{Pattern: "GET /api/v1/campaigns/{campaign_id}/fees", Policy: user, Handler: h.organiser},

		{Pattern: "GET " + adm + "/overview", Policy: perm("fee.view"), Handler: h.overview},
		{Pattern: "GET " + adm + "/policies", Policy: perm("fee.view"), Handler: h.policies},
		{Pattern: "POST " + adm + "/policies", Policy: perm("fee.config.request"), Handler: h.createPolicy, Idempotent: true, Scope: "fees.policies.create"},
		{Pattern: "GET " + adm + "/policies/{policy_id}", Policy: perm("fee.view"), Handler: h.policy},
		{Pattern: "POST " + adm + "/policies/{policy_id}/versions", Policy: perm("fee.config.request"), Handler: h.createVersion, Idempotent: true,
			Scope: "fees.policy_versions.create"},
		{Pattern: "GET " + pv, Policy: perm("fee.view"), Handler: h.version},
		{Pattern: "PUT " + pv, Policy: perm("fee.config.request"), Handler: h.updateDraft},
		{Pattern: "GET " + pv + "/events", Policy: perm("fee.view"), Handler: h.events("fee_policy_version", "version_id")},
		{Pattern: "POST " + pv + "/submit", Policy: perm("fee.config.request"), Handler: h.submit},
		{Pattern: "POST " + pv + "/approve", Policy: perm("fee.config.approve"), Handler: h.decide(true)},
		{Pattern: "POST " + pv + "/reject", Policy: perm("fee.config.approve"), Handler: h.decide(false)},
		{Pattern: "POST " + pv + "/activate", Policy: perm("fee.config.request"), Handler: h.lifecycle("activate")},
		{Pattern: "POST " + pv + "/suspend", Policy: perm("fee.config.request"), Handler: h.lifecycle("suspend")},
		{Pattern: "POST " + pv + "/reactivate", Policy: perm("fee.config.approve"), Handler: h.lifecycle("reactivate")},
		{Pattern: "POST " + pv + "/retire", Policy: perm("fee.config.request"), Handler: h.lifecycle("retire")},

		{Pattern: "GET " + adm + "/provider-schedules", Policy: perm("fee.view"), Handler: h.schedules},
		{Pattern: "POST " + adm + "/provider-schedules", Policy: perm("fee.config.request"), Handler: h.createSchedule, Idempotent: true,
			Scope: "fees.provider_schedules.create"},
		{Pattern: "GET " + adm + "/provider-schedules/{schedule_id}", Policy: perm("fee.view"), Handler: h.schedule},
		{Pattern: "POST " + adm + "/provider-schedules/{schedule_id}/approve", Policy: perm("fee.config.approve"), Handler: h.scheduleDecision(true)},
		{Pattern: "POST " + adm + "/provider-schedules/{schedule_id}/retire", Policy: perm("fee.config.request"), Handler: h.scheduleDecision(false)},

		{Pattern: "GET " + adm + "/quotes", Policy: perm("fee.view"), Handler: h.quotes},
		{Pattern: "GET " + adm + "/quotes/{quote_id}", Policy: perm("fee.view"), Handler: h.staffQuote},
		{Pattern: "GET " + adm + "/assessments", Policy: perm("fee.view"), Handler: h.assessments},
		{Pattern: "GET " + adm + "/assessments/{assessment_id}", Policy: perm("fee.view"), Handler: h.assessment},
		{Pattern: "POST " + adm + "/assessments/{assessment_id}/adjustments", Policy: perm("fee.adjust.request"), Handler: h.requestAdjustment,
			Idempotent: true, Scope: "fees.adjustments.create"},
		{Pattern: "GET " + adm + "/adjustments", Policy: perm("fee.view"), Handler: h.adjustments},
		{Pattern: "POST " + adm + "/adjustments/{adjustment_id}/approve", Policy: perm("fee.adjust.approve"), Handler: h.decideAdjustment(true)},
		{Pattern: "POST " + adm + "/adjustments/{adjustment_id}/reject", Policy: perm("fee.adjust.approve"), Handler: h.decideAdjustment(false)},
		{Pattern: "GET " + adm + "/reconciliation", Policy: perm("fee.view"), Handler: h.reconciliation},
		{Pattern: "POST " + adm + "/reconciliation/run", Policy: perm("fee.adjust.request"), Handler: h.reconcile},
		{Pattern: "POST " + adm + "/reconciliation/{item_id}/resolve", Policy: perm("fee.adjust.request"), Handler: h.resolveRecon},
		{Pattern: "POST " + adm + "/fixtures", Policy: perm("fee.fixtures"), Handler: h.fixture},
		{Pattern: "GET /api/v1/admin/finance/revenue", Policy: perm("fee.view"), Handler: h.revenue},
	}
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

type rateLimited struct {
	after time.Duration
	err   *errs.Error
}

func (e rateLimited) Error() string             { return e.err.Error() }
func (e rateLimited) Unwrap() error             { return e.err }
func (e rateLimited) RetryAfter() time.Duration { return e.after }

func (h *HTTP) throttle(r *http.Request) error {
	if h.Limiter == nil {
		return errors.New("fees: rate limiter not configured")
	}
	d, err := h.Limiter.Allow(r.Context(), ratelimit.DefaultPolicies().FeesQuoteUser, principal(r).UserID)
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
	if n <= 0 || n > 200 {
		n = 50
	}
	return n
}

func list[T any](items []T) map[string]any { return map[string]any{"items": items, "next_cursor": nil} }

// ---- user ----------------------------------------------------------------------------------------------------------

func (h *HTTP) activePolicies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.ActivePolicies(r.Context(), q.Get("currency"), q.Get("transaction_type"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil, "fee_environment": EnvTest, "label": TestLabel})
}

func (h *HTTP) createQuote(w http.ResponseWriter, r *http.Request) {
	if err := h.throttle(r); err != nil {
		h.fail(w, r, err)
		return
	}
	var in QuoteRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	q, err := h.S.CreateQuote(WithCorrelation(r.Context(), r.Header.Get("X-Correlation-ID")), principal(r).UserID, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, q)
}

func (h *HTTP) getQuote(w http.ResponseWriter, r *http.Request) {
	q, err := h.S.GetQuote(r.Context(), principal(r).UserID, r.PathValue("quote_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, q)
}

func (h *HTTP) organiser(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.OrganiserFees(r.Context(), principal(r).UserID, r.PathValue("campaign_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

// ---- staff: policies -------------------------------------------------------------------------------------------------

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.StaffOverview(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) policies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.Policies(r.Context(), PolicyFilter{Category: q.Get("category"), Currency: q.Get("currency"), Status: q.Get("status")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, list(items))
}

func (h *HTTP) createPolicy(w http.ResponseWriter, r *http.Request) {
	var in NewPolicy
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.CreatePolicy(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, p)
}

func (h *HTTP) policy(w http.ResponseWriter, r *http.Request) {
	p, err := h.S.Policy(r.Context(), r.PathValue("policy_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, p)
}

func (h *HTTP) createVersion(w http.ResponseWriter, r *http.Request) {
	var in VersionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateVersion(r.Context(), principal(r).UserID, r.PathValue("policy_id"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) version(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.Version(r.Context(), r.PathValue("version_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) updateDraft(w http.ResponseWriter, r *http.Request) {
	var in VersionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.UpdateDraft(r.Context(), principal(r).UserID, r.PathValue("version_id"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) events(aggType, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := h.S.Events(r.Context(), aggType, r.PathValue(param))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, list(items))
	}
}

func (h *HTTP) submit(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.Submit(r.Context(), principal(r).UserID, r.PathValue("version_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) decide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in Decision
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		p := principal(r)
		var v Version
		var err error
		if approve {
			v, err = h.S.Approve(r.Context(), p.UserID, p.StepUpAt, r.PathValue("version_id"), in)
		} else {
			v, err = h.S.Reject(r.Context(), p.UserID, p.StepUpAt, r.PathValue("version_id"), in)
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, v)
	}
}

func (h *HTTP) lifecycle(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		if action != "activate" {
			if err := httpx.DecodeJSON(r, &in); err != nil {
				h.fail(w, r, err)
				return
			}
		}
		p := principal(r)
		id := r.PathValue("version_id")
		var v Version
		var err error
		switch action {
		case "activate":
			v, err = h.S.Activate(r.Context(), p.UserID, p.StepUpAt, id)
		case "suspend":
			v, err = h.S.Suspend(r.Context(), p.UserID, p.StepUpAt, id, in.Reason)
		case "reactivate":
			v, err = h.S.Reactivate(r.Context(), p.UserID, p.StepUpAt, id, in.Reason)
		case "retire":
			v, err = h.S.Retire(r.Context(), p.UserID, p.StepUpAt, id, in.Reason)
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, v)
	}
}

// ---- staff: provider evidence ------------------------------------------------------------------------------------

func (h *HTTP) schedules(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.Schedules(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, list(items))
}

func (h *HTTP) createSchedule(w http.ResponseWriter, r *http.Request) {
	var in NewSchedule
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateSchedule(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) schedule(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.Schedule(r.Context(), r.PathValue("schedule_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) scheduleDecision(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		if !approve {
			if err := httpx.DecodeJSON(r, &in); err != nil {
				h.fail(w, r, err)
				return
			}
		}
		p := principal(r)
		var v Schedule
		var err error
		if approve {
			v, err = h.S.ApproveSchedule(r.Context(), p.UserID, p.StepUpAt, r.PathValue("schedule_id"))
		} else {
			v, err = h.S.RetireSchedule(r.Context(), p.UserID, p.StepUpAt, r.PathValue("schedule_id"), in.Reason)
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, v)
	}
}

// ---- staff: quotes, assessments, adjustments ----------------------------------------------------------------------

func (h *HTTP) quotes(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.Quotes(r.Context(), r.URL.Query().Get("status"), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, list(items))
}

func (h *HTTP) staffQuote(w http.ResponseWriter, r *http.Request) {
	q, err := h.S.StaffQuote(r.Context(), r.PathValue("quote_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, q)
}

func (h *HTTP) assessments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.Assessments(r.Context(), AssessmentFilter{CampaignID: q.Get("campaign_id"), SourceType: q.Get("source_type"),
		Category: q.Get("category"), Currency: q.Get("currency"), Limit: limitParam(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, list(items))
}

// AssessmentDetail is an assessment with its journals, adjustments, provider reports and history.
type AssessmentDetail struct {
	Assessment  Assessment      `json:"assessment"`
	Journals    []AccountingRef `json:"journals"`
	Adjustments []Adjustment    `json:"adjustments"`
	Events      []Event         `json:"events"`
}

func (h *HTTP) assessment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("assessment_id")
	a, err := h.S.Assessment(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := AssessmentDetail{Assessment: a}
	if d.Journals, err = h.S.AccountingRefs(r.Context(), id); err == nil {
		if d.Adjustments, err = h.S.Adjustments(r.Context(), AdjustmentFilter{AssessmentID: id, Limit: 500}); err == nil {
			d.Events, err = h.S.Events(r.Context(), "fee_assessment", id)
		}
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, d)
}

func (h *HTTP) requestAdjustment(w http.ResponseWriter, r *http.Request) {
	var in ManualAdjustment
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.S.RequestAdjustment(r.Context(), principal(r).UserID, r.PathValue("assessment_id"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, a)
}

func (h *HTTP) adjustments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.Adjustments(r.Context(), AdjustmentFilter{AssessmentID: q.Get("assessment_id"), Status: q.Get("status"), Reason: q.Get("reason"),
		CampaignID: q.Get("campaign_id"), Limit: limitParam(r)})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, list(items))
}

func (h *HTTP) decideAdjustment(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		p := principal(r)
		a, err := h.S.DecideAdjustment(r.Context(), p.UserID, p.StepUpAt, r.PathValue("adjustment_id"), approve, in.Reason)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.ok(w, r, http.StatusOK, a)
	}
}

// ---- staff: reconciliation, revenue, fixtures -----------------------------------------------------------------------

func (h *HTTP) reconciliation(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.ReconItems(r.Context(), r.URL.Query().Get("status"), limitParam(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	run, err := h.S.LatestRun(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nil, "latest_run": run, "settlement_reconciliation": "NOT_PERFORMED"})
}

func (h *HTTP) reconcile(w http.ResponseWriter, r *http.Request) {
	run, err := h.S.Reconcile(r.Context(), "staff:"+principal(r).UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, run)
}

func (h *HTTP) resolveRecon(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.ResolveRecon(r.Context(), principal(r).UserID, r.PathValue("item_id"), in.Note)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) revenue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := h.S.now()
	from, to := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0), now.Add(time.Second)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			h.fail(w, r, validation("from", "INVALID_FORMAT"))
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			h.fail(w, r, validation("to", "INVALID_FORMAT"))
			return
		}
		to = t
	}
	if !to.After(from) || to.Sub(from) > 3*366*24*time.Hour {
		h.fail(w, r, validation("to", "INVALID_RANGE"))
		return
	}
	v, err := h.S.Revenue(r.Context(), from, to)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func (h *HTTP) fixture(w http.ResponseWriter, r *http.Request) {
	var in FixtureRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.RunFixture(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}
