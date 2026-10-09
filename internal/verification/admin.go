package verification

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Case types.
const (
	TypeKYC         = "KYC"
	TypeKYB         = "KYB"
	TypeBeneficiary = "BENEFICIARY"
	TypeDestination = "PAYOUT_DESTINATION"
)

// decisionPermission per type (all require step-up: app.permissions.requires_step_up).
var decisionPermission = map[string]string{TypeKYC: "kyc.decision.record", TypeKYB: "org.verification.decide",
	TypeBeneficiary: "beneficiary.verification.decide", TypeDestination: "payout_destination.verify"}

var errCaseNotFound = errs.New(errs.NotFound, "CASE_NOT_FOUND", "No such verification case.")

// resolveType finds which module owns a case id.
func (s *Service) resolveType(ctx context.Context, id string) (string, error) {
	kind, err := s.KYC.CaseKind(ctx, id)
	if err != nil {
		return "", err
	}
	if kind != "" {
		return kind, nil
	}
	if ok, err := s.Benefs.Exists(ctx, id); err != nil {
		return "", err
	} else if ok {
		return TypeBeneficiary, nil
	}
	if ok, err := s.Payouts.Exists(ctx, id); err != nil {
		return "", err
	} else if ok {
		return TypeDestination, nil
	}
	return "", errCaseNotFound
}

func (s *Service) requireDecision(p *authz.Principal, typ string) error {
	if !p.Has(decisionPermission[typ]) {
		return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission to decide this type of verification.")
	}
	if !p.StepUpFresh(s.Clock.Now(), s.StepUpMaxAge) {
		return errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	}
	return nil
}

// Queue: GET /admin/verification/cases?type=&status=&assigned=&limit=&cursor=
func (s *Service) Queue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	q := r.URL.Query()
	f := kyc.QueueFilter{Assigned: q.Get("assigned"), ActorID: p.UserID}
	if f.Assigned != "" && f.Assigned != "me" && f.Assigned != "unassigned" && f.Assigned != "any" {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "assigned", Code: "INVALID_VALUE"}))
		return
	}
	if st := q.Get("status"); st != "" {
		f.Statuses = strings.Split(st, ",")
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil {
		f.Limit = l
	}
	if c := q.Get("cursor"); c != "" {
		// keyset cursor "<submitted_at RFC3339Nano>|<id>": ties on the timestamp are broken by id, so no item is
		// skipped or repeated at a page boundary
		ts, cid, _ := strings.Cut(c, "|")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || (cid != "" && !ids.Valid(cid)) {
			s.fail(w, r, httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"}))
			return
		}
		f.Before, f.AfterID = &t, cid
	}
	types := []string{TypeKYC, TypeKYB, TypeBeneficiary, TypeDestination}
	if t := q.Get("type"); t != "" {
		if decisionPermission[t] == "" {
			s.fail(w, r, httpx.Validation(errs.Detail{Field: "type", Code: "INVALID_VALUE"}))
			return
		}
		types = []string{t}
	}
	var all []kyc.QueueItem
	for _, t := range types {
		var items []kyc.QueueItem
		var err error
		ff := f
		if t == TypeDestination && len(ff.Statuses) > 0 {
			ff.Statuses = nil // destinations use their own status vocabulary
			if strings.Contains(q.Get("status"), "PENDING_VERIFICATION") || q.Get("type") == TypeDestination {
				ff.Statuses = f.Statuses
			}
		}
		switch t {
		case TypeKYC, TypeKYB:
			items, err = s.KYC.Queue(ctx, t, ff)
		case TypeBeneficiary:
			items, err = s.Benefs.Queue(ctx, ff)
		case TypeDestination:
			items, err = s.Payouts.Queue(ctx, ff)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		all = append(all, items...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i].SubmittedAt, all[j].SubmittedAt
		switch {
		case a == nil || b == nil:
			return a != nil
		case !a.Equal(*b):
			return a.Before(*b)
		}
		return all[i].ID < all[j].ID // same order as the SQL (uuid order == lowercase hex order)
	})
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	meta := map[string]any{}
	if len(all) > limit {
		all = all[:limit]
	}
	if len(all) == limit && all[len(all)-1].SubmittedAt != nil {
		last := all[len(all)-1]
		meta["next_cursor"] = last.SubmittedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": all, "next_cursor": meta["next_cursor"]})
}

// subjectSummary is a masked description of the subject for reviewers (no C3 beyond what the case view shows).
func (s *Service) subjectSummary(ctx context.Context, subjectType, subjectID string) map[string]any {
	out := map[string]any{"type": subjectType, "id": subjectID}
	switch subjectType {
	case "USER":
		if a, err := users.ByID(ctx, s.Orgs.Pool, subjectID); err == nil {
			out["display_name"], out["email_masked"], out["email_verified"] = a.DisplayName, maskEmail(a.Email), a.EmailVerified
			out["account_created_at"] = a.CreatedAt
		}
	case "ORGANISATION":
		if name, typ, status, err := s.Orgs.Summary(ctx, subjectID); err == nil {
			out["display_name"], out["org_type"], out["status"] = name, typ, status
		}
	}
	return out
}

func maskEmail(e string) string {
	at := strings.LastIndexByte(e, '@')
	if at < 1 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

// CaseDetail: GET /admin/verification/cases/{case_id}
func (s *Service) CaseDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	id := r.PathValue("case_id")
	typ, err := s.resolveType(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"id": id, "type": typ}
	var subjType, subjID, noteCol string
	switch typ {
	case TypeKYC:
		v, err := s.KYC.CaseView(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["case"] = v
		subjType, subjID, _ = s.KYC.ReviewSubject(ctx, typ, id)
		noteCol = "kyc_case_id"
		rs, err := s.KYC.ReviewStateOf(ctx, typ, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["review"] = rs
	case TypeKYB:
		v, err := s.KYC.KYBCaseView(ctx, id, true)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["case"] = v
		subjType, subjID, _ = s.KYC.ReviewSubject(ctx, typ, id)
		noteCol = "kyb_case_id"
		rs, err := s.KYC.ReviewStateOf(ctx, typ, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["review"] = rs
	case TypeBeneficiary:
		b, err := s.Benefs.View(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["case"] = b
		subjType, subjID = b.Owner.Type, b.Owner.ID
		out["status"], out["assigned_to"] = b.Status(), b.AssignedTo()
		noteCol = "beneficiary_id"
	case TypeDestination:
		d, err := s.Payouts.View(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["case"] = d
		subjType, subjID = d.Owner.Type, d.Owner.ID
		out["status"], out["assigned_to"] = d.Status, d.AssignedTo()
		docs, err := s.KYC.DocumentsFor(ctx, "destination_id", id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["documents"] = docs
		checks, err := s.Payouts.ChecksOf(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["checks"] = checks
		noteCol = "destination_id"
	}
	if typ == TypeKYC || typ == TypeKYB {
		checks, err := s.KYC.Checks(ctx, typ, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["checks"] = checks
		a, err := s.KYC.ReviewAssignment(ctx, typ, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["assigned_to"] = a
	}
	out["subject"] = s.subjectSummary(ctx, subjType, subjID)
	var hist []kyc.HistoryItem
	switch typ {
	case TypeKYC, TypeKYB:
		hist, err = s.KYC.History(ctx, typ, id)
	case TypeBeneficiary:
		hist, err = s.Benefs.History(ctx, id)
	case TypeDestination:
		hist, err = s.Payouts.History(ctx, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out["history"] = hist
	notes, err := s.KYC.Notes(ctx, noteCol, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out["notes"] = notes
	if s.Risk != nil && p.Has("risk.view") && (subjType == "USER" || subjType == "ORGANISATION") {
		if rs, err := s.Risk.Summary(ctx, subjType, subjID); err == nil {
			out["risk"] = rs
		}
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, out)
}

type actionBody struct {
	AssigneeID  string   `json:"assignee_id"`
	ReasonCode  string   `json:"reason_code"`
	Note        string   `json:"note"`
	UserMessage string   `json:"user_message"`
	Message     string   `json:"message"`
	Items       []string `json:"items"`
	NameMatch   *bool    `json:"name_match"`
}

// Action: POST /admin/verification/cases/{case_id}/{action}
func (s *Service) Action(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		p := principal(r)
		var body actionBody
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(r, &body); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		id := r.PathValue("case_id")
		typ, err := s.resolveType(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		decisive := map[string]verifcase.Action{"approve": verifcase.ActApprove, "reject": verifcase.ActReject, "suspend": verifcase.ActSuspend,
			"reinstate": verifcase.ActReinstate, "reopen": verifcase.ActReopen, "revoke": verifcase.ActRevoke, "return": verifcase.ActReturn,
			"escalate": verifcase.ActEscalate}
		if act, ok := decisive[action]; ok && act != verifcase.ActEscalate && act != verifcase.ActReturn || action == "second-approval" {
			if err := s.requireDecision(p, typ); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		// a case may only be assigned to a staff member who can review cases (never to a subject or support)
		if action == "assign" && body.AssigneeID != "" && body.AssigneeID != p.UserID {
			ok := false
			if s.StaffCan != nil {
				if ok, err = s.StaffCan(ctx, body.AssigneeID, "kyc.case.review"); err != nil {
					s.fail(w, r, err)
					return
				}
			}
			if !ok {
				s.fail(w, r, errs.New(errs.Unprocessable, "ASSIGNEE_NOT_ELIGIBLE", "That person cannot review verification cases."))
				return
			}
		}
		in := kyc.DecisionInput{ReasonCode: body.ReasonCode, Note: body.Note, UserMessage: body.UserMessage}
		var out any = map[string]string{"status": "ok"}
		status := http.StatusOK
		switch typ {
		case TypeKYC, TypeKYB:
			switch action {
			case "assign":
				err = s.KYC.Assign(ctx, typ, id, p.UserID, body.AssigneeID)
			case "start-review":
				err = s.KYC.StartReview(ctx, typ, id, p.UserID)
			case "request-info":
				err = s.KYC.RequestInformation(ctx, typ, id, p.UserID, body.Message, body.Items)
			case "second-approval":
				var o kyc.Outcome
				o, err = s.KYC.SecondApprove(ctx, typ, id, p.UserID, body.Note)
				out = o
			default:
				var o kyc.Outcome
				o, err = s.KYC.Decide(ctx, typ, id, p.UserID, decisive[action], in)
				out = o
				if o.AwaitingSecondApproval {
					status = http.StatusAccepted
				}
			}
		case TypeBeneficiary:
			switch action {
			case "assign":
				err = s.Benefs.Assign(ctx, id, p.UserID, body.AssigneeID)
			case "start-review":
				err = s.Benefs.StartReview(ctx, id, p.UserID)
			case "request-info":
				err = s.Benefs.RequestInformation(ctx, id, p.UserID, body.Message, body.Items)
			case "second-approval":
				var o kyc.Outcome
				o, err = s.Benefs.SecondApprove(ctx, id, p.UserID, body.Note)
				out = o
			default:
				var o kyc.Outcome
				o, err = s.Benefs.Decide(ctx, id, p.UserID, decisive[action], in)
				out = o
				if o.AwaitingSecondApproval {
					status = http.StatusAccepted
				}
			}
		case TypeDestination:
			ri := payouts.ReviewInput{ReasonCode: body.ReasonCode, Note: body.Note, UserMessage: body.UserMessage, NameMatch: body.NameMatch != nil && *body.NameMatch}
			switch action {
			case "assign":
				err = s.Payouts.Assign(ctx, id, p.UserID, body.AssigneeID)
			case "start-review":
				// destinations have no separate review state: assignment starts the review
			case "request-info":
				err = s.Payouts.RequestInformation(ctx, id, p.UserID, body.Message)
			case "approve":
				err = s.Payouts.Approve(ctx, id, p.UserID, ri)
			case "reject":
				err = s.Payouts.Reject(ctx, id, p.UserID, ri)
			case "suspend", "reinstate":
				err = s.Payouts.SetSuspended(ctx, id, p.UserID, body.ReasonCode, action == "suspend")
			default:
				err = errs.New(errs.Conflict, "ACTION_NOT_ALLOWED", "This action does not apply to payout destinations.")
			}
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		httpx.WriteData(w, r, status, out)
	}
}

// RevealIDNumber: POST /admin/verification/cases/{case_id}/reveal-identity-number (permission + step-up by policy)
func (s *Service) RevealIDNumber(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Justification string `json:"justification"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.KYC.RevealIDNumber(r.Context(), r.PathValue("case_id"), principal(r).UserID, body.Justification)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]string{"id_document_number": n})
}

// ListPolicies: GET /admin/verification/policies
func (s *Service) ListPolicies(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !p.Has("verification_policy.request") && !p.Has("verification_policy.approve") {
		s.fail(w, r, errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for verification policies."))
		return
	}
	list, err := s.KYC.ListPolicies(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, list)
}

// ProposePolicy: POST /admin/verification/policies
func (s *Service) ProposePolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PolicyVersion string          `json:"policy_version"`
		Rules         json.RawMessage `json:"rules"`
		ChangeReason  string          `json:"change_reason"`
		DecisionRef   string          `json:"decision_ref"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.KYC.ProposePolicy(r.Context(), principal(r).UserID, body.PolicyVersion, body.Rules, body.ChangeReason, body.DecisionRef)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, map[string]string{"id": id, "status": "PROPOSED"})
}

// DecidePolicy: POST /admin/verification/policies/{policy_id}/approve | /reject
func (s *Service) DecidePolicy(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 {
			var body struct{}
			if err := httpx.DecodeJSON(r, &body); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if err := s.KYC.ApprovePolicy(r.Context(), principal(r).UserID, r.PathValue("policy_id"), approve); err != nil {
			s.fail(w, r, err)
			return
		}
		httpx.WriteData(w, r, http.StatusOK, map[string]string{"status": map[bool]string{true: "APPROVED", false: "REJECTED"}[approve]})
	}
}
