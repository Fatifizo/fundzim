package notifications

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Identities returns an actor's linked accounts (kyc.Service.ActorIdentities), for template maker-checker.
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// HTTP serves the notifications API.
type HTTP struct {
	S          *Service
	Identities Identities
}

// HTTPRoute is one notifications route (Idempotent routes get the REQUIRED idempotency middleware; RateLimited routes the
// per-user consent limiter; Webhook the per-IP webhook limiter).
type HTTPRoute struct {
	Pattern     string
	Policy      httpx.Policy
	Handler     http.HandlerFunc
	Idempotent  bool
	Scope       string
	RateLimited bool
	Webhook     bool
}

// Stable error codes.
var (
	errNotFound     = errs.New(errs.NotFound, "NOTIFICATION_NOT_FOUND", "No such notification record.")
	errState        = errs.New(errs.Conflict, "NOTIFICATION_STATE", "This is not possible in the record's current state.")
	errStale        = errs.New(errs.Conflict, "NOTIFICATION_RECORD_CHANGED", "The record changed since you loaded it. Reload and try again.")
	errSelf         = errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "A different person must decide this (not the maker or an account linked to the maker).")
	errMandatory    = errs.New(errs.Unprocessable, "NOTIFICATION_MANDATORY", "Security notifications by e-mail and in the app cannot be turned off.")
	errMarketing    = errs.New(errs.Unprocessable, "MARKETING_NOT_AVAILABLE", "FundZim does not send marketing messages; consent to them cannot be given yet.")
	errInvalid      = errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Check the request and try again.")
	errTemplateBusy = errs.New(errs.Conflict, "NOTIFICATION_TEMPLATE_STATE", "This template already has an open version, or the version is not in a state that allows this.")
)

func mapErr(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errNotFound
	case errors.Is(err, ErrState):
		return errState
	case errors.Is(err, ErrStale):
		return errStale
	case errors.Is(err, ErrSelfApproval):
		return errSelf
	case errors.Is(err, ErrMandatory):
		return errMandatory
	case errors.Is(err, ErrMarketingClosed):
		return errMarketing
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRender):
		return errInvalid
	}
	return err
}

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteError(w, r, h.S.Logger, mapErr(err))
}

func (h *HTTP) ok(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, status, v)
}

func (h *HTTP) respond(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func uid(r *http.Request) string { return authz.PrincipalFrom(r.Context()).UserID }

func decode(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return httpx.DecodeJSON(r, v)
}

func page(items any, next string) map[string]any {
	var nc any
	if next != "" {
		nc = next
	}
	return map[string]any{"items": items, "next_cursor": nc}
}

// Routes lists the notifications API.
func (h *HTTP) Routes() []HTTPRoute {
	auth, user, perm := httpx.Authenticated(), httpx.User(), httpx.Permission
	const n = "/api/v1/notifications"
	const a = "/api/v1/admin/communications"
	const tv = a + "/template-versions/{version_id}"
	view := perm("communications.view")
	return []HTTPRoute{
		// the caller's own in-app notifications (users and staff)
		{Pattern: "GET " + n, Policy: auth, Handler: h.inbox},
		{Pattern: "GET " + n + "/unread-count", Policy: auth, Handler: h.unread},
		{Pattern: "POST " + n + "/read-all", Policy: auth, Handler: h.readAll},
		{Pattern: "POST " + n + "/{notification_id}/read", Policy: auth, Handler: h.read},
		{Pattern: "GET " + n + "/preferences", Policy: user, Handler: h.preferences},
		{Pattern: "PUT " + n + "/preferences", Policy: user, Handler: h.setPreferences, RateLimited: true},
		{Pattern: "GET /api/v1/communications/consent", Policy: user, Handler: h.consents},
		{Pattern: "POST /api/v1/communications/consent", Policy: user, Handler: h.setConsent, RateLimited: true},
		// provider callbacks
		{Pattern: "POST /api/v1/webhooks/notifications/{provider}", Policy: httpx.Public(), Handler: h.webhook, Webhook: true},
		// staff
		{Pattern: "GET " + a + "/overview", Policy: view, Handler: h.overview},
		{Pattern: "GET " + a + "/deliveries", Policy: view, Handler: h.deliveries},
		{Pattern: "GET " + a + "/deliveries/{delivery_id}", Policy: view, Handler: h.delivery},
		{Pattern: "GET " + a + "/dead-letters", Policy: view, Handler: h.deadLetters},
		{Pattern: "POST " + a + "/dead-letters/{dead_letter_id}/replay", Policy: perm("communications.replay"), Handler: h.replay},
		{Pattern: "POST " + a + "/dead-letters/{dead_letter_id}/discard", Policy: perm("communications.replay"), Handler: h.discard},
		{Pattern: "GET " + a + "/providers", Policy: view, Handler: h.providers},
		{Pattern: "GET " + a + "/reports", Policy: view, Handler: h.reports},
		{Pattern: "GET " + a + "/suppressions", Policy: view, Handler: h.suppressions},
		{Pattern: "POST " + a + "/suppressions", Policy: perm("communications.suppression.manage"), Handler: h.suppress, Idempotent: true,
			Scope: "notifications.suppression"},
		{Pattern: "POST " + a + "/suppressions/{suppression_id}/lift", Policy: perm("communications.suppression.manage"), Handler: h.lift},
		{Pattern: "GET " + a + "/consent", Policy: perm("communications.consent.view"), Handler: h.consentOverview},
		{Pattern: "GET " + a + "/templates", Policy: view, Handler: h.templates},
		{Pattern: "GET " + a + "/templates/{template_id}", Policy: view, Handler: h.template},
		{Pattern: "POST " + a + "/templates/{template_id}/versions", Policy: perm("communications.template.request"), Handler: h.createDraft,
			Idempotent: true, Scope: "notifications.template_version"},
		{Pattern: "PUT " + tv, Policy: perm("communications.template.request"), Handler: h.updateDraft},
		{Pattern: "POST " + tv + "/preview", Policy: view, Handler: h.preview},
		{Pattern: "POST " + tv + "/submit", Policy: perm("communications.template.request"), Handler: h.templateAction("submit")},
		{Pattern: "POST " + tv + "/whatsapp-simulator", Policy: perm("communications.template.request"), Handler: h.whatsappSim},
		{Pattern: "POST " + tv + "/approve", Policy: perm("communications.template.approve"), Handler: h.templateAction("approve")},
		{Pattern: "POST " + tv + "/reject", Policy: perm("communications.template.approve"), Handler: h.templateAction("reject")},
		{Pattern: "POST " + tv + "/activate", Policy: perm("communications.template.approve"), Handler: h.templateAction("activate")},
		{Pattern: "POST " + tv + "/retire", Policy: perm("communications.template.approve"), Handler: h.templateAction("retire")},
	}
}

// ---- user ------------------------------------------------------------------------------------------------------------

func (h *HTTP) inbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := h.S.Inbox(r.Context(), uid(r), InboxFilter{UnreadOnly: q.Get("unread") == "true", Category: q.Get("category"), Limit: limit,
		Cursor: q.Get("cursor")})
	h.respond(w, r, page(items, next), err)
}

func (h *HTTP) unread(w http.ResponseWriter, r *http.Request) {
	n, err := h.S.UnreadCount(r.Context(), uid(r))
	h.respond(w, r, map[string]int{"unread": n}, err)
}

func (h *HTTP) read(w http.ResponseWriter, r *http.Request) {
	err := h.S.MarkRead(r.Context(), uid(r), r.PathValue("notification_id"))
	h.respond(w, r, map[string]bool{"read": true}, err)
}

func (h *HTTP) readAll(w http.ResponseWriter, r *http.Request) {
	n, err := h.S.MarkAllRead(r.Context(), uid(r))
	h.respond(w, r, map[string]int{"marked_read": n}, err)
}

func (h *HTTP) preferences(w http.ResponseWriter, r *http.Request) {
	p, err := h.S.Preferences(r.Context(), uid(r))
	h.respond(w, r, map[string]any{"items": p, "quiet_hours": "NOT_AVAILABLE", "marketing_enabled": MarketingEnabled}, err)
}

func (h *HTTP) setPreferences(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Changes []PreferenceChange `json:"changes"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.S.SetPreferences(r.Context(), uid(r), in.Changes); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.S.Preferences(r.Context(), uid(r))
	h.respond(w, r, map[string]any{"items": p, "quiet_hours": "NOT_AVAILABLE", "marketing_enabled": MarketingEnabled}, err)
}

func (h *HTTP) consents(w http.ResponseWriter, r *http.Request) {
	c, err := h.S.Consents(r.Context(), uid(r))
	h.respond(w, r, map[string]any{"items": c, "policy_version": ConsentPolicyVersion,
		"notice": "Consent wording is an engineering placeholder pending legal review (LR-023)."}, err)
}

func (h *HTTP) setConsent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Channel string `json:"channel"`
		Purpose string `json:"purpose"`
		Granted *bool  `json:"granted"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if in.Granted == nil {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "granted", Code: "REQUIRED"}))
		return
	}
	// evidence: this request (its id is in the audit trail and the response headers), never a staff assertion
	evidence := "user-settings:request:" + firstNonEmpty(httpx.RequestID(r.Context()), ids.New()) // the server-assigned id, not a client header
	if len(evidence) > 200 {
		evidence = evidence[:200]
	}
	v, err := h.S.SetConsent(r.Context(), uid(r), in.Channel, in.Purpose, *in.Granted, evidence)
	h.respond(w, r, v, err)
}

// ---- webhook -----------------------------------------------------------------------------------------------------------

func (h *HTTP) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(body) > 8192 {
		h.S.reject(r.Context(), "unknown", "PAYLOAD_INVALID")
		h.fail(w, r, errs.New(errs.Unprocessable, "WEBHOOK_PAYLOAD_INVALID", "The callback payload is invalid."))
		return
	}
	res, err := h.S.HandleWebhook(r.Context(), r.PathValue("provider"), r.Header, body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	switch res.Status {
	case http.StatusOK:
		h.ok(w, r, http.StatusOK, map[string]bool{"received": true, "duplicate": res.Duplicate})
	case http.StatusUnauthorized:
		h.fail(w, r, errs.New(errs.Unauthenticated, "WEBHOOK_SIGNATURE_INVALID", "The callback could not be verified."))
	case http.StatusNotFound:
		h.fail(w, r, errs.New(errs.NotFound, "WEBHOOK_PROVIDER_UNKNOWN", "Unknown provider."))
	default:
		h.fail(w, r, errs.New(errs.Unprocessable, "WEBHOOK_PAYLOAD_INVALID", "The callback payload is invalid."))
	}
}

// ---- staff ------------------------------------------------------------------------------------------------------------

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.S.Overview(r.Context())
	h.respond(w, r, o, err)
}

func (h *HTTP) deliveries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := h.S.Deliveries(r.Context(), DeliveryFilter{Status: q.Get("status"), Channel: q.Get("channel"), TemplateKey: q.Get("template_key"),
		RecipientUserID: q.Get("recipient_user_id"), NotificationID: q.Get("notification_id"), Limit: limit, Cursor: q.Get("cursor")})
	h.respond(w, r, page(items, next), err)
}

func (h *HTTP) delivery(w http.ResponseWriter, r *http.Request) {
	d, err := h.S.Delivery(r.Context(), r.PathValue("delivery_id"))
	h.respond(w, r, d, err)
}

func (h *HTTP) deadLetters(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	if st != "" && !contains([]string{"OPEN", "REPLAYED", "DISCARDED", "ALL"}, st) {
		h.fail(w, r, ErrInvalid)
		return
	}
	items, err := h.S.DeadLetters(r.Context(), st)
	h.respond(w, r, page(items, ""), err)
}

func reasonBody(r *http.Request) (string, error) {
	var in struct {
		Reason string `json:"reason"`
	}
	err := decode(r, &in)
	return in.Reason, err
}

func (h *HTTP) replay(w http.ResponseWriter, r *http.Request) {
	reason, err := reasonBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.S.Replay(r.Context(), uid(r), r.PathValue("dead_letter_id"), reason)
	h.respond(w, r, res, err)
}

func (h *HTTP) discard(w http.ResponseWriter, r *http.Request) {
	reason, err := reasonBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.respond(w, r, map[string]bool{"discarded": true}, h.S.Discard(r.Context(), uid(r), r.PathValue("dead_letter_id"), reason))
}

func (h *HTTP) providers(w http.ResponseWriter, r *http.Request) {
	p, err := h.S.ProviderViews(r.Context())
	h.respond(w, r, map[string]any{"items": p, "live_messaging_enabled": LiveMessagingEnabled, "next_cursor": nil}, err)
}

func (h *HTTP) reports(w http.ResponseWriter, r *http.Request) {
	now := h.S.now()
	from, to := now.Add(-30*24*time.Hour), now
	q := r.URL.Query()
	for name, dst := range map[string]*time.Time{"from": &from, "to": &to} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				h.fail(w, r, httpx.Validation(errs.Detail{Field: name, Code: "INVALID_FORMAT"}))
				return
			}
			*dst = t.UTC()
		}
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "to", Code: "OUT_OF_RANGE"}))
		return
	}
	rep, err := h.S.Reports(r.Context(), from, to)
	h.respond(w, r, rep, err)
}

func (h *HTTP) suppressions(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.Suppressions(r.Context(), r.URL.Query().Get("include_lifted") == "true")
	h.respond(w, r, page(items, ""), err)
}

func (h *HTTP) suppress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID  string `json:"user_id"`
		Channel string `json:"channel"`
		Reason  string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.S.SuppressUser(r.Context(), uid(r), in.UserID, in.Channel, in.Reason); err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, map[string]bool{"suppressed": true})
}

func (h *HTTP) lift(w http.ResponseWriter, r *http.Request) {
	reason, err := reasonBody(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.respond(w, r, map[string]bool{"lifted": true}, h.S.LiftSuppression(r.Context(), uid(r), r.PathValue("suppression_id"), reason))
}

func (h *HTTP) consentOverview(w http.ResponseWriter, r *http.Request) {
	sum, recs, err := h.S.ConsentOverview(r.Context(), r.URL.Query().Get("user_id"))
	h.respond(w, r, map[string]any{"summary": sum, "records": recs, "policy_version": ConsentPolicyVersion}, err)
}

func (h *HTTP) templates(w http.ResponseWriter, r *http.Request) {
	c := r.URL.Query().Get("category")
	if c != "" && !contains(Categories, c) {
		h.fail(w, r, ErrInvalid)
		return
	}
	items, err := h.S.Templates(r.Context(), c)
	h.respond(w, r, page(items, ""), err)
}

func (h *HTTP) template(w http.ResponseWriter, r *http.Request) {
	t, err := h.S.TemplateDetail(r.Context(), r.PathValue("template_id"))
	h.respond(w, r, t, err)
}

func (h *HTTP) linked(ctx context.Context) func(a, b string) bool {
	return func(a, b string) bool {
		if h.Identities == nil {
			return false
		}
		idl, err := h.Identities.ActorIdentities(ctx, b)
		if err != nil {
			return true // fail closed: an identity lookup failure blocks the decision
		}
		return contains(idl, a)
	}
}

func (h *HTTP) createDraft(w http.ResponseWriter, r *http.Request) {
	var in DraftInput
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateDraft(r.Context(), TemplateOp{StaffID: uid(r), TemplateID: r.PathValue("template_id")}, in)
	if errors.Is(err, ErrState) {
		h.fail(w, r, errTemplateBusy)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) updateDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DraftInput
		Version int `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.UpdateDraft(r.Context(), TemplateOp{StaffID: uid(r), VersionID: r.PathValue("version_id"), ExpectedVersion: in.Version}, in.DraftInput)
	h.respond(w, r, v, err)
}

func (h *HTTP) templateAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason  string `json:"reason"`
			Version int    `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		v, err := h.S.TemplateAction(r.Context(), TemplateOp{StaffID: uid(r), VersionID: r.PathValue("version_id"), Reason: in.Reason,
			ExpectedVersion: in.Version}, action, h.linked(r.Context()))
		h.respond(w, r, v, err)
	}
}

func (h *HTTP) whatsappSim(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.SubmitWhatsAppToSimulator(r.Context(), TemplateOp{StaffID: uid(r), VersionID: r.PathValue("version_id")})
	h.respond(w, r, v, err)
}

// preview renders a version with sample values ("[variable]") for every channel it has; nothing is sent or stored.
func (h *HTTP) preview(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.version(r.Context(), r.PathValue("version_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c := Content{EmailSubject: v.EmailSubject, EmailText: v.EmailText, SMSText: v.SMSText, WhatsAppName: v.WhatsAppName, WhatsAppLanguage: v.WhatsAppLanguage,
		InAppTitle: v.InAppTitle, InAppBody: v.InAppBody}
	vars := map[string]string{}
	for _, t := range []string{strOr(c.EmailSubject), strOr(c.EmailText), strOr(c.SMSText), c.InAppTitle, c.InAppBody} {
		for _, p := range Placeholders(t) {
			vars[p] = "[" + p + "]"
		}
	}
	out := map[string]any{}
	for _, ch := range Channels {
		if !c.HasChannel(ch) {
			continue
		}
		rendered, err := Render(c, vars, ch, false)
		if err != nil {
			out[strings.ToLower(ch)] = map[string]string{"error": "RENDER_FAILED"}
			continue
		}
		entry := map[string]any{}
		switch ch {
		case ChInApp:
			entry["title"], entry["body"] = rendered.Title, rendered.Body
		case ChEmail:
			entry["subject"], entry["text"], entry["html"] = rendered.Subject, rendered.Text, rendered.HTML
		case ChSMS:
			est := EstimateSMS(rendered.SMS)
			entry["text"], entry["encoding"], entry["segments"] = rendered.SMS, est.Encoding, est.Segments
		case ChWhatsApp:
			entry["template_name"], entry["language"], entry["parameters"] = rendered.WhatsAppName, rendered.WhatsAppLanguage, rendered.WhatsAppParams
		}
		out[strings.ToLower(ch)] = entry
	}
	h.ok(w, r, http.StatusOK, out)
}
