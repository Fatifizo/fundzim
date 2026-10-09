package compliance

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Route is one HTTP route for the composition root: register each with
// r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler) (plus idempotency options where noted).
type Route struct {
	Pattern string
	Policy  httpx.Policy
	Handler http.HandlerFunc
}

// Routes lists the staff compliance API (contract §7.4). Every route is a staff permission route (404 for
// non-staff); resolve, approve-resolution, close and reopen additionally require a fresh step-up, checked
// in the handler because case.manage itself is not a step-up permission.
func (s *Service) Routes() []Route {
	view, manage, create := httpx.Permission(PermView), httpx.Permission(PermManage), httpx.Permission(PermCreate)
	const base = "/api/v1/admin/compliance/cases"
	return []Route{
		{"GET " + base, view, s.HandleList},
		{"POST " + base, create, s.HandleOpen},
		{"GET " + base + "/{case_id}", view, s.HandleGet},
		{"POST " + base + "/{case_id}/assign", manage, s.HandleAssign},
		{"POST " + base + "/{case_id}/start", manage, s.HandleStart},
		{"POST " + base + "/{case_id}/request-info", manage, s.HandleRequestInfo},
		{"POST " + base + "/{case_id}/escalate", manage, s.HandleEscalate},
		{"POST " + base + "/{case_id}/notes", manage, s.HandleAddNote},
		{"POST " + base + "/{case_id}/resolve", manage, s.HandleResolve},
		{"POST " + base + "/{case_id}/approve-resolution", manage, s.HandleApproveResolution},
		{"POST " + base + "/{case_id}/close", manage, s.HandleClose},
		{"POST " + base + "/{case_id}/reopen", manage, s.HandleReopen},
	}
}

// staffOp builds an Op for the authenticated staff principal. The router has already enforced the route
// permission; STR access additionally needs str.prepare with a fresh step-up.
func (s *Service) staffOp(r *http.Request) (Op, error) {
	p := authz.PrincipalFrom(r.Context())
	if p == nil || p.Kind != authz.KindStaff {
		return Op{}, errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	}
	op := Op{Actor: Staff(p.UserID), CaseID: r.PathValue("case_id"),
		STRAccess: p.Has(PermSTR) && p.StepUpFresh(s.now(), s.stepUpAge)}
	if v := strings.Trim(r.Header.Get("If-Match"), `"`); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Op{}, httpx.Validation(errs.Detail{Field: "If-Match", Code: "INVALID_VERSION"})
		}
		op.IfVersion = n
	}
	if op.CaseID != "" && !ids.Valid(op.CaseID) {
		return Op{}, errNotFound()
	}
	return op, nil
}

func (s *Service) stepUp(r *http.Request) error {
	if !authz.PrincipalFrom(r.Context()).StepUpFresh(s.now(), s.stepUpAge) {
		return errStepUp()
	}
	return nil
}

// decodeOptional decodes a JSON body when one is sent (empty bodies are allowed for simple actions).
func decodeOptional(r *http.Request, v any) error {
	if r.ContentLength == 0 && r.Header.Get("Content-Type") == "" {
		return nil
	}
	return httpx.DecodeJSON(r, v)
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, s.logger, err)
}

// ---- reads -----------------------------------------------------------------------------------------------

// HandleList is GET /admin/compliance/cases?status=&severity=&assigned=me|unassigned|any&limit=&cursor=.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	f := ListFilter{Status: q.Get("status"), Severity: q.Get("severity"), Assigned: q.Get("assigned"), Me: op.Actor.ID, Cursor: q.Get("cursor")}
	var details []errs.Detail
	if f.Status != "" && len(Transitions[f.Status]) == 0 {
		details = append(details, errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	if _, ok := Severities[f.Severity]; f.Severity != "" && !ok {
		details = append(details, errs.Detail{Field: "severity", Code: "INVALID_VALUE"})
	}
	if f.Assigned != "" && f.Assigned != "me" && f.Assigned != "unassigned" && f.Assigned != "any" {
		details = append(details, errs.Detail{Field: "assigned", Code: "INVALID_VALUE"})
	}
	if f.Cursor != "" && !ids.Valid(f.Cursor) {
		details = append(details, errs.Detail{Field: "cursor", Code: "INVALID_VALUE"})
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			details = append(details, errs.Detail{Field: "limit", Code: "INVALID_VALUE"})
		}
		f.Limit = n
	}
	if len(details) > 0 {
		s.fail(w, r, httpx.Validation(details...))
		return
	}
	cases, next, err := s.List(r.Context(), f, op.STRAccess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"cases": cases, "next_cursor": next})
}

// HandleGet is GET /admin/compliance/cases/{case_id}: the case, its timeline and its (decrypted) notes.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.Get(r.Context(), op.Actor, op.CaseID, op.STRAccess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, d)
}

// ---- writes ----------------------------------------------------------------------------------------------

type openReq struct {
	CaseType        string      `json:"case_type"`
	Severity        string      `json:"severity"`
	ReasonCode      string      `json:"reason_code"`
	Confidentiality string      `json:"confidentiality"`
	Subjects        []LinkInput `json:"subjects"`
	Note            string      `json:"note"`
}

// HandleOpen is POST /admin/compliance/cases (manual open, case.create).
func (s *Service) HandleOpen(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req openReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Open(r.Context(), op.Actor, OpenInput{CaseType: req.CaseType, Severity: req.Severity, OpeningReasonCode: req.ReasonCode,
		Confidentiality: req.Confidentiality, Links: req.Subjects, Note: req.Note}, op.STRAccess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, c)
}

type assignReq struct {
	AssigneeID string `json:"assignee_id"`
}

// HandleAssign is POST …/{case_id}/assign {assignee_id?} (default: the caller).
func (s *Service) HandleAssign(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req assignReq
	if err := decodeOptional(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.AssigneeID = req.AssigneeID
	s.respond(w, r, http.StatusOK)(s.Assign(r.Context(), op))
}

// HandleStart is POST …/{case_id}/start.
func (s *Service) HandleStart(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct{}
	if err := decodeOptional(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	s.respond(w, r, http.StatusOK)(s.Start(r.Context(), op))
}

type reasonReq struct {
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
}

// HandleRequestInfo is POST …/{case_id}/request-info {reason_code, note?}.
func (s *Service) HandleRequestInfo(w http.ResponseWriter, r *http.Request) {
	s.reasonAction(w, r, false, s.RequestInformation)
}

type escalateReq struct {
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
	Severity   string `json:"severity"`
}

// HandleEscalate is POST …/{case_id}/escalate {reason_code, note?, severity?}.
func (s *Service) HandleEscalate(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req escalateReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.ReasonCode, op.Note, op.Severity = req.ReasonCode, req.Note, req.Severity
	s.respond(w, r, http.StatusOK)(s.Escalate(r.Context(), op))
}

type noteReq struct {
	Body string `json:"body"`
}

// HandleAddNote is POST …/{case_id}/notes {body} → 201 Note.
func (s *Service) HandleAddNote(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req noteReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.Note = req.Body
	n, err := s.AddNote(r.Context(), op)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusCreated, n)
}

type resolveReq struct {
	Decision   string `json:"decision"`
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
}

// HandleResolve is POST …/{case_id}/resolve {decision, reason_code, note} (step-up). 200 when the resolution
// takes effect; 202 with resolution.status PROPOSED when it awaits a second approval.
func (s *Service) HandleResolve(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err == nil {
		err = s.stepUp(r)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req resolveReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.Decision, op.ReasonCode, op.Note = req.Decision, req.ReasonCode, req.Note
	c, err := s.Resolve(r.Context(), op)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if c.Resolution != nil && c.Resolution.Status == ResolutionProposed {
		status = http.StatusAccepted
	}
	httpx.WriteData(w, r, status, c)
}

type approveReq struct {
	Note string `json:"note"`
}

// HandleApproveResolution is POST …/{case_id}/approve-resolution {note?} (step-up; a different person).
func (s *Service) HandleApproveResolution(w http.ResponseWriter, r *http.Request) {
	op, err := s.staffOp(r)
	if err == nil {
		err = s.stepUp(r)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req approveReq
	if err := decodeOptional(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.Note = req.Note
	s.respond(w, r, http.StatusOK)(s.ApproveResolution(r.Context(), op))
}

// HandleClose is POST …/{case_id}/close {reason_code, note?} (step-up).
func (s *Service) HandleClose(w http.ResponseWriter, r *http.Request) {
	s.reasonAction(w, r, true, s.Close)
}

// HandleReopen is POST …/{case_id}/reopen {reason_code, note} (step-up).
func (s *Service) HandleReopen(w http.ResponseWriter, r *http.Request) {
	s.reasonAction(w, r, true, s.Reopen)
}

func (s *Service) reasonAction(w http.ResponseWriter, r *http.Request, stepUp bool, fn func(ctx context.Context, op Op) (Case, error)) {
	op, err := s.staffOp(r)
	if err == nil && stepUp {
		err = s.stepUp(r)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req reasonReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	op.ReasonCode, op.Note = req.ReasonCode, req.Note
	s.respond(w, r, http.StatusOK)(fn(r.Context(), op))
}

func (s *Service) respond(w http.ResponseWriter, r *http.Request, status int) func(Case, error) {
	return func(c Case, err error) {
		if err != nil {
			s.fail(w, r, err)
			return
		}
		httpx.WriteData(w, r, status, c)
	}
}
