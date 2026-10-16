package risk

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Route is one risk route. Idempotent routes get the REQUIRED idempotency middleware (scope below). Permissions that
// require step-up (every risk mutation) are enforced by the route policy; handlers pass the principal's step-up time to
// the service, which records it where a checker decides. Risk data is staff-only: there is no public or owner route
// (owners only ever see a coarse "on hold pending a review" from the enforcing module).
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
	Scope      string
}

// HTTP serves the risk operations API.
type HTTP struct {
	S *Service
	// Sandbox is the screening simulator (nil: sandbox control routes are not registered).
	Sandbox *SandboxScreening
}

// Routes lists the risk operations API.
func (h *HTTP) Routes() []Route {
	perm := httpx.Permission
	const adm = "/api/v1/admin/risk"
	const rv = adm + "/rule-versions/{version_id}"
	const cs = adm + "/cases/{case_id}"
	const hd = adm + "/holds/{hold_id}"
	view := perm("risk.operations.view")
	rt := []Route{
		{Pattern: "GET " + adm + "/overview", Policy: view, Handler: h.overview},
		{Pattern: "GET " + adm + "/signals", Policy: view, Handler: h.signals},
		{Pattern: "GET " + adm + "/evaluations", Policy: view, Handler: h.evaluations},
		{Pattern: "GET " + adm + "/evaluations/{evaluation_id}", Policy: view, Handler: h.evaluation},
		{Pattern: "GET " + adm + "/monitoring", Policy: view, Handler: h.monitoring},
		{Pattern: "GET " + adm + "/quality", Policy: view, Handler: h.quality},
		{Pattern: "POST " + adm + "/reconciliation/run", Policy: perm("risk.case.work"), Handler: h.reconcile},

		{Pattern: "GET " + adm + "/rules", Policy: view, Handler: h.rules},
		{Pattern: "POST " + adm + "/rules", Policy: perm("risk.rule.request"), Handler: h.createRule, Idempotent: true, Scope: "risk.rules.create"},
		{Pattern: "GET " + adm + "/rules/{rule_id}", Policy: view, Handler: h.rule},
		{Pattern: "POST " + adm + "/rules/{rule_id}/versions", Policy: perm("risk.rule.request"), Handler: h.createRuleVersion, Idempotent: true,
			Scope: "risk.rule_versions.create"},
		{Pattern: "GET " + rv, Policy: view, Handler: h.ruleVersion},
		{Pattern: "PUT " + rv, Policy: perm("risk.rule.request"), Handler: h.updateRuleDraft},
		{Pattern: "GET " + rv + "/events", Policy: view, Handler: h.events("risk_rule_version", "version_id")},
		{Pattern: "POST " + rv + "/simulate", Policy: perm("risk.rule.request"), Handler: h.simulate},
		{Pattern: "POST " + rv + "/submit", Policy: perm("risk.rule.request"), Handler: h.ruleAction("submit")},
		{Pattern: "POST " + rv + "/approve", Policy: perm("risk.rule.approve"), Handler: h.ruleAction("approve")},
		{Pattern: "POST " + rv + "/reject", Policy: perm("risk.rule.approve"), Handler: h.ruleAction("reject")},
		{Pattern: "POST " + rv + "/activate", Policy: perm("risk.rule.request"), Handler: h.ruleAction("activate")},
		{Pattern: "POST " + rv + "/suspend", Policy: perm("risk.rule.request"), Handler: h.ruleAction("suspend")},
		{Pattern: "POST " + rv + "/reactivate", Policy: perm("risk.rule.approve"), Handler: h.ruleAction("reactivate")},
		{Pattern: "POST " + rv + "/retire", Policy: perm("risk.rule.request"), Handler: h.ruleAction("retire")},

		{Pattern: "GET " + adm + "/cases", Policy: view, Handler: h.cases},
		{Pattern: "POST " + adm + "/cases", Policy: perm("risk.case.work"), Handler: h.openCase, Idempotent: true, Scope: "risk.cases.create"},
		{Pattern: "GET " + cs, Policy: view, Handler: h.caseView},
		{Pattern: "GET " + cs + "/evidence/{evidence_id}", Policy: view, Handler: h.evidence},
		{Pattern: "POST " + cs + "/assign", Policy: perm("risk.case.work"), Handler: h.caseAction("assign")},
		{Pattern: "POST " + cs + "/transition", Policy: perm("risk.case.work"), Handler: h.caseAction("transition")},
		{Pattern: "POST " + cs + "/evidence", Policy: perm("risk.case.work"), Handler: h.addEvidence, Idempotent: true, Scope: "risk.case_evidence.create"},
		{Pattern: "POST " + cs + "/signal-reviews", Policy: perm("risk.case.work"), Handler: h.caseAction("review_signal")},
		{Pattern: "POST " + cs + "/escalate", Policy: perm("risk.case.work"), Handler: h.caseAction("escalate")},
		{Pattern: "POST " + cs + "/propose-decision", Policy: perm("risk.case.work"), Handler: h.caseAction("propose")},
		{Pattern: "POST " + cs + "/approve-decision", Policy: perm("risk.case.approve"), Handler: h.caseAction("approve")},
		{Pattern: "POST " + cs + "/return-decision", Policy: perm("risk.case.approve"), Handler: h.caseAction("return")},
		{Pattern: "POST " + cs + "/reopen", Policy: perm("risk.case.work"), Handler: h.caseAction("reopen")},

		{Pattern: "GET " + adm + "/holds", Policy: view, Handler: h.holds},
		{Pattern: "POST " + adm + "/holds", Policy: perm("risk.hold.create"), Handler: h.createHold, Idempotent: true, Scope: "risk.holds.create"},
		{Pattern: "GET " + hd, Policy: view, Handler: h.hold},
		{Pattern: "GET " + hd + "/events", Policy: view, Handler: h.events("risk_hold", "hold_id")},
		{Pattern: "POST " + hd + "/activate", Policy: perm("risk.hold.release"), Handler: h.holdAction("activate")},
		{Pattern: "POST " + hd + "/revoke", Policy: perm("risk.hold.create"), Handler: h.holdAction("revoke")},
		{Pattern: "POST " + hd + "/review", Policy: perm("risk.hold.create"), Handler: h.holdAction("review")},
		{Pattern: "POST " + hd + "/request-release", Policy: perm("risk.hold.create"), Handler: h.holdAction("request_release")},
		{Pattern: "POST " + hd + "/approve-release", Policy: perm("risk.hold.release"), Handler: h.holdAction("approve_release")},
		{Pattern: "POST " + hd + "/reject-release", Policy: perm("risk.hold.release"), Handler: h.holdAction("reject_release")},

		{Pattern: "GET " + adm + "/screening", Policy: view, Handler: h.screenings},
		{Pattern: "GET " + adm + "/screening/capabilities", Policy: view, Handler: h.capabilities},
		{Pattern: "POST " + adm + "/screening", Policy: perm("risk.screening.request"), Handler: h.requestScreening, Idempotent: true,
			Scope: "risk.screening.create"},
		{Pattern: "GET " + adm + "/screening/{request_id}", Policy: view, Handler: h.screening},
		{Pattern: "POST " + adm + "/screening/{request_id}/review", Policy: perm("risk.screening.review"), Handler: h.reviewScreening},
		{Pattern: "POST " + adm + "/screening/{request_id}/retry", Policy: perm("risk.screening.request"), Handler: h.retryScreening},
	}
	if h.Sandbox != nil {
		rt = append(rt, Route{Pattern: "POST " + adm + "/sandbox/screening/deliver", Policy: perm("risk.sandbox.control"), Handler: h.sandboxDeliver},
			Route{Pattern: "POST " + adm + "/sandbox/screening/poll", Policy: perm("risk.sandbox.control"), Handler: h.sandboxPoll})
	}
	return rt
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

func (h *HTTP) respond(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, v)
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 200 {
		n = 50
	}
	return n
}

func list[T any](items []T, err error) (map[string]any, error) {
	return map[string]any{"items": items, "next_cursor": nil}, err
}

func expectVersion(r *http.Request) int {
	n, _ := strconv.Atoi(r.Header.Get("If-Match-Version"))
	return n
}

// ---- dashboards ------------------------------------------------------------------------------------------------------

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.S.RiskOverview(r.Context())
	h.respond(w, r, o, err)
}

func (h *HTTP) signals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := list(h.S.Signals(r.Context(), SignalFilter{Category: q.Get("category"), SignalType: q.Get("signal_type"), SubjectType: q.Get("subject_type"),
		SubjectID: q.Get("subject_id"), Limit: limitParam(r)}))
	h.respond(w, r, v, err)
}

func (h *HTTP) evaluations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := list(h.S.Evaluations(r.Context(), q.Get("classification"), q.Get("subject_type"), q.Get("subject_id"), limitParam(r)))
	h.respond(w, r, v, err)
}

func (h *HTTP) evaluation(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.EvaluationDetail(r.Context(), r.PathValue("evaluation_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) monitoring(w http.ResponseWriter, r *http.Request) {
	v, err := list(h.S.Monitoring(r.Context(), r.URL.Query().Get("event_type"), limitParam(r)))
	h.respond(w, r, v, err)
}

func (h *HTTP) quality(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	v, err := h.S.Quality(r.Context(), days)
	h.respond(w, r, v, err)
}

func (h *HTTP) reconcile(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.Reconcile(r.Context(), "staff:"+principal(r).UserID)
	h.respond(w, r, v, err)
}

func (h *HTTP) events(aggType, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := list(h.S.history(r.Context(), aggType, r.PathValue(param)))
		h.respond(w, r, v, err)
	}
}

// ---- rules -----------------------------------------------------------------------------------------------------------

func (h *HTTP) rules(w http.ResponseWriter, r *http.Request) {
	v, err := list(h.S.Rules(r.Context()))
	h.respond(w, r, v, err)
}

func (h *HTTP) rule(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.RiskRule(r.Context(), r.PathValue("rule_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) createRule(w http.ResponseWriter, r *http.Request) {
	var in NewRule
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateRule(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) createRuleVersion(w http.ResponseWriter, r *http.Request) {
	var in RuleVersionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateRuleVersion(r.Context(), principal(r).UserID, r.PathValue("rule_id"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) ruleVersion(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.RuleVersion(r.Context(), r.PathValue("version_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) updateRuleDraft(w http.ResponseWriter, r *http.Request) {
	var in RuleVersionInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.UpdateRuleDraft(r.Context(), principal(r).UserID, r.PathValue("version_id"), expectVersion(r), in)
	h.respond(w, r, v, err)
}

func (h *HTTP) simulate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SubjectID string `json:"subject_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	ev, err := h.S.SimulateRule(r.Context(), r.PathValue("version_id"), in.SubjectID)
	if err != nil {
		if errors.Is(err, ErrRule) {
			err = validation("definition", "INVALID_RULE")
		}
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"score": ev.Score, "classification": ev.Classification, "band_source": ev.BandSource,
		"confidence": ev.Confidence, "recommended_action": ev.RecommendedAction, "results": ev.Outcomes, "explanation": ev.Factors,
		"simulation": true, "notice": EvaluationNotice})
}

type actionBody struct {
	Reason         string `json:"reason"`
	DefinitionHash string `json:"definition_hash"`
	Version        int    `json:"version"`
}

func decodeAction(r *http.Request) (actionBody, error) {
	var in actionBody
	if r.ContentLength == 0 {
		return in, nil
	}
	err := httpx.DecodeJSON(r, &in)
	return in, err
}

func (h *HTTP) ruleAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, err := decodeAction(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		p := principal(r)
		op := RuleOp{StaffID: p.UserID, StepUpAt: p.StepUpAt, VersionID: r.PathValue("version_id"), ExpectedVersion: in.Version, Reason: in.Reason,
			DefinitionHash: in.DefinitionHash}
		var v RuleVersion
		switch action {
		case "submit":
			v, err = h.S.SubmitRuleVersion(r.Context(), op)
		case "approve":
			v, err = h.S.DecideRuleVersion(r.Context(), op, true)
		case "reject":
			v, err = h.S.DecideRuleVersion(r.Context(), op, false)
		case "activate":
			v, err = h.S.ActivateRuleVersion(r.Context(), op)
		case "suspend":
			v, err = h.S.SuspendRuleVersion(r.Context(), op)
		case "reactivate":
			v, err = h.S.ReactivateRuleVersion(r.Context(), op)
		case "retire":
			v, err = h.S.RetireRuleVersion(r.Context(), op)
		}
		h.respond(w, r, v, err)
	}
}

// ---- cases -----------------------------------------------------------------------------------------------------------

func (h *HTTP) cases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	assigned := q.Get("assigned_to")
	if assigned == "me" {
		assigned = principal(r).UserID
	}
	v, err := list(h.S.Cases(r.Context(), CaseFilter{Status: q.Get("status"), Priority: q.Get("priority"), CaseType: q.Get("case_type"),
		AssignedTo: assigned, SubjectType: q.Get("subject_type"), SubjectID: q.Get("subject_id"), Limit: limitParam(r)}))
	h.respond(w, r, v, err)
}

func (h *HTTP) openCase(w http.ResponseWriter, r *http.Request) {
	var in NewCase
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.OpenCase(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) caseView(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.CaseView(r.Context(), principal(r).UserID, r.PathValue("case_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) evidence(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.EvidenceRecord(r.Context(), principal(r).UserID, r.PathValue("case_id"), r.PathValue("evidence_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) addEvidence(w http.ResponseWriter, r *http.Request) {
	var in NoteInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p := principal(r)
	c, id, err := h.S.AddEvidence(r.Context(), CaseOp{StaffID: p.UserID, StepUpAt: p.StepUpAt, CaseID: r.PathValue("case_id")}, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, map[string]any{"case": c, "evidence_id": id})
}

func (h *HTTP) caseAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason         string `json:"reason"`
			Version        int    `json:"version"`
			AssigneeID     string `json:"assignee_id"`
			To             string `json:"to"`
			Outcome        string `json:"outcome"`
			SignalID       string `json:"signal_id"`
			Classification string `json:"classification"`
			Explanation    string `json:"explanation"`
		}
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(r, &in); err != nil {
				h.fail(w, r, err)
				return
			}
		}
		p := principal(r)
		op := CaseOp{StaffID: p.UserID, StepUpAt: p.StepUpAt, CaseID: r.PathValue("case_id"), ExpectedVersion: in.Version, Reason: in.Reason}
		var c Case
		var err error
		switch action {
		case "assign":
			c, err = h.S.Assign(r.Context(), op, in.AssigneeID)
		case "transition":
			c, err = h.S.Transition(r.Context(), op, in.To)
		case "review_signal":
			c, err = h.S.ReviewSignal(r.Context(), op, in.SignalID, in.Classification, in.Explanation)
		case "escalate":
			c, err = h.S.Escalate(r.Context(), op)
		case "propose":
			c, err = h.S.ProposeDecision(r.Context(), op, in.Outcome)
		case "approve":
			c, err = h.S.DecideCase(r.Context(), op, true)
		case "return":
			c, err = h.S.DecideCase(r.Context(), op, false)
		case "reopen":
			c, err = h.S.Reopen(r.Context(), op)
		}
		h.respond(w, r, c, err)
	}
}

// ---- holds -----------------------------------------------------------------------------------------------------------

func (h *HTTP) holds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := list(h.S.Holds(r.Context(), HoldFilter{Status: q.Get("status"), HoldType: q.Get("hold_type"), SubjectType: q.Get("subject_type"),
		SubjectID: q.Get("subject_id")}))
	h.respond(w, r, v, err)
}

func (h *HTTP) hold(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.Hold(r.Context(), r.PathValue("hold_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) createHold(w http.ResponseWriter, r *http.Request) {
	var in NewHold
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.CreateHold(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) holdAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, err := decodeAction(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		p := principal(r)
		op := HoldOp{StaffID: p.UserID, StepUpAt: p.StepUpAt, HoldID: r.PathValue("hold_id"), ExpectedVersion: in.Version, Reason: in.Reason}
		var v Hold
		switch action {
		case "activate":
			v, err = h.S.ActivateHold(r.Context(), op)
		case "revoke":
			v, err = h.S.RevokeHold(r.Context(), op)
		case "review":
			v, err = h.S.ReviewHold(r.Context(), op)
		case "request_release":
			v, err = h.S.RequestRelease(r.Context(), op)
		case "approve_release":
			v, err = h.S.DecideRelease(r.Context(), op, true)
		case "reject_release":
			v, err = h.S.DecideRelease(r.Context(), op, false)
		}
		h.respond(w, r, v, err)
	}
}

// ---- screening -------------------------------------------------------------------------------------------------------

func (h *HTTP) screenings(w http.ResponseWriter, r *http.Request) {
	v, err := list(h.S.Screenings(r.Context(), r.URL.Query().Get("status")))
	h.respond(w, r, v, err)
}

func (h *HTTP) capabilities(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"production_screening": "BLOCKED", "provider_selected": false, "providers": []ProviderCapabilities{}}
	if h.S.Screening != nil {
		out["providers"] = []ProviderCapabilities{h.S.Screening.GetProviderCapabilities()}
	}
	h.ok(w, r, http.StatusOK, out)
}

func (h *HTTP) screening(w http.ResponseWriter, r *http.Request) {
	x, err := h.S.Screening1(r.Context(), r.PathValue("request_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.S.ScreeningResults(r.Context(), x.ID)
	h.respond(w, r, map[string]any{"request": x, "results": res}, err)
}

func (h *HTTP) requestScreening(w http.ResponseWriter, r *http.Request) {
	var in NewScreening
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.S.RequestScreening(r.Context(), principal(r).UserID, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, v)
}

func (h *HTTP) reviewScreening(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Version  int    `json:"version"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p := principal(r)
	v, err := h.S.ReviewScreening(r.Context(), p.UserID, p.StepUpAt, r.PathValue("request_id"), in.Decision, in.Reason, in.Version)
	h.respond(w, r, v, err)
}

func (h *HTTP) retryScreening(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.RetryScreening(r.Context(), principal(r).UserID, r.PathValue("request_id"))
	h.respond(w, r, v, err)
}

func (h *HTTP) sandboxDeliver(w http.ResponseWriter, r *http.Request) {
	n, err := h.Sandbox.DeliverDue(r.Context(), 100)
	h.respond(w, r, map[string]any{"delivered": n, "label": "TEST_ONLY"}, err)
}

func (h *HTTP) sandboxPoll(w http.ResponseWriter, r *http.Request) {
	n, err := h.S.PollScreenings(r.Context(), 100)
	h.respond(w, r, map[string]any{"polled": n, "label": "TEST_ONLY"}, err)
}

// HandleScreeningWebhook is POST /api/v1/webhooks/screening/{provider} (public; signature verified; rate limited).
func (h *HTTP) HandleScreeningWebhook(w http.ResponseWriter, r *http.Request) {
	if h.S.Screening == nil || r.PathValue("provider") != h.S.Screening.GetProviderCapabilities().Code {
		h.fail(w, r, errs.New(errs.NotFound, errs.CodeRouteNotFound, "Not found."))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		h.fail(w, r, errs.New(errs.Invalid, errs.CodeMalformedRequest, "The request body could not be read."))
		return
	}
	if err := h.S.HandleScreeningCallback(r.Context(), r.Header.Get(ScreeningSignatureHeader), body); err != nil {
		if errors.Is(err, ErrNotFound) {
			err = errs.New(errs.NotFound, "SCREENING_REFERENCE_UNKNOWN", "Unknown screening reference.")
		}
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusOK, map[string]any{"received": true})
}
