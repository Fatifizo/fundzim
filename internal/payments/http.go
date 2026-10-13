package payments

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// Route is one payments route. Idempotent routes get the REQUIRED idempotency middleware (scope below).
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
	Scope      string
}

// Routes is the payments API (interface-contracts §2, §4). Staff permissions requiring step-up (payment.operate,
// refund.approve) are enforced by the route policy.
func (s *Service) Routes(sb *sandbox.Adapter) []Route {
	user, public, perm := httpx.User(), httpx.Public(), httpx.Permission
	const di = "/api/v1/donation-intents/{donation_intent_id}"
	const adm = "/api/v1/admin/payments"
	rs := []Route{
		{Pattern: "POST /api/v1/donation-intents", Policy: user, Handler: s.handleCreateIntent, Idempotent: true, Scope: "payments.donation_intents.create"},
		{Pattern: "GET /api/v1/donation-intents", Policy: user, Handler: s.handleListIntents},
		{Pattern: "GET " + di, Policy: user, Handler: s.handleGetIntent},
		{Pattern: "POST " + di + "/attempts", Policy: user, Handler: s.handleCreateAttempt, Idempotent: true, Scope: "payments.attempts.create"},
		{Pattern: "GET " + di + "/attempts", Policy: user, Handler: s.handleListAttempts},
		{Pattern: "GET " + di + "/status", Policy: user, Handler: s.handleStatus},
		{Pattern: "POST " + di + "/cancel", Policy: user, Handler: s.handleCancel},
		{Pattern: "POST /api/v1/payment-sessions/{session_id}/return", Policy: user, Handler: s.handleSessionReturn},
		{Pattern: "GET /api/v1/payment-methods", Policy: public, Handler: s.handleMethods},
		{Pattern: "GET /api/v1/payment-methods/available", Policy: public, Handler: s.handleAvailable},
		{Pattern: "GET /api/v1/payment-providers/capabilities", Policy: public, Handler: s.handleCapabilities},

		{Pattern: "GET " + adm + "/overview", Policy: perm("payment.view"), Handler: s.handleOverview},
		{Pattern: "GET " + adm + "/intents", Policy: perm("payment.view"), Handler: s.handleAdminIntents},
		{Pattern: "GET " + adm + "/intents/{donation_intent_id}", Policy: perm("payment.view"), Handler: s.handleAdminIntent},
		{Pattern: "GET " + adm + "/attempts", Policy: perm("payment.view"), Handler: s.handleAdminAttempts},
		{Pattern: "GET " + adm + "/attempts/{payment_id}", Policy: perm("payment.view"), Handler: s.handleAdminAttempt},
		{Pattern: "GET " + adm + "/unknown", Policy: perm("payment.view"), Handler: s.handleUnknown},
		{Pattern: "POST " + adm + "/attempts/{payment_id}/status-query", Policy: perm("payment.operate"), Handler: s.handleStatusQuery},
		{Pattern: "GET " + adm + "/webhooks", Policy: perm("payment.view"), Handler: s.handleWebhooks},
		{Pattern: "GET " + adm + "/webhooks/{inbox_id}", Policy: perm("payment.view"), Handler: s.handleWebhook},
		{Pattern: "POST " + adm + "/webhooks/{inbox_id}/requeue", Policy: perm("payment.operate"), Handler: s.handleRequeue},
		{Pattern: "GET " + adm + "/webhook-rejections", Policy: perm("payment.view"), Handler: s.handleRejections},
		{Pattern: "GET " + adm + "/providers", Policy: perm("payment.view"), Handler: s.handleProviders},
		{Pattern: "GET " + adm + "/providers/{provider_id}", Policy: perm("payment.view"), Handler: s.handleProvider},
		{Pattern: "POST " + adm + "/providers/{provider_id}/enable-sandbox", Policy: perm("payment.operate"), Handler: s.providerAction(actEnableSandbox)},
		{Pattern: "POST " + adm + "/providers/{provider_id}/disable", Policy: perm("payment.operate"), Handler: s.providerAction(actDisable)},
		{Pattern: "POST " + adm + "/providers/{provider_id}/outage", Policy: perm("payment.operate"), Handler: s.providerAction(actDeclareOutage)},
		{Pattern: "POST " + adm + "/providers/{provider_id}/outage/resolve", Policy: perm("payment.operate"), Handler: s.providerAction(actResolveOutage)},
		{Pattern: "GET " + adm + "/health", Policy: perm("payment.view"), Handler: s.handleHealth},
		{Pattern: "GET " + adm + "/attempts/{payment_id}/evidence", Policy: perm("payment.view"), Handler: s.handleEvidence},
		{Pattern: "GET " + adm + "/reconciliation", Policy: perm("payment.view"), Handler: s.handleRecon},
		{Pattern: "POST " + adm + "/reconciliation/{reference_id}/await-settlement", Policy: perm("payment.operate"), Handler: s.handleAwaitSettlement},
		{Pattern: "POST " + adm + "/attempts/{payment_id}/refund-requests", Policy: perm("refund.request"), Handler: s.handleRequestRefund,
			Idempotent: true, Scope: "payments.refund_requests.create"},
		{Pattern: "GET " + adm + "/refund-requests", Policy: perm("payment.view"), Handler: s.handleRefunds},
		{Pattern: "POST " + adm + "/refund-requests/{refund_request_id}/approve", Policy: perm("refund.approve"), Handler: s.decide(DecideApprove)},
		{Pattern: "POST " + adm + "/refund-requests/{refund_request_id}/reject", Policy: perm("refund.approve"), Handler: s.decide(DecideReject)},
		{Pattern: "POST " + adm + "/refund-requests/{refund_request_id}/hold", Policy: perm("refund.approve"), Handler: s.decide(DecideHold)},
		{Pattern: "POST " + adm + "/refund-requests/{refund_request_id}/resume", Policy: perm("refund.approve"), Handler: s.decide(DecideResume)},
		{Pattern: "POST " + adm + "/refund-requests/{refund_request_id}/withdraw", Policy: perm("refund.request"), Handler: s.decide(DecideWithdraw)},
	}
	// development-only simulator controls: registered only when the sandbox runs (never in production)
	sc := sandboxControl{s: s, sb: sb}
	rs = append(rs,
		Route{Pattern: "GET " + adm + "/sandbox/attempts/{payment_id}", Policy: perm("payment.sandbox.control"), Handler: sc.inspect},
		Route{Pattern: "POST " + adm + "/sandbox/attempts/{payment_id}/webhooks", Policy: perm("payment.sandbox.control"), Handler: sc.schedule})
	return rs
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	noStore(w)
	httpx.WriteError(w, r, s.Logger, err)
}

func (s *Service) ok(w http.ResponseWriter, r *http.Request, status int, v any) {
	noStore(w)
	httpx.WriteData(w, r, status, v)
}

// page renders {items, next_cursor} (ADR-038 §7).
func page[T any](items []T, next *string) map[string]any {
	if items == nil {
		items = []T{}
	}
	var nc any
	if next != nil {
		nc = *next
	}
	return map[string]any{"items": items, "next_cursor": nc}
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// throttle applies a protective rate limit; the error carries the real wait for Retry-After.
func (s *Service) throttle(r *http.Request, p ratelimit.Policy, key string) error {
	if s.Limiter == nil {
		return errors.New("payments: rate limiter not configured")
	}
	d, err := s.Limiter.Allow(r.Context(), p, key)
	if err != nil {
		return err
	}
	if !d.Allowed {
		return rateLimited{after: d.RetryAfter, err: errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Try again later.")}
	}
	return nil
}

type rateLimited struct {
	after time.Duration
	err   *errs.Error
}

func (e rateLimited) Error() string             { return e.err.Error() }
func (e rateLimited) Unwrap() error             { return e.err }
func (e rateLimited) RetryAfter() time.Duration { return e.after }

var policies = ratelimit.DefaultPolicies()

// ---- donor --------------------------------------------------------------------------------------------------

func (s *Service) handleCreateIntent(w http.ResponseWriter, r *http.Request) {
	var in CreateIntentInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principal(r)
	if err := s.throttle(r, policies.PaymentsIntentUser, p.UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.throttle(r, policies.PaymentsIntentCampaign, in.CampaignID); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.CreateIntent(r.Context(), p.UserID, r.Header.Get("Idempotency-Key"), httpx.RequestID(r.Context()), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusCreated, v)
}

func (s *Service) handleListIntents(w http.ResponseWriter, r *http.Request) {
	items, next, err := s.ListIntents(r.Context(), principal(r).UserID, r.URL.Query().Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleGetIntent(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetIntent(r.Context(), principal(r).UserID, r.PathValue("donation_intent_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleCreateAttempt(w http.ResponseWriter, r *http.Request) {
	var in CreateAttemptInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principal(r)
	if err := s.throttle(r, policies.PaymentsAttemptUser, p.UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.CreateAttempt(r.Context(), p.UserID, r.PathValue("donation_intent_id"), r.Header.Get("Idempotency-Key"),
		httpx.RequestID(r.Context()), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusCreated, v)
}

func (s *Service) handleListAttempts(w http.ResponseWriter, r *http.Request) {
	items, err := s.ListAttempts(r.Context(), principal(r).UserID, r.PathValue("donation_intent_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, nil))
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.Status(r.Context(), principal(r).UserID, r.PathValue("donation_intent_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	v, err := s.CancelIntent(r.Context(), principal(r).UserID, r.PathValue("donation_intent_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleSessionReturn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.ReturnFromSession(r.Context(), principal(r).UserID, r.PathValue("session_id"), in.Token)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"attempt": v})
}

func (s *Service) handleMethods(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ccy := q.Get("currency")
	if cid := q.Get("campaign_id"); cid != "" {
		t, err := s.checkCampaign(r.Context(), cid)
		if err != nil {
			s.ok(w, r, http.StatusOK, map[string]any{"items": []psp.MethodOption{}})
			return
		}
		if ccy == "" {
			ccy = t.GoalCurrency
		}
		if t.GoalCurrency != ccy {
			s.ok(w, r, http.StatusOK, map[string]any{"items": []psp.MethodOption{}})
			return
		}
	}
	if len(ccy) != 3 {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "currency", Code: "INVALID_FORMAT"}))
		return
	}
	items, err := s.PSP.AvailableMethods(r.Context(), ccy, s.Simulated())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	items, err := s.PSP.PublicCapabilities(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"items": items})
}

// ---- staff --------------------------------------------------------------------------------------------------

func (s *Service) handleOverview(w http.ResponseWriter, r *http.Request) {
	v, err := s.AdminOverview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleAdminIntents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.AdminIntents(r.Context(), q.Get("status"), q.Get("campaign_id"), q.Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleAdminIntent(w http.ResponseWriter, r *http.Request) {
	v, err := s.AdminIntentDetail(r.Context(), r.PathValue("donation_intent_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleAdminAttempts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.AdminAttempts(r.Context(), q.Get("status"), q.Get("method"), q.Get("needs_reconciliation") == "true",
		q.Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleAdminAttempt(w http.ResponseWriter, r *http.Request) {
	v, err := s.AdminAttemptDetail(r.Context(), r.PathValue("payment_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleUnknown(w http.ResponseWriter, r *http.Request) {
	items, next, err := s.UnknownQueue(r.Context(), r.URL.Query().Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

type reasonBody struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

func (s *Service) handleStatusQuery(w http.ResponseWriter, r *http.Request) {
	var in reasonBody
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.RequestStatusQuery(r.Context(), principal(r).UserID, r.PathValue("payment_id"), in.Reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"payment_id": r.PathValue("payment_id"), "status": st})
}

func (s *Service) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.PSP.InboxList(r.Context(), q.Get("status"), q.Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	v, err := s.PSP.InboxGet(r.Context(), r.PathValue("inbox_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleRequeue(w http.ResponseWriter, r *http.Request) {
	var in reasonBody
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	reason, ok := cleanReason(in.Reason)
	if !ok {
		s.fail(w, r, invalidReason("reason"))
		return
	}
	if err := s.PSP.Requeue(r.Context(), principal(r).UserID, r.PathValue("inbox_id"), reason); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.PSP.InboxGet(r.Context(), r.PathValue("inbox_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleRejections(w http.ResponseWriter, r *http.Request) {
	items, next, err := s.PSP.Rejections(r.Context(), r.URL.Query().Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleProviders(w http.ResponseWriter, r *http.Request) {
	v, err := s.PSP.AdminProviders(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleProvider(w http.ResponseWriter, r *http.Request) {
	v, err := s.PSP.AdminProviderDetail(r.Context(), r.PathValue("provider_id"))
	if errors.Is(err, psp.ErrProviderNotFound) {
		err = ErrProviderNotFound
	}
	if err == nil {
		err = s.errorCategoryCounts(r.Context(), v.ID, v.ErrorCategories24h)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

const (
	actEnableSandbox = iota
	actDisable
	actDeclareOutage
	actResolveOutage
)

// providerAction runs a staff provider control (payment.operate with step-up, by route policy). None of them can
// enable production execution or change a payment.
func (s *Service) providerAction(act int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		reason, ok := cleanReason(in.Reason)
		if !ok {
			s.fail(w, r, invalidReason("reason"))
			return
		}
		ctx, staff, id := r.Context(), principal(r).UserID, r.PathValue("provider_id")
		var err error
		switch act {
		case actEnableSandbox:
			_, err = s.PSP.EnableSandbox(ctx, staff, id, reason)
		case actDisable:
			_, err = s.PSP.Disable(ctx, staff, id, reason)
		case actDeclareOutage:
			err = s.PSP.DeclareOutage(ctx, staff, id, reason)
		case actResolveOutage:
			err = s.PSP.ResolveOutage(ctx, staff, id, reason)
		}
		if errors.Is(err, psp.ErrProviderNotFound) {
			err = ErrProviderNotFound
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.handleProvider(w, r)
	}
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	v, err := s.PSP.AdminHealth(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleEvidence(w http.ResponseWriter, r *http.Request) {
	items, err := s.PaymentEvidence(r.Context(), r.PathValue("payment_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleAvailable(w http.ResponseWriter, r *http.Request) {
	ccy := r.URL.Query().Get("currency")
	if ccy == "" {
		ccy = "USD"
	}
	if len(ccy) != 3 || strings.ToUpper(ccy) != ccy {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "currency", Code: "INVALID_FORMAT"}))
		return
	}
	v, err := s.AvailablePaymentMethods(r.Context(), ccy)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, v)
}

func (s *Service) handleRecon(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.ReconList(r.Context(), q.Get("status"), q.Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) handleAwaitSettlement(w http.ResponseWriter, r *http.Request) {
	var in reasonBody
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.AwaitSettlement(r.Context(), principal(r).UserID, r.PathValue("reference_id"), in.Note); err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, map[string]any{"id": r.PathValue("reference_id"), "status": "AWAITING_SETTLEMENT_DATA"})
}

func (s *Service) handleRequestRefund(w http.ResponseWriter, r *http.Request) {
	var in RefundInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.RequestRefund(r.Context(), principal(r).UserID, r.PathValue("payment_id"), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusCreated, v)
}

func (s *Service) handleRefunds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.ListRefunds(r.Context(), q.Get("status"), q.Get("cursor"), limitParam(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, http.StatusOK, page(items, next))
}

func (s *Service) decide(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		p := principal(r)
		v, err := s.DecideRefund(r.Context(), p.UserID, r.PathValue("refund_request_id"), decision, in.Reason, p.StepUpAt)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.ok(w, r, http.StatusOK, v)
	}
}

// ---- development-only simulator control ------------------------------------------------------------------------

type sandboxControl struct {
	s  *Service
	sb *sandbox.Adapter
}

func (c sandboxControl) enabled(w http.ResponseWriter, r *http.Request) bool {
	if c.sb == nil || !c.s.PSP.Cfg.SandboxEnabled {
		c.s.fail(w, r, ErrSandboxDisabled)
		return false
	}
	return true
}

func (c sandboxControl) inspect(w http.ResponseWriter, r *http.Request) {
	if !c.enabled(w, r) {
		return
	}
	st, err := c.sb.Inspect(r.Context(), r.PathValue("payment_id"))
	if errors.Is(err, sandbox.ErrNoSimulatedPayment) {
		c.s.fail(w, r, ErrPaymentNotFound)
		return
	}
	if err != nil {
		c.s.fail(w, r, err)
		return
	}
	c.s.ok(w, r, http.StatusOK, st)
}

var sandboxStatuses = map[string]bool{"pending": true, "action_required": true, "paid": true, "failed": true, "expired": true,
	"cancelled": true, "limbo": true, "refunded": true, "partially_refunded": true}

func (c sandboxControl) schedule(w http.ResponseWriter, r *http.Request) {
	if !c.enabled(w, r) {
		return
	}
	var in struct {
		Status                 string `json:"status"`
		DelaySeconds           int    `json:"delay_seconds"`
		Copies                 int    `json:"copies"`
		AmountMinor            string `json:"amount_minor"`
		SignWith               string `json:"sign_with"`
		TimestampOffsetSeconds int    `json:"timestamp_offset_seconds"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		c.s.fail(w, r, err)
		return
	}
	var det []errs.Detail
	if !sandboxStatuses[in.Status] {
		det = append(det, errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	if in.DelaySeconds < 0 || in.DelaySeconds > 3600 {
		det = append(det, errs.Detail{Field: "delay_seconds", Code: "OUT_OF_RANGE"})
	}
	if in.Copies < 0 || in.Copies > 10 {
		det = append(det, errs.Detail{Field: "copies", Code: "OUT_OF_RANGE"})
	}
	if in.SignWith != "" && in.SignWith != "current" && in.SignWith != "previous" && in.SignWith != "wrong" {
		det = append(det, errs.Detail{Field: "sign_with", Code: "INVALID_VALUE"})
	}
	if in.TimestampOffsetSeconds < -86400 || in.TimestampOffsetSeconds > 86400 {
		det = append(det, errs.Detail{Field: "timestamp_offset_seconds", Code: "OUT_OF_RANGE"})
	}
	var amt int64
	if in.AmountMinor != "" {
		if !amountRE.MatchString(in.AmountMinor) {
			det = append(det, errs.Detail{Field: "amount_minor", Code: "INVALID_FORMAT"})
		}
		amt, _ = strconv.ParseInt(in.AmountMinor, 10, 64)
	}
	if len(det) > 0 {
		c.s.fail(w, r, httpx.Validation(det...))
		return
	}
	err := c.sb.Schedule(r.Context(), r.PathValue("payment_id"), sandbox.Hook{Status: in.Status, After: time.Duration(in.DelaySeconds) * time.Second,
		Copies: in.Copies, SignWith: in.SignWith, TimestampOffset: in.TimestampOffsetSeconds, Amount: amt})
	if errors.Is(err, sandbox.ErrNoSimulatedPayment) {
		c.s.fail(w, r, ErrPaymentNotFound)
		return
	}
	if err != nil {
		c.s.fail(w, r, err)
		return
	}
	c.s.ok(w, r, http.StatusAccepted, map[string]any{"scheduled": true, "simulated": true})
}
