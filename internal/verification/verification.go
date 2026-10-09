// Package verification is the HTTP orchestration layer for Stage 5 (like `admin`, it owns no tables): it
// composes kyc (KYC, KYB, documents), beneficiaries, payouts (destinations) and storage behind the routes in
// docs/stage-5/interface-contracts.md §7, resolves which module owns a subject, and applies object-level
// authorisation before any module call. Staff decisions check the type-specific permission and a fresh
// step-up here because one route serves several verification types.
package verification

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/beneficiaries"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// RiskReader is the reviewer-facing read of the risk module (optional).
type RiskReader interface {
	Summary(ctx context.Context, subjectType, subjectID string) (any, error)
}

// Service wires the verification endpoints.
type Service struct {
	KYC          *kyc.Service
	Benefs       *beneficiaries.Service
	Payouts      *payouts.Service
	Storage      *storage.Service
	Orgs         *organisations.Service
	Risk         RiskReader
	Clock        clock.Clock
	Logger       *slog.Logger
	StepUpMaxAge time.Duration
	TicketTTL    time.Duration
	MaxUpload    int64
	// StaffCan reports whether an active staff account holds a permission (assignee eligibility). Nil refuses
	// assignment to anyone but the caller (fail closed).
	StaffCan func(ctx context.Context, staffID, permission string) (bool, error)
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, s.Logger, err)
}

func ifMatch(r *http.Request) int {
	v := strings.Trim(r.Header.Get("If-Match"), `" `)
	n, _ := strconv.Atoi(v)
	return n
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }

// ---- KYC (individual) --------------------------------------------------------------------------------------

// KYCStatus: GET /kyc/status
func (s *Service) KYCStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	st, err := s.KYC.Level(ctx, p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var cv any
	if id, err := s.KYC.CurrentCaseID(ctx, p.UserID); err != nil {
		s.fail(w, r, err)
		return
	} else if id != "" {
		v, err := s.KYC.CaseView(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		cv = v
	}
	active := st.Status == kyc.ProfileActive
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"level": st.Level, "status": st.Status, "risk_level": st.RiskLevel, "expires_at": st.ExpiresAt,
		"case": cv, "gates": map[string]bool{
			"create_draft":    active && kyc.AtLeast(st.Level, kyc.LevelBasic),
			"submit_campaign": active && kyc.AtLeast(st.Level, kyc.LevelIdentity),
			"withdraw":        false, // no payout processing exists in Stage 5
		}})
}

// CreateKYCCase: POST /kyc/cases
func (s *Service) CreateKYCCase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetLevel string `json:"target_level"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.KYC.CreateCase(r.Context(), principal(r).UserID, body.TargetLevel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeCase(w, r, id, http.StatusCreated)
}

func (s *Service) writeCase(w http.ResponseWriter, r *http.Request, id string, status int) {
	v, err := s.KYC.CaseView(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(v.Version)))
	httpx.WriteData(w, r, status, v)
}

// CurrentKYCCase: GET /kyc/cases/current
func (s *Service) CurrentKYCCase(w http.ResponseWriter, r *http.Request) {
	id, err := s.KYC.CurrentCaseID(r.Context(), principal(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id == "" {
		noStore(w)
		httpx.WriteData(w, r, http.StatusOK, nil)
		return
	}
	s.writeCase(w, r, id, http.StatusOK)
}

// ownKYCCase checks the path case belongs to the caller (404 otherwise).
func (s *Service) ownKYCCase(r *http.Request) (string, error) {
	id := r.PathValue("case_id")
	owner, err := s.KYC.CaseOwner(r.Context(), id)
	if err != nil {
		return "", err
	}
	if owner != principal(r).UserID {
		return "", kyc.ErrCaseNotFound
	}
	return id, nil
}

// UpdateKYCCase: PATCH /kyc/cases/{case_id}
func (s *Service) UpdateKYCCase(w http.ResponseWriter, r *http.Request) {
	var patch kyc.IdentityPatch
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("case_id")
	if err := s.KYC.UpdateDraft(r.Context(), principal(r).UserID, id, patch, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeCase(w, r, id, http.StatusOK)
}

// SubmitKYCCase: POST /kyc/cases/{case_id}/submit
func (s *Service) SubmitKYCCase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("case_id")
	if err := s.KYC.Submit(r.Context(), principal(r).UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeCase(w, r, id, http.StatusOK)
}

// WithdrawKYCCase: POST /kyc/cases/{case_id}/withdraw
func (s *Service) WithdrawKYCCase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("case_id")
	if err := s.KYC.Withdraw(r.Context(), principal(r).UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeCase(w, r, id, http.StatusOK)
}

// ---- KYB (organisation) -------------------------------------------------------------------------------------

// orgRole returns the caller's role (404 for non-members: existence not revealed).
func (s *Service) orgRole(r *http.Request) (string, string, error) {
	orgID := r.PathValue("org_id")
	role, err := s.Orgs.MemberRole(r.Context(), orgID, principal(r).UserID)
	if err != nil {
		return "", "", err
	}
	if role == "" {
		return "", "", errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
	}
	return orgID, role, nil
}

func (s *Service) requireOrgAdmin(r *http.Request) (string, error) {
	orgID, role, err := s.orgRole(r)
	if err != nil {
		return "", err
	}
	if role != organisations.RoleAdmin {
		return "", errs.New(errs.Forbidden, "PERMISSION_DENIED", "Only organisation administrators can manage verification.")
	}
	return orgID, nil
}

func (s *Service) writeKYB(w http.ResponseWriter, r *http.Request, orgID string, full bool, status int) {
	ctx := r.Context()
	st, err := s.KYC.OrganisationLevel(ctx, orgID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var cv any
	id, err := s.KYC.CurrentKYBCaseID(ctx, orgID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if id != "" {
		v, err := s.KYC.KYBCaseView(ctx, id, full)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		cv = v
		w.Header().Set("ETag", strconv.Quote(strconv.Itoa(v.Version)))
	}
	noStore(w)
	httpx.WriteData(w, r, status, map[string]any{"level": st.Level, "status": st.Status, "expires_at": st.ExpiresAt, "case": cv})
}

// CreateKYB: POST /organisations/{org_id}/kyb
func (s *Service) CreateKYB(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !principal(r).EmailVerified {
		s.fail(w, r, errs.New(errs.Forbidden, "EMAIL_NOT_VERIFIED", "Verify your email address first."))
		return
	}
	var body struct{}
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &body); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	_, orgType, _, err := s.Orgs.Summary(r.Context(), orgID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.KYC.CreateKYB(r.Context(), orgID, orgType, principal(r).UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeKYB(w, r, orgID, true, http.StatusCreated)
}

// GetKYB: GET /organisations/{org_id}/kyb (members: status only; ORG_ADMIN: full)
func (s *Service) GetKYB(w http.ResponseWriter, r *http.Request) {
	orgID, role, err := s.orgRole(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeKYB(w, r, orgID, role == organisations.RoleAdmin, http.StatusOK)
}

// UpdateKYB: PATCH /organisations/{org_id}/kyb
func (s *Service) UpdateKYB(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var p kyc.KYBDetailsPatch
	if err := httpx.DecodeJSON(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.KYC.UpdateKYB(r.Context(), orgID, p, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeKYB(w, r, orgID, true, http.StatusOK)
}

// AddKYBPerson: POST /organisations/{org_id}/kyb/persons
func (s *Service) AddKYBPerson(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in kyc.PersonInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.KYC.AddPerson(r.Context(), orgID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, map[string]string{"id": id})
}

// RemoveKYBPerson: DELETE /organisations/{org_id}/kyb/persons/{person_id}
func (s *Service) RemoveKYBPerson(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.KYC.RemovePerson(r.Context(), orgID, r.PathValue("person_id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SubmitKYB: POST /organisations/{org_id}/kyb/submit
func (s *Service) SubmitKYB(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.KYC.SubmitKYB(r.Context(), orgID, principal(r).UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeKYB(w, r, orgID, true, http.StatusOK)
}

// WithdrawKYB: POST /organisations/{org_id}/kyb/withdraw
func (s *Service) WithdrawKYB(w http.ResponseWriter, r *http.Request) {
	orgID, err := s.requireOrgAdmin(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.KYC.WithdrawKYB(r.Context(), orgID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeKYB(w, r, orgID, true, http.StatusOK)
}

// ---- beneficiaries -------------------------------------------------------------------------------------------

// CreateBeneficiary: POST /beneficiaries
func (s *Service) CreateBeneficiary(w http.ResponseWriter, r *http.Request) {
	var in beneficiaries.Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.Benefs.Create(r.Context(), principal(r).UserID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeBeneficiary(w, r, id, http.StatusCreated)
}

func (s *Service) writeBeneficiary(w http.ResponseWriter, r *http.Request, id string, status int) {
	b, err := s.Benefs.Get(r.Context(), principal(r).UserID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(b.Version)))
	httpx.WriteData(w, r, status, b)
}

// ListBeneficiaries: GET /beneficiaries
func (s *Service) ListBeneficiaries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Benefs.List(r.Context(), principal(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, list)
}

// GetBeneficiary: GET /beneficiaries/{beneficiary_id}
func (s *Service) GetBeneficiary(w http.ResponseWriter, r *http.Request) {
	s.writeBeneficiary(w, r, r.PathValue("beneficiary_id"), http.StatusOK)
}

// UpdateBeneficiary: PATCH /beneficiaries/{beneficiary_id}
func (s *Service) UpdateBeneficiary(w http.ResponseWriter, r *http.Request) {
	var in beneficiaries.Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("beneficiary_id")
	if in.BeneficiaryType != "" || in.OwnerOrganisationID != nil || in.BeneficiaryOrganisationID != nil || in.DateOfBirth != nil {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "beneficiary_type", Code: "IMMUTABLE"}))
		return
	}
	if err := s.Benefs.Update(r.Context(), principal(r).UserID, id, in, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeBeneficiary(w, r, id, http.StatusOK)
}

// SubmitBeneficiary: POST /beneficiaries/{beneficiary_id}/submit
func (s *Service) SubmitBeneficiary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("beneficiary_id")
	if err := s.Benefs.Submit(r.Context(), principal(r).UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeBeneficiary(w, r, id, http.StatusOK)
}

// WithdrawBeneficiary: POST /beneficiaries/{beneficiary_id}/withdraw
func (s *Service) WithdrawBeneficiary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("beneficiary_id")
	if err := s.Benefs.Withdraw(r.Context(), principal(r).UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeBeneficiary(w, r, id, http.StatusOK)
}

// ---- payout destinations -------------------------------------------------------------------------------------

// CreateDestination: POST /payout-destinations
func (s *Service) CreateDestination(w http.ResponseWriter, r *http.Request) {
	var in payouts.Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.Payouts.Create(r.Context(), principal(r).UserID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeDestination(w, r, id, http.StatusCreated)
}

func (s *Service) writeDestination(w http.ResponseWriter, r *http.Request, id string, status int) {
	d, err := s.Payouts.Get(r.Context(), principal(r).UserID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(d.Version)))
	httpx.WriteData(w, r, status, d)
}

// ListDestinations: GET /payout-destinations
func (s *Service) ListDestinations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Payouts.List(r.Context(), principal(r).UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, list)
}

// GetDestination: GET /payout-destinations/{destination_id}
func (s *Service) GetDestination(w http.ResponseWriter, r *http.Request) {
	s.writeDestination(w, r, r.PathValue("destination_id"), http.StatusOK)
}

// ChangeDestination: PATCH /payout-destinations/{destination_id}
func (s *Service) ChangeDestination(w http.ResponseWriter, r *http.Request) {
	var in payouts.Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Rail != "" || in.Currency != "" || in.OwnerOrganisationID != nil || in.Payee != nil {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "rail", Code: "IMMUTABLE"}))
		return
	}
	id := r.PathValue("destination_id")
	if err := s.Payouts.Change(r.Context(), principal(r).UserID, id, in, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeDestination(w, r, id, http.StatusOK)
}

// RequestDestinationVerification: POST /payout-destinations/{destination_id}/verification
func (s *Service) RequestDestinationVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Method      string   `json:"method"`
		DocumentIDs []string `json:"document_ids"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("destination_id")
	if err := s.Payouts.RequestVerification(r.Context(), principal(r).UserID, id, body.Method); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeDestination(w, r, id, http.StatusOK)
}

// RetireDestination: DELETE /payout-destinations/{destination_id}
func (s *Service) RetireDestination(w http.ResponseWriter, r *http.Request) {
	if err := s.Payouts.Retire(r.Context(), principal(r).UserID, r.PathValue("destination_id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeOptional(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return httpx.DecodeJSON(r, v)
}
