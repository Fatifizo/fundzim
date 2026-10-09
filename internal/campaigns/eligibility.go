package campaigns

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Eligibility actions (interface-contracts §5). There is no universal "eligible" flag: each action has its own
// gates, and a campaign that is publicly visible is not thereby able to accept donations (later stages).
const (
	ActCreateDraft     = "CREATE_DRAFT"
	ActEditDraft       = "EDIT_DRAFT"
	ActSubmit          = "SUBMIT_FOR_REVIEW"
	ActApprove         = "APPROVE"
	ActPublish         = "PUBLISH"
	ActUpdatePublished = "UPDATE_PUBLISHED"
	ActReactivate      = "REACTIVATE"
)

// FlagIndividualForOthers gates campaigns where an individual raises funds for someone else (LR-046 – LR-048).
const FlagIndividualForOthers = "campaign.individual_for_others.enabled"

// Reason is one unmet requirement.
type Reason struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Result is an eligibility evaluation.
type Result struct {
	Action        string   `json:"action"`
	Allowed       bool     `json:"allowed"`
	Reasons       []Reason `json:"reasons"`
	PolicyVersion string   `json:"policy_version"`
}

var messages = map[string]string{
	"ACCOUNT_NOT_ACTIVE":                 "Your account is not active.",
	"AGE_ATTESTATION_REQUIRED":           "Confirm your age before creating a campaign.",
	"AGE_REQUIREMENT_NOT_MET":            "Your verified date of birth does not meet the age requirement.",
	"BASIC_VERIFICATION_REQUIRED":        "Verify your email address and phone number and confirm your age first.",
	"IDENTITY_VERIFICATION_REQUIRED":     "Verify your identity before submitting or publishing a campaign.",
	"ORGANISATION_VERIFICATION_REQUIRED": "The organisation must complete its verification first.",
	"NOT_ORGANISATION_ADMIN":             "Only organisation admins can manage the organisation's campaigns.",
	"REPRESENTATIVE_AUTHORITY_REQUIRED":  "You need verified authority to submit campaigns for this organisation.",
	"BENEFICIARY_REQUIRED":               "Choose who the funds are for.",
	"BENEFICIARY_NOT_VERIFIED":           "The beneficiary must be verified first.",
	"BENEFICIARY_NOT_AUTHORISED":         "This beneficiary does not belong to the campaign owner.",
	"INDIVIDUAL_FOR_OTHERS_DISABLED":     "Raising funds for someone else is not available yet for individual accounts.",
	"MINOR_DISCLOSURE_NOT_ALLOWED":       "A child's details cannot be shown publicly.",
	"ACCOUNT_RESTRICTED":                 "This action is not available for this account. Contact support if you have questions.",
	"CATEGORY_INACTIVE":                  "This category is not available.",
	"CATEGORY_REQUIRES_ORGANISATION":     "This category is only for verified organisations.",
	"COVER_IMAGE_REQUIRED":               "Add a cover image that has passed the safety checks.",
	"MEDIA_NOT_READY":                    "Some images are still being checked.",
	"CURRENCY_NOT_AVAILABLE":             "This currency is not available for campaign goals.",
	"GOAL_OUT_OF_RANGE":                  "The goal is outside the allowed range.",
	"MISSING_FIELD":                      "Complete this field.",
	"INVALID_LENGTH":                     "This field is too short or too long.",
	"RESUBMISSION_LIMIT_REACHED":         "This campaign cannot be submitted again.",
	"POLICY_UNAVAILABLE":                 "Campaign rules are temporarily unavailable. Try again later.",
}

type evalCtx struct {
	s       *Service
	ctx     context.Context
	pol     Policy
	reasons []Reason
}

func (e *evalCtx) fail(code, field string) {
	for _, r := range e.reasons {
		if r.Code == code && r.Field == field {
			return
		}
	}
	e.reasons = append(e.reasons, Reason{Code: code, Field: field, Message: messages[code]})
}

// EvaluateCreate evaluates CREATE_DRAFT for a user (optionally for an organisation).
func (s *Service) EvaluateCreate(ctx context.Context, userID, orgID, category string) (Result, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return Result{}, err
	}
	e := &evalCtx{s: s, ctx: ctx, pol: pol}
	if err := e.person(userID, pol.Rules.Gates.CreateDraftMinLevel); err != nil {
		return Result{}, err
	}
	subjects := []Subject{{Type: "USER", ID: userID}}
	if orgID != "" {
		role, err := s.Orgs.MemberRole(ctx, orgID, userID)
		if err != nil {
			return Result{}, err
		}
		if role != organisations.RoleAdmin {
			e.fail("NOT_ORGANISATION_ADMIN", "organisation_id")
		}
		subjects = append(subjects, Subject{Type: "ORGANISATION", ID: orgID})
	}
	if err := e.restrictions(subjects, ActCreateDraft); err != nil {
		return Result{}, err
	}
	if category != "" {
		if err := e.category(category, orgID != ""); err != nil {
			return Result{}, err
		}
	}
	return e.result(ActCreateDraft), nil
}

// Evaluate evaluates an action on an existing campaign for the acting user (the owner side; staff actions
// evaluate the owner side too: an approval or publication re-checks that the owner is still eligible).
func (s *Service) Evaluate(ctx context.Context, q querier, action, actorUserID string, c campaignRow) (Result, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return Result{}, err
	}
	e := &evalCtx{s: s, ctx: ctx, pol: pol}
	owner := c.CreatedBy // the person who acts for an organisation campaign is checked as its representative
	if c.OwnerUserID != nil {
		owner = *c.OwnerUserID
	}
	person := owner
	if actorUserID != "" && (action == ActCreateDraft || action == ActEditDraft || action == ActSubmit) {
		person = actorUserID
	}
	minLevel := pol.Rules.Gates.SubmitMinLevel
	if action == ActEditDraft || action == ActUpdatePublished {
		minLevel = pol.Rules.Gates.CreateDraftMinLevel
	}
	if err := e.person(person, minLevel); err != nil {
		return Result{}, err
	}
	if c.OwnerOrgID != nil {
		if err := e.organisation(*c.OwnerOrgID, person, action); err != nil {
			return Result{}, err
		}
	}
	level, err := s.Restricted(ctx, c.access())
	if err != nil {
		return Result{}, err
	}
	e.applyRestriction(level, action)
	if action == ActEditDraft || action == ActUpdatePublished {
		return e.result(action), nil
	}
	if err := e.category(c.Category, c.OwnerOrgID != nil); err != nil {
		return Result{}, err
	}
	e.content(c)
	if _, det := s.validateGoal(ctx, pol, itoa(c.GoalAmountMinor), c.GoalCurrency); len(det) > 0 {
		e.fail(det[0].Code, det[0].Field)
	}
	if err := e.beneficiary(q, c); err != nil {
		return Result{}, err
	}
	if err := e.media(c.ID); err != nil {
		return Result{}, err
	}
	if action == ActSubmit && pol.Rules.MaxResubmissions > 0 && c.ResubmissionCount > pol.Rules.MaxResubmissions {
		e.fail("RESUBMISSION_LIMIT_REACHED", "")
	}
	return e.result(action), nil
}

func (e *evalCtx) result(action string) Result {
	r := Result{Action: action, Allowed: len(e.reasons) == 0, Reasons: e.reasons, PolicyVersion: e.pol.Version}
	if r.Reasons == nil {
		r.Reasons = []Reason{}
	}
	return r
}

// person checks account status, age attestation and the KYC level (gated on kyc.Level, never the mirror).
func (e *evalCtx) person(userID, minLevel string) error {
	acct, err := users.ByID(e.ctx, e.s.Pool, userID)
	if errors.Is(err, users.ErrNotFound) {
		e.fail("ACCOUNT_NOT_ACTIVE", "")
		return nil
	}
	if err != nil {
		return err
	}
	if acct.Status != "ACTIVE" || acct.Kind != "USER" {
		e.fail("ACCOUNT_NOT_ACTIVE", "")
	}
	if e.s.Age == nil {
		return errors.New("campaigns: age checker not configured")
	}
	attested, contradicted, err := e.s.Age.Attested(e.ctx, userID)
	if err != nil {
		return err
	}
	if contradicted {
		e.fail("AGE_REQUIREMENT_NOT_MET", "")
	} else if !attested {
		e.fail("AGE_ATTESTATION_REQUIRED", "")
	}
	st, err := e.s.KYC.Level(e.ctx, userID)
	if err != nil {
		return err
	}
	if st.Status != kyc.ProfileActive || !kyc.AtLeast(st.Level, minLevel) {
		if minLevel == kyc.LevelBasic {
			e.fail("BASIC_VERIFICATION_REQUIRED", "")
		} else {
			e.fail("IDENTITY_VERIFICATION_REQUIRED", "")
		}
	}
	return nil
}

func (e *evalCtx) organisation(orgID, person, action string) error {
	role, err := e.s.Orgs.MemberRole(e.ctx, orgID, person)
	if err != nil {
		return err
	}
	if action == ActCreateDraft || action == ActEditDraft || action == ActSubmit {
		if role != organisations.RoleAdmin {
			e.fail("NOT_ORGANISATION_ADMIN", "")
		}
	}
	if action == ActEditDraft || action == ActUpdatePublished || action == ActCreateDraft {
		return nil
	}
	st, err := e.s.KYC.OrganisationLevel(e.ctx, orgID)
	if err != nil {
		return err
	}
	if st.Status != kyc.ProfileActive || !kyc.AtLeast(st.Level, e.pol.Rules.Gates.OrganisationMinLevel) {
		e.fail("ORGANISATION_VERIFICATION_REQUIRED", "")
	}
	ok, err := e.s.KYC.RepresentativeHas(e.ctx, orgID, person, e.pol.Rules.Gates.OrganisationSubmitPermission)
	if err != nil {
		return err
	}
	if !ok {
		e.fail("REPRESENTATIVE_AUTHORITY_REQUIRED", "")
	}
	return nil
}

func (e *evalCtx) restrictions(subjects []Subject, action string) error {
	if e.s.Restrictions == nil {
		return errors.New("campaigns: restrictions reader not configured")
	}
	levels, err := e.s.Restrictions.Levels(e.ctx, subjects)
	if err != nil {
		return err
	}
	e.applyRestriction(highest(levels), action)
	return nil
}

// applyRestriction is the ADR-037 §3 matrix. Reasons never disclose the restriction or its cause.
func (e *evalCtx) applyRestriction(level, action string) {
	switch level {
	case RestrictionSuspended, RestrictionOffboarded:
		e.fail("ACCOUNT_RESTRICTED", "")
	case RestrictionRestricted:
		if action != ActEditDraft && action != ActUpdatePublished {
			e.fail("ACCOUNT_RESTRICTED", "")
		}
	}
}

func (e *evalCtx) category(code string, isOrg bool) error {
	var active, needsOrg bool
	err := e.s.Pool.QueryRow(e.ctx, `SELECT active, requires_organisation FROM app.campaign_categories WHERE code = $1`, code).Scan(&active, &needsOrg)
	if errors.Is(err, pgx.ErrNoRows) {
		e.fail("CATEGORY_INACTIVE", "category")
		return nil
	}
	if err != nil {
		return err
	}
	if !active {
		e.fail("CATEGORY_INACTIVE", "category")
	}
	if needsOrg && !isOrg {
		e.fail("CATEGORY_REQUIRES_ORGANISATION", "category")
	}
	return nil
}

func (e *evalCtx) content(c campaignRow) {
	lim := e.pol.Rules.Content
	for _, f := range []struct {
		name, v  string
		min, max int
		multi    bool
	}{{"title", c.Title, lim.Title[0], lim.Title[1], false}, {"summary", c.Summary, lim.Summary[0], lim.Summary[1], false},
		{"story", c.Story, lim.Story[0], lim.Story[1], true}} {
		if f.v == "" {
			e.fail("MISSING_FIELD", f.name)
		} else if _, code := CleanText(f.v, f.min, f.max, f.multi); code != "" {
			e.fail("INVALID_LENGTH", f.name)
		}
	}
}

// beneficiary checks the current link: it must exist, belong to the campaign owner, and be verified (a SELF
// beneficiary of an identity-verified owner needs no separate approval; any other type must be APPROVED).
// Individuals raising for someone else are blocked while the policy flag is off (LR-046 – LR-048).
func (e *evalCtx) beneficiary(q querier, c campaignRow) error {
	l, err := e.s.currentLink(e.ctx, q, c.ID)
	if err != nil {
		return err
	}
	if l == nil {
		e.fail("BENEFICIARY_REQUIRED", "beneficiary")
		return nil
	}
	b, err := e.s.Beneficiaries.View(e.ctx, l.BeneficiaryID)
	if err != nil {
		return err
	}
	if !ownsBeneficiary(c, b.Owner.Type, b.Owner.ID) {
		e.fail("BENEFICIARY_NOT_AUTHORISED", "beneficiary")
		return nil
	}
	status := b.Status()
	switch {
	case b.BeneficiaryType == "SELF":
		if status == "REJECTED" || status == "REVOKED" || status == "SUSPENDED" || status == "EXPIRED" {
			e.fail("BENEFICIARY_NOT_VERIFIED", "beneficiary")
		}
	case status != "APPROVED":
		e.fail("BENEFICIARY_NOT_VERIFIED", "beneficiary")
	}
	if b.BeneficiaryType == "MINOR" && l.Disclosure != "NONE" {
		e.fail("MINOR_DISCLOSURE_NOT_ALLOWED", "beneficiary.disclosure")
	}
	if c.OwnerUserID != nil && b.BeneficiaryType != "SELF" {
		on, err := e.s.flag(e.ctx, FlagIndividualForOthers)
		if err != nil {
			return err
		}
		if !on {
			e.fail("INDIVIDUAL_FOR_OTHERS_DISABLED", "beneficiary")
		}
	}
	return nil
}

func ownsBeneficiary(c campaignRow, ownerType, ownerID string) bool {
	if c.OwnerUserID != nil {
		return ownerType == "USER" && ownerID == *c.OwnerUserID
	}
	return ownerType == "ORGANISATION" && ownerID == *c.OwnerOrgID
}

func (e *evalCtx) media(campaignID string) error {
	if !e.pol.Rules.RequireCoverImage {
		return nil
	}
	if e.s.media == nil {
		e.fail("COVER_IMAGE_REQUIRED", "media")
		return nil
	}
	m, err := e.s.media.Readiness(e.ctx, campaignID)
	if err != nil {
		return err
	}
	if !m.CoverApproved {
		e.fail("COVER_IMAGE_REQUIRED", "media")
	}
	if m.Pending > 0 {
		e.fail("MEDIA_NOT_READY", "media")
	}
	return nil
}

func (s *Service) flag(ctx context.Context, key string) (bool, error) {
	if s.Flags == nil {
		return false, nil // fail closed: an unconfigured flag is off
	}
	return s.Flags.Enabled(ctx, key)
}

// recordEvaluation persists a gated evaluation (append-only evidence of what was checked).
func (s *Service) recordEvaluation(ctx context.Context, tx pgx.Tx, campaignID, actorID string, r Result) error {
	reasons, _ := json.Marshal(r.Reasons)
	var a any
	if actorID != "" {
		a = actorID
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.campaign_eligibility_evaluations (id, campaign_id, action, actor_id, policy_version, allowed, reasons, evaluated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), campaignID, r.Action, a, r.PolicyVersion, r.Allowed, reasons, s.now())
	return err
}

// notEligible is the 422 for a refused gated action; details carry the reason codes.
func notEligible(r Result) error {
	det := make([]errs.Detail, 0, len(r.Reasons))
	for _, x := range r.Reasons {
		det = append(det, errs.Detail{Field: x.Field, Code: x.Code})
	}
	e := errs.New(errs.Unprocessable, "NOT_ELIGIBLE", "Some requirements are not met yet.")
	e.Details = det
	return e
}
