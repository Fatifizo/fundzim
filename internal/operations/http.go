package operations

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Route is one operations route. Idempotent routes get the REQUIRED idempotency middleware. Every route is a staff
// permission route (404 for non-staff); permissions that require step-up are enforced by the route policy.
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
	Scope      string
}

// HTTP serves the operations API.
type HTTP struct{ S *Service }

// Routes lists the operations API (no unrestricted mutation route exists: every change is an operations record or is
// taken by the owning module's own route).
func (h *HTTP) Routes() []Route {
	perm := httpx.Permission
	const ops = "/api/v1/admin/operations"
	const sup = "/api/v1/admin/support/cases"
	const rev = "/api/v1/admin/access-reviews"
	dash := perm(PermDashboard)
	return []Route{
		{Pattern: "GET " + ops + "/overview", Policy: dash, Handler: h.overview},
		{Pattern: "GET " + ops + "/metrics", Policy: dash, Handler: h.metrics},
		{Pattern: "GET " + ops + "/tasks", Policy: dash, Handler: h.tasks},
		{Pattern: "GET " + ops + "/health", Policy: dash, Handler: h.health},
		{Pattern: "GET " + ops + "/alerts", Policy: dash, Handler: h.alerts},
		{Pattern: "GET " + ops + "/search", Policy: dash, Handler: h.search},
		{Pattern: "GET " + ops + "/exceptions", Policy: dash, Handler: h.exceptions},
		// reading triage needs ops.exception.work (checked by the service with the exception's own permission) but no step-up
		{Pattern: "GET " + ops + "/exceptions/{module}/{ref}/history", Policy: dash, Handler: h.exceptionHistory},
		{Pattern: "POST " + ops + "/exceptions/{module}/{ref}/triage", Policy: perm(PermExceptionWork), Handler: h.triage},
		{Pattern: "GET " + ops + "/incidents", Policy: perm(PermIncidentView), Handler: h.incidents},
		{Pattern: "POST " + ops + "/incidents", Policy: perm(PermIncidentManage), Handler: h.declareIncident, Idempotent: true, Scope: "ops.incidents.create"},
		{Pattern: "GET " + ops + "/incidents/{incident_id}", Policy: perm(PermIncidentView), Handler: h.incident},
		{Pattern: "POST " + ops + "/incidents/{incident_id}/update", Policy: perm(PermIncidentManage), Handler: h.updateIncident},
		{Pattern: "GET " + ops + "/sla-targets", Policy: dash, Handler: h.slaTargets},
		{Pattern: "POST " + ops + "/sla-targets", Policy: perm(PermSLARequest), Handler: h.proposeSLA, Idempotent: true, Scope: "ops.sla_targets.create"},
		{Pattern: "POST " + ops + "/sla-targets/{target_id}/approve", Policy: perm(PermSLAApprove), Handler: h.decideSLA(true)},
		{Pattern: "POST " + ops + "/sla-targets/{target_id}/reject", Policy: perm(PermSLAApprove), Handler: h.decideSLA(false)},

		// Stage 18 security alerts (docs/stage-18/security-monitoring.md); manage requires step-up (permission flag)
		{Pattern: "GET /api/v1/admin/security/alerts", Policy: perm(PermSecurityAlertView), Handler: h.securityAlerts},
		{Pattern: "GET /api/v1/admin/security/alerts/{alert_id}", Policy: perm(PermSecurityAlertView), Handler: h.securityAlert},
		{Pattern: "POST /api/v1/admin/security/alerts/{alert_id}/acknowledge", Policy: perm(PermSecurityAlertManage), Handler: h.alertAction("acknowledge")},
		{Pattern: "POST /api/v1/admin/security/alerts/{alert_id}/escalate", Policy: perm(PermSecurityAlertManage), Handler: h.alertAction("escalate")},
		{Pattern: "POST /api/v1/admin/security/alerts/{alert_id}/resolve", Policy: perm(PermSecurityAlertManage), Handler: h.alertAction("resolve")},
		{Pattern: "GET /api/v1/admin/security/backups", Policy: perm(PermSecurityAlertView), Handler: h.backupEvidence},

		{Pattern: "GET /api/v1/admin/approvals", Policy: dash, Handler: h.approvals},
		{Pattern: "GET /api/v1/admin/reports", Policy: dash, Handler: h.reports},

		{Pattern: "GET " + sup, Policy: perm(PermSupportView), Handler: h.supportCases},
		{Pattern: "POST " + sup, Policy: perm(PermSupportWork), Handler: h.openSupport, Idempotent: true, Scope: "ops.support_cases.create"},
		{Pattern: "GET " + sup + "/{case_id}", Policy: perm(PermSupportView), Handler: h.supportCase},
		{Pattern: "POST " + sup + "/{case_id}/assign", Policy: perm(PermSupportWork), Handler: h.supportAction("assign")},
		{Pattern: "POST " + sup + "/{case_id}/transition", Policy: perm(PermSupportWork), Handler: h.supportAction("transition")},
		{Pattern: "POST " + sup + "/{case_id}/escalate", Policy: perm(PermSupportWork), Handler: h.supportAction("escalate")},
		{Pattern: "POST " + sup + "/{case_id}/notes", Policy: perm(PermSupportWork), Handler: h.supportAction("note")},

		{Pattern: "GET " + rev, Policy: perm(PermStaffView), Handler: h.accessReviews},
		{Pattern: "POST " + rev, Policy: perm(PermReviewRun), Handler: h.startReview, Idempotent: true, Scope: "ops.access_reviews.create"},
		{Pattern: "GET " + rev + "/{review_id}", Policy: perm(PermStaffView), Handler: h.accessReview},
		{Pattern: "POST " + rev + "/{review_id}/items/{item_id}/decide", Policy: perm(PermReviewCertify), Handler: h.decideItem},
		{Pattern: "POST " + rev + "/{review_id}/complete", Policy: perm(PermReviewRun), Handler: h.completeReview},

		{Pattern: "GET /api/v1/admin/audit/events", Policy: perm(PermAuditRead), Handler: h.auditEvents(false)},
		{Pattern: "GET /api/v1/admin/audit/security-events", Policy: perm(PermSecurityAudit), Handler: h.auditEvents(true)},
		{Pattern: "POST /api/v1/admin/audit/exports", Policy: perm(PermAuditExport), Handler: h.auditExport},
	}
}

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteError(w, r, h.S.Logger, err)
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

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func (h *HTTP) viewer(r *http.Request) (Viewer, error) {
	return h.S.ViewerFor(r.Context(), principal(r).UserID)
}

func timeParam(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, validation(name, "INVALID_FORMAT")
	}
	t = t.UTC()
	return &t, nil
}

func decode(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return httpx.DecodeJSON(r, v)
}

// ---- command center ----------------------------------------------------------------------------------------------------

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	o, err := h.S.Overview(r.Context(), v)
	h.respond(w, r, o, err)
}

func (h *HTTP) metrics(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	m, st, err := h.S.Metrics(r.Context(), v)
	h.respond(w, r, map[string]any{"items": m, "sources": st, "next_cursor": nil}, err)
}

func (h *HTTP) tasks(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c := h.S.collect(r.Context())
	t, err := h.S.tasks(r.Context(), v, c)
	h.respond(w, r, t, err)
}

func (h *HTTP) health(w http.ResponseWriter, r *http.Request) {
	rep, err := h.S.Health(r.Context())
	h.respond(w, r, rep, err)
}

func (h *HTTP) alerts(w http.ResponseWriter, r *http.Request) {
	a, err := h.S.Alerts(r.Context())
	h.respond(w, r, map[string]any{"items": a, "next_cursor": nil}, err)
}

func (h *HTTP) search(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.S.Search(r.Context(), v, r.URL.Query().Get("q"))
	h.respond(w, r, res, err)
}

func (h *HTTP) approvals(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	items, st, err := h.S.Approvals(r.Context(), v, ApprovalFilter{Module: q.Get("module"), Action: q.Get("action"), MineOnly: q.Get("decidable") == "true"})
	h.respond(w, r, map[string]any{"items": items, "sources": st, "next_cursor": nil}, err)
}

func (h *HTTP) reports(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	from, err := timeParam(r, "from")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	to, err := timeParam(r, "to")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep, err := h.S.Reports(r.Context(), v, r.URL.Query().Get("key"), from, to)
	h.respond(w, r, map[string]any{"items": rep, "next_cursor": nil}, err)
}

// ---- exceptions ---------------------------------------------------------------------------------------------------------

func (h *HTTP) exceptions(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	items, st, err := h.S.Exceptions(r.Context(), v, ExceptionFilter{Module: q.Get("module"), Severity: q.Get("severity"), Status: q.Get("status"),
		AssignedToMe: q.Get("assigned_to") == "me"})
	h.respond(w, r, map[string]any{"items": items, "sources": st, "next_cursor": nil}, err)
}

func (h *HTTP) exceptionHistory(w http.ResponseWriter, r *http.Request) {
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hist, err := h.S.ExceptionHistory(r.Context(), v, r.PathValue("module"), r.PathValue("ref"))
	h.respond(w, r, map[string]any{"items": hist, "next_cursor": nil}, err)
}

func (h *HTTP) triage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To         string `json:"to"`
		AssigneeID string `json:"assignee_id"`
		Note       string `json:"note"`
		Version    int    `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.Triage(r.Context(), v, TriageOp{StaffID: v.StaffID, Module: r.PathValue("module"), Ref: r.PathValue("ref"), To: in.To,
		AssigneeID: in.AssigneeID, Note: in.Note, ExpectedVersion: in.Version})
	h.respond(w, r, x, err)
}

// ---- incidents ----------------------------------------------------------------------------------------------------------

func (h *HTTP) incidents(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.Incidents(r.Context(), r.URL.Query().Get("status"))
	h.respond(w, r, map[string]any{"items": items, "next_cursor": nil}, err)
}

func (h *HTTP) declareIncident(w http.ResponseWriter, r *http.Request) {
	var in NewIncident
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.DeclareIncident(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, x)
}

func (h *HTTP) incident(w http.ResponseWriter, r *http.Request) {
	x, err := h.S.Incident(r.Context(), r.PathValue("incident_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	tl, err := h.S.IncidentTimeline(r.Context(), x.ID)
	h.respond(w, r, map[string]any{"incident": x, "timeline": tl}, err)
}

func (h *HTTP) updateIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To       string `json:"to"`
		OwnerID  string `json:"owner_id"`
		Note     string `json:"note"`
		Severity string `json:"severity"`
		Version  int    `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.UpdateIncident(r.Context(), IncidentOp{StaffID: principal(r).UserID, IncidentID: r.PathValue("incident_id"), ExpectedVersion: in.Version,
		To: in.To, OwnerID: in.OwnerID, Note: in.Note, Severity: in.Severity})
	h.respond(w, r, x, err)
}

// ---- security alerts (Stage 18) ----------------------------------------------------------------------------------------

func (h *HTTP) securityAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.S.SecurityAlerts(r.Context(), q.Get("status"), q.Get("min_severity"))
	h.respond(w, r, map[string]any{"items": items, "next_cursor": nil,
		"notice": "Alerts are raised by fixed rules over module snapshots; thresholds are engineering defaults, not service levels. No external notification is sent."}, err)
}

func (h *HTTP) securityAlert(w http.ResponseWriter, r *http.Request) {
	a, hist, err := h.S.SecurityAlert(r.Context(), r.PathValue("alert_id"))
	h.respond(w, r, map[string]any{"alert": a, "history": hist}, err)
}

func (h *HTTP) alertAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note    string `json:"note"`
			Version int    `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		op := AlertOp{StaffID: principal(r).UserID, ID: r.PathValue("alert_id"), Version: in.Version, Note: in.Note}
		var a SecurityAlert
		var err error
		switch action {
		case "acknowledge":
			a, err = h.S.AcknowledgeAlert(r.Context(), op)
		case "escalate":
			a, err = h.S.EscalateAlert(r.Context(), op)
		default:
			a, err = h.S.ResolveAlert(r.Context(), op)
		}
		h.respond(w, r, a, err)
	}
}

func (h *HTTP) backupEvidence(w http.ResponseWriter, r *http.Request) {
	b, err := h.S.BackupEvidenceSummary(r.Context())
	h.respond(w, r, b, err)
}

// ---- SLA targets --------------------------------------------------------------------------------------------------------

func (h *HTTP) slaTargets(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.SLATargets(r.Context())
	h.respond(w, r, map[string]any{"items": items, "metrics": SLAMetrics, "notice": SLANotice, "next_cursor": nil}, err)
}

func (h *HTTP) proposeSLA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MetricKey     string `json:"metric_key"`
		TargetSeconds int    `json:"target_seconds"`
		Reason        string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.S.ProposeSLATarget(r.Context(), principal(r).UserID, in.MetricKey, in.TargetSeconds, in.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, t)
}

func (h *HTTP) decideSLA(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := decode(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		v, err := h.viewer(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		t, err := h.S.DecideSLATarget(r.Context(), v, r.PathValue("target_id"), approve, in.Reason)
		h.respond(w, r, t, err)
	}
}

// ---- support ------------------------------------------------------------------------------------------------------------

func (h *HTTP) supportCases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	assigned := q.Get("assigned_to")
	if assigned == "me" {
		assigned = principal(r).UserID
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := h.S.SupportCases(r.Context(), SupportFilter{Status: q.Get("status"), Category: q.Get("category"), Priority: q.Get("priority"),
		AssignedTo: assigned, Query: q.Get("q"), Limit: limit, Cursor: q.Get("cursor")})
	var nc any
	if next != "" {
		nc = next
	}
	h.respond(w, r, map[string]any{"items": items, "next_cursor": nc}, err)
}

func (h *HTTP) openSupport(w http.ResponseWriter, r *http.Request) {
	var in NewSupportCase
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.S.OpenSupportCase(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, c)
}

func (h *HTTP) supportCase(w http.ResponseWriter, r *http.Request) {
	c, err := h.S.SupportCase(r.Context(), r.PathValue("case_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hist, err := h.S.SupportHistory(r.Context(), c.ID)
	h.respond(w, r, map[string]any{"case": c, "history": hist}, err)
}

func (h *HTTP) supportAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AssigneeID  string `json:"assignee_id"`
			To          string `json:"to"`
			EscalatedTo string `json:"escalated_to"`
			Reference   string `json:"escalation_reference"`
			Note        string `json:"note"`
			Version     int    `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		op := SupportOp{StaffID: principal(r).UserID, CaseID: r.PathValue("case_id"), ExpectedVersion: in.Version, Note: in.Note}
		var c SupportCase
		var err error
		switch action {
		case "assign":
			c, err = h.S.AssignSupportCase(r.Context(), op, in.AssigneeID)
		case "transition":
			c, err = h.S.TransitionSupportCase(r.Context(), op, in.To)
		case "escalate":
			c, err = h.S.EscalateSupportCase(r.Context(), op, in.EscalatedTo, in.Reference)
		case "note":
			c, err = h.S.AddSupportNote(r.Context(), op)
		}
		h.respond(w, r, c, err)
	}
}

// ---- access reviews -----------------------------------------------------------------------------------------------------

func (h *HTTP) accessReviews(w http.ResponseWriter, r *http.Request) {
	items, err := h.S.AccessReviews(r.Context())
	h.respond(w, r, map[string]any{"items": items, "next_cursor": nil}, err)
}

func (h *HTTP) startReview(w http.ResponseWriter, r *http.Request) {
	var in NewAccessReview
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.StartAccessReview(r.Context(), principal(r).UserID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.ok(w, r, http.StatusCreated, x)
}

func (h *HTTP) accessReview(w http.ResponseWriter, r *http.Request) {
	x, err := h.S.AccessReview(r.Context(), r.PathValue("review_id"))
	h.respond(w, r, x, err)
}

func (h *HTTP) decideItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.viewer(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.DecideReviewItem(r.Context(), v, r.PathValue("review_id"), r.PathValue("item_id"), in.Decision, in.Reason)
	h.respond(w, r, x, err)
}

func (h *HTTP) completeReview(w http.ResponseWriter, r *http.Request) {
	x, err := h.S.CompleteAccessReview(r.Context(), principal(r).UserID, r.PathValue("review_id"))
	h.respond(w, r, x, err)
}

// ---- audit center -------------------------------------------------------------------------------------------------------

func auditFilter(r *http.Request) (audit.SearchFilter, error) {
	q := r.URL.Query()
	f := audit.SearchFilter{Action: q.Get("action"), TargetType: q.Get("target_type"), CorrelationID: q.Get("correlation_id"), RequestID: q.Get("request_id"),
		Outcome: q.Get("outcome")}
	for name, dst := range map[string]*string{"actor_id": &f.ActorID, "target_id": &f.TargetID} {
		if v := q.Get(name); v != "" {
			if !ids.Valid(v) {
				return f, validation(name, "INVALID_FORMAT")
			}
			*dst = v
		}
	}
	var err error
	if f.From, err = timeParam(r, "from"); err != nil {
		return f, err
	}
	if f.To, err = timeParam(r, "to"); err != nil {
		return f, err
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if c := q.Get("cursor"); c != "" {
		if f.BeforeSeq, err = strconv.ParseInt(c, 10, 64); err != nil || f.BeforeSeq <= 0 {
			return f, validation("cursor", "INVALID_FORMAT")
		}
	}
	return f, nil
}

func (h *HTTP) auditEvents(security bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := auditFilter(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v, err := h.viewer(r)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		f.Security, f.WithNetwork = security, v.Can(PermSecurityAudit)
		items, next, err := audit.Search(r.Context(), h.S.Pool, f)
		if errors.Is(err, audit.ErrBadFilter) {
			err = validation("from", "INVALID_RANGE")
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		// reading the audit log is itself recorded (security stream)
		if err := h.S.tx(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "audit.queried", TargetType: "audit_log",
				Metadata: map[string]any{"security_stream": security, "results": len(items)}})
		}); err != nil {
			h.fail(w, r, err)
			return
		}
		var nc any
		if next != "" {
			nc = next
		}
		h.ok(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nc})
	}
}

// MaxExportRows bounds an export; MaxExportRange bounds its time range.
const (
	MaxExportRows  = 5000
	MaxExportRange = 31 * 24 * time.Hour
)

// auditExport is POST /api/v1/admin/audit/exports {format, from, to, action?, actor_id?, target_type?, correlation_id?}
// (audit.export + step-up): a bounded CSV or JSON export of the platform audit stream, without IP or user agent. The
// export is recorded in the security audit stream with its filter and row count before the file is returned.
func (h *HTTP) auditExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format        string    `json:"format"`
		From          time.Time `json:"from"`
		To            time.Time `json:"to"`
		Action        string    `json:"action"`
		ActorID       string    `json:"actor_id"`
		TargetType    string    `json:"target_type"`
		CorrelationID string    `json:"correlation_id"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if in.Format != "csv" && in.Format != "json" {
		h.fail(w, r, validation("format", "INVALID_VALUE"))
		return
	}
	if in.From.IsZero() || in.To.IsZero() || !in.To.After(in.From) || in.To.Sub(in.From) > MaxExportRange {
		h.fail(w, r, validation("from", "INVALID_RANGE"))
		return
	}
	if in.ActorID != "" && !ids.Valid(in.ActorID) {
		h.fail(w, r, validation("actor_id", "INVALID_FORMAT"))
		return
	}
	from, to := in.From.UTC(), in.To.UTC()
	f := audit.SearchFilter{From: &from, To: &to, Action: in.Action, ActorID: in.ActorID, TargetType: in.TargetType, CorrelationID: in.CorrelationID, Limit: 200}
	var all []audit.RecordView
	for len(all) <= MaxExportRows {
		page, next, err := audit.Search(r.Context(), h.S.Pool, f)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		f.BeforeSeq, _ = strconv.ParseInt(next, 10, 64)
	}
	if len(all) > MaxExportRows {
		h.fail(w, r, errs.New(errs.Unprocessable, "AUDIT_EXPORT_TOO_LARGE", "The export would exceed 5,000 events. Narrow the filters or the date range."))
		return
	}
	if err := h.S.tx(r.Context(), func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "audit.exported", TargetType: "audit_log",
			Metadata: map[string]any{"format": in.Format, "from": from, "to": to, "action": in.Action, "actor_id": in.ActorID, "target_type": in.TargetType,
				"correlation_id": in.CorrelationID, "rows": len(all)}})
	}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := "fundzim-audit-" + from.Format("20060102") + "-" + to.Format("20060102")
	if in.Format == "json" {
		for i := range all {
			all[i].IP, all[i].UserAgent = nil, nil
		}
		b, _ := json.Marshal(map[string]any{"environment": "TEST", "from": from, "to": to, "events": all})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		_, _ = w.Write(b)
		return
	}
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write(audit.ExportHeader)
	for _, e := range all {
		row := audit.ExportRow(e)
		for i, c := range row { // neutralise spreadsheet formulas
			if len(c) > 0 && (c[0] == '=' || c[0] == '+' || c[0] == '-' || c[0] == '@') {
				row[i] = "'" + c
			}
		}
		_ = cw.Write(row)
	}
	cw.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}
