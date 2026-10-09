// Package beneficiaries models who the money is for, separately from the owner who raises it (ADR-016,
// Stage 1 beneficiary-verification). A relationship type never establishes authority: the authority basis is
// declared separately and must be evidenced. Verification follows the shared verification-case workflow
// (ADR-034); minors, incapacitated adults and RESTRICTED risk always need two reviewers. A verified owner
// does not make a beneficiary verified. Identity details (name for C3 purposes, date of birth) live
// encrypted in the kyc module; evidence documents are kyc documents.
package beneficiaries

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
)

// Types, relationships and authority bases (migration 20261009150300).
var (
	Types         = map[string]string{"SELF": "INDIVIDUAL", "INDIVIDUAL": "INDIVIDUAL", "MINOR": "INDIVIDUAL", "INCAPACITATED_ADULT": "INDIVIDUAL", "ORGANISATION": "ORGANISATION", "COMMUNITY_GROUP": "ORGANISATION"}
	Relationships = map[string]bool{"SELF": true, "PARENT_GUARDIAN": true, "FAMILY_MEMBER": true, "AUTHORIZED_REPRESENTATIVE": true,
		"ORGANISATION_REPRESENTATIVE": true, "THIRD_PARTY_ORGANISER": true, "OTHER": true}
	Authorities = map[string]bool{"NOT_REQUIRED": true, "BENEFICIARY_CONSENT": true, "PARENTAL_RESPONSIBILITY": true, "GUARDIANSHIP_ORDER": true,
		"LEGAL_REPRESENTATION": true, "ORGANISATION_AUTHORITY": true, "GROUP_MANDATE": true, "INSTITUTION_CONFIRMATION": true}
)

var (
	ErrNotFound   = errs.New(errs.NotFound, "BENEFICIARY_NOT_FOUND", "No such beneficiary.")
	errNotEdit    = errs.New(errs.Conflict, "CASE_NOT_EDITABLE", "This beneficiary can no longer be changed.")
	errNotAllowed = errs.New(errs.Conflict, "ACTION_NOT_ALLOWED", "This action is not allowed in the beneficiary's current status.")
	errSelf       = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot review a beneficiary you own or whose organisation you belong to.")
	errAssigned   = errs.New(errs.Conflict, "NOT_ASSIGNED", "Assign the case to yourself before deciding it.")
)

// Service is the beneficiaries module (app pool).
type Service struct {
	Pool   *pgxpool.Pool
	KYC    *kyc.Service
	Orgs   *organisations.Service
	Clock  clock.Clock
	Logger *slog.Logger
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Input declares or edits a beneficiary.
type Input struct {
	OwnerOrganisationID       *string `json:"owner_organisation_id"`
	BeneficiaryType           string  `json:"beneficiary_type"`
	DisplayName               *string `json:"display_name"`
	FullName                  *string `json:"full_name"`
	DateOfBirth               *string `json:"date_of_birth"`
	BeneficiaryOrganisationID *string `json:"beneficiary_organisation_id"`
	Relationship              *struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	} `json:"relationship"`
	AuthorityBasis *string `json:"authority_basis"`
}

// Beneficiary is the owner's view.
type Beneficiary struct {
	ID              string                  `json:"id"`
	Owner           Owner                   `json:"owner"`
	BeneficiaryType string                  `json:"beneficiary_type"`
	Kind            string                  `json:"kind"`
	DisplayName     string                  `json:"display_name"`
	FullName        *string                 `json:"full_name"`
	OrganisationID  *string                 `json:"beneficiary_organisation_id"`
	Relationship    Relationship            `json:"relationship"`
	AuthorityBasis  string                  `json:"authority_basis"`
	Verification    Verification            `json:"verification"`
	Documents       []kyc.Document          `json:"documents"`
	Requirements    []kyc.RequirementStatus `json:"requirements"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	Version         int                     `json:"version"`
	identityRef     *string
	ownerUser       *string
	ownerOrg        *string
	createdBy       string
	assignedTo      *string
	pendingOutcome  *string
	pendingBy       *string
	pendingReason   *string
	requiresSecond  bool
	status          string
	riskLevel       string
	policyVersion   string
	submittedAt     *time.Time
}

// Owner identifies the owner.
type Owner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Relationship is the declared relationship.
type Relationship struct {
	Type        string  `json:"type"`
	Description *string `json:"description"`
}

// Verification is the verification state shown to owners.
type Verification struct {
	Status                 string                   `json:"status"`
	RiskLevel              string                   `json:"risk_level"`
	RequiresSecondApproval bool                     `json:"requires_second_approval"`
	InformationRequests    []kyc.InformationRequest `json:"information_requests"`
	Decision               *kyc.DecisionView        `json:"decision"`
	SubmittedAt            *time.Time               `json:"submitted_at"`
	ExpiresAt              *time.Time               `json:"expires_at"`
}

const selectB = `SELECT id, owner_user_id, owner_organisation_id, created_by, beneficiary_type, display_name, full_name, beneficiary_organisation_id,
	kyc_identity_ref, relationship_type, relationship_description, authority_basis, status, risk_level, requires_second_approval, policy_version,
	assigned_to, pending_outcome, pending_decided_by, pending_reason_code, submitted_at, expires_at, version, created_at, updated_at
	FROM app.beneficiaries`

func scan(row pgx.Row) (Beneficiary, error) {
	var b Beneficiary
	err := row.Scan(&b.ID, &b.ownerUser, &b.ownerOrg, &b.createdBy, &b.BeneficiaryType, &b.DisplayName, &b.FullName, &b.OrganisationID,
		&b.identityRef, &b.Relationship.Type, &b.Relationship.Description, &b.AuthorityBasis, &b.status, &b.riskLevel, &b.requiresSecond,
		&b.policyVersion, &b.assignedTo, &b.pendingOutcome, &b.pendingBy, &b.pendingReason, &b.submittedAt, &b.Verification.ExpiresAt,
		&b.Version, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	b.Kind = Types[b.BeneficiaryType]
	b.Verification.Status, b.Verification.RiskLevel, b.Verification.RequiresSecondApproval, b.Verification.SubmittedAt =
		b.status, b.riskLevel, b.requiresSecond, b.submittedAt
	if b.ownerUser != nil {
		b.Owner = Owner{Type: "USER", ID: *b.ownerUser}
	} else if b.ownerOrg != nil {
		b.Owner = Owner{Type: "ORGANISATION", ID: *b.ownerOrg}
	}
	return b, err
}

func (s *Service) load(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (Beneficiary, error) {
	if !ids.Valid(id) {
		return Beneficiary{}, ErrNotFound
	}
	sql := selectB + ` WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scan(q.QueryRow(ctx, sql, id))
}

// access: "edit" (owner user or ORG_ADMIN of owner org), "view" (also ORG_MEMBER), "" (none → 404).
func (s *Service) access(ctx context.Context, b Beneficiary, userID string) (string, error) {
	if b.ownerUser != nil {
		if *b.ownerUser == userID {
			return "edit", nil
		}
		return "", nil
	}
	role, err := s.Orgs.MemberRole(ctx, *b.ownerOrg, userID)
	switch {
	case err != nil:
		return "", err
	case role == organisations.RoleAdmin:
		return "edit", nil
	case role != "":
		return "view", nil
	}
	return "", nil
}

func cleanText(v string, min, max int) (string, bool) {
	t := strings.Join(strings.Fields(v), " ")
	n := utf8.RuneCountInString(t)
	return t, n >= min && n <= max && !strings.ContainsAny(t, "<>{}")
}

func ageOn(dob string, now time.Time) (int, bool) {
	t, err := time.Parse("2006-01-02", dob)
	if err != nil || t.After(now) {
		return 0, false
	}
	years := now.Year() - t.Year()
	// compare (month, day), not YearDay: day-of-year shifts by one after 28 February in leap years
	if now.Month() < t.Month() || (now.Month() == t.Month() && now.Day() < t.Day()) {
		years--
	}
	return years, true
}

// Create declares a beneficiary (verification DRAFT).
func (s *Service) Create(ctx context.Context, actorID string, in Input) (string, error) {
	kind, ok := Types[in.BeneficiaryType]
	var det []errs.Detail
	if !ok {
		return "", httpx.Validation(errs.Detail{Field: "beneficiary_type", Code: "INVALID_VALUE"})
	}
	var ownerUser, ownerOrg any
	if in.OwnerOrganisationID != nil {
		admin, err := s.Orgs.IsAdmin(ctx, *in.OwnerOrganisationID, actorID)
		if err != nil {
			return "", err
		}
		if !admin {
			return "", errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
		}
		if in.BeneficiaryType == "SELF" {
			det = append(det, errs.Detail{Field: "beneficiary_type", Code: "INVALID_VALUE"})
		}
		ownerOrg = *in.OwnerOrganisationID
	} else {
		ownerUser = actorID
	}
	rel, desc := "SELF", ""
	if in.Relationship != nil {
		rel, desc = in.Relationship.Type, strings.TrimSpace(in.Relationship.Description)
	}
	auth := "NOT_REQUIRED"
	if in.AuthorityBasis != nil {
		auth = *in.AuthorityBasis
	}
	if !Relationships[rel] || (rel == "SELF") != (in.BeneficiaryType == "SELF") {
		det = append(det, errs.Detail{Field: "relationship.type", Code: "INVALID_VALUE"})
	}
	if rel == "OTHER" && len(desc) < 3 {
		det = append(det, errs.Detail{Field: "relationship.description", Code: "MISSING_FIELD"})
	}
	if !Authorities[auth] || (auth == "NOT_REQUIRED") != (in.BeneficiaryType == "SELF") {
		det = append(det, errs.Detail{Field: "authority_basis", Code: "INVALID_VALUE"})
	}
	if in.BeneficiaryType == "MINOR" && auth != "PARENTAL_RESPONSIBILITY" && auth != "GUARDIANSHIP_ORDER" && auth != "LEGAL_REPRESENTATION" {
		det = append(det, errs.Detail{Field: "authority_basis", Code: "MINOR_REQUIRES_GUARDIAN_AUTHORITY"})
	}
	display := ""
	if in.DisplayName != nil {
		var ok bool
		if display, ok = cleanText(*in.DisplayName, 1, 100); !ok {
			det = append(det, errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"})
		}
	} else if in.BeneficiaryType != "SELF" {
		det = append(det, errs.Detail{Field: "display_name", Code: "MISSING_FIELD"})
	}
	var fullName any
	if in.FullName != nil {
		f, ok := cleanText(*in.FullName, 2, 150)
		if !ok {
			det = append(det, errs.Detail{Field: "full_name", Code: "INVALID_LENGTH"})
		}
		fullName = f
	} else if in.BeneficiaryType != "SELF" && in.BeneficiaryType != "ORGANISATION" {
		det = append(det, errs.Detail{Field: "full_name", Code: "MISSING_FIELD"})
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	adult := pol.Rules.AdultAge // INTERNAL_POLICY (LR-021): never a hard-coded legal age
	dob := ""
	if in.DateOfBirth != nil {
		dob = *in.DateOfBirth
		age, ok := ageOn(dob, s.now())
		switch {
		case !ok:
			det = append(det, errs.Detail{Field: "date_of_birth", Code: "INVALID_FORMAT"})
		case in.BeneficiaryType == "MINOR" && age >= adult:
			det = append(det, errs.Detail{Field: "date_of_birth", Code: "NOT_A_MINOR"})
		case in.BeneficiaryType != "MINOR" && age < adult && in.BeneficiaryType != "ORGANISATION":
			det = append(det, errs.Detail{Field: "beneficiary_type", Code: "MINOR_MUST_USE_MINOR_TYPE"})
		}
	} else if in.BeneficiaryType == "MINOR" {
		det = append(det, errs.Detail{Field: "date_of_birth", Code: "MISSING_FIELD"})
	}
	var benOrg any
	if kind == "ORGANISATION" && in.BeneficiaryType == "ORGANISATION" {
		if in.BeneficiaryOrganisationID == nil {
			det = append(det, errs.Detail{Field: "beneficiary_organisation_id", Code: "MISSING_FIELD"})
		} else if _, _, _, err := s.Orgs.Summary(ctx, *in.BeneficiaryOrganisationID); err != nil {
			det = append(det, errs.Detail{Field: "beneficiary_organisation_id", Code: "NOT_FOUND"})
		} else {
			benOrg = *in.BeneficiaryOrganisationID
		}
	}
	if len(det) > 0 {
		return "", httpx.Validation(det...)
	}
	var benUser any
	if in.BeneficiaryType == "SELF" {
		benUser = actorID
		display = "Self"
	}
	var identityRef any
	if in.FullName != nil && (dob != "" || in.BeneficiaryType == "MINOR" || in.BeneficiaryType == "INCAPACITATED_ADULT") {
		ref, err := s.KYC.BeneficiaryIdentity(ctx, fullName.(string), dob)
		if err != nil {
			return "", err
		}
		identityRef = ref
	}
	rule := pol.BeneficiaryRule(in.BeneficiaryType)
	second := rule.SecondApproval || in.BeneficiaryType == "MINOR" || in.BeneficiaryType == "INCAPACITATED_ADULT"
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.beneficiaries (id, owner_user_id, owner_organisation_id, created_by, beneficiary_type, display_name,
			full_name, beneficiary_user_id, beneficiary_organisation_id, kyc_identity_ref, relationship_type, relationship_description, authority_basis,
			status, requires_second_approval, policy_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'DRAFT',$14,$15)`,
			id, ownerUser, ownerOrg, actorID, in.BeneficiaryType, display, fullName, benUser, benOrg, identityRef, rel, nullable(desc), auth, second, pol.Version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, 1, "CREATED", "", verifcase.Draft, ""); err != nil {
			return err
		}
		if err := s.relationshipRow(ctx, tx, id, rel, desc, auth, actorID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "beneficiary.created", TargetType: "beneficiary", TargetID: id,
			Metadata: map[string]any{"beneficiary_type": in.BeneficiaryType, "relationship": rel, "authority_basis": auth}})
	})
	return id, err
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Service) relationshipRow(ctx context.Context, tx pgx.Tx, id, rel, desc, auth, actorID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.beneficiary_relationships (id, beneficiary_id, relationship_type, description, authority_basis, declared_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), id, rel, nullable(desc), auth, actorID)
	return err
}

func (s *Service) event(ctx context.Context, tx pgx.Tx, id string, version int, typ, from, to, reason string) error {
	at, aid := "SYSTEM", any(nil)
	if p := actorOf(ctx); p != "" {
		at, aid = actorTypeOf(ctx), p
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.beneficiary_events (id, beneficiary_id, beneficiary_version, event_type, from_status, to_status,
		actor_type, actor_id, reason_code, occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		ids.New(), id, version, typ, nullable(from), to, at, aid, nullable(reason), s.now())
	return err
}

func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ string, b Beneficiary, extra map[string]any) error {
	payload := map[string]any{"beneficiary_id": b.ID, "owner_type": b.Owner.Type, "owner_id": b.Owner.ID}
	for k, v := range extra {
		payload[k] = v
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "beneficiary", AggregateID: b.ID, EventType: typ, Payload: payload,
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

// Get returns a beneficiary the caller may see (404 otherwise: no IDOR).
func (s *Service) Get(ctx context.Context, actorID, id string) (Beneficiary, error) {
	b, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return b, err
	}
	acc, err := s.access(ctx, b, actorID)
	if err != nil {
		return b, err
	}
	if acc == "" {
		return Beneficiary{}, ErrNotFound
	}
	return s.enrich(ctx, b)
}

// View returns any beneficiary (reviewer endpoints; authorisation by the caller).
func (s *Service) View(ctx context.Context, id string) (Beneficiary, error) {
	b, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return b, err
	}
	return s.enrich(ctx, b)
}

func (s *Service) enrich(ctx context.Context, b Beneficiary) (Beneficiary, error) {
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return b, err
	}
	if b.Documents, err = s.KYC.DocumentsFor(ctx, "beneficiary_id", b.ID); err != nil {
		return b, err
	}
	if b.Requirements, err = s.KYC.Requirements(ctx, "beneficiary_id", b.ID, pol.BeneficiaryRule(b.BeneficiaryType).RequiredDocuments); err != nil {
		return b, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, message, items, requested_at, responded_at FROM app.beneficiary_information_requests
		WHERE beneficiary_id = $1 ORDER BY requested_at`, b.ID)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	b.Verification.InformationRequests = []kyc.InformationRequest{}
	for rows.Next() {
		var r kyc.InformationRequest
		if err := rows.Scan(&r.ID, &r.Message, &r.Items, &r.RequestedAt, &r.RespondedAt); err != nil {
			return b, err
		}
		b.Verification.InformationRequests = append(b.Verification.InformationRequests, r)
	}
	var d kyc.DecisionView
	var msg *string
	err = s.Pool.QueryRow(ctx, `SELECT outcome, reason_code, user_message, decided_at FROM app.beneficiary_verifications WHERE beneficiary_id = $1
		AND outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REJECT','SUSPEND','REVOKE','EXPIRE') ORDER BY decided_at DESC LIMIT 1`, b.ID).
		Scan(&d.Outcome, &d.ReasonCode, &msg, &d.DecidedAt)
	if err == nil {
		if msg != nil {
			d.Message = *msg
		}
		if d.Outcome == "APPROVE_WITH_CONDITIONS" {
			d.Outcome = "APPROVE"
		}
		b.Verification.Decision = &d
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	return b, nil
}

// List returns beneficiaries owned by the caller or by organisations they belong to.
func (s *Service) List(ctx context.Context, actorID string) ([]Beneficiary, error) {
	orgs, err := s.Orgs.OrganisationsOf(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if orgs == nil {
		orgs = []string{}
	}
	rows, err := s.Pool.Query(ctx, selectB+` WHERE owner_user_id = $1 OR owner_organisation_id = ANY($2::uuid[]) ORDER BY created_at DESC LIMIT 200`, actorID, orgs)
	if err != nil {
		return nil, err
	}
	var out []Beneficiary
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	rows.Close()
	res := []Beneficiary{}
	for _, b := range out {
		e, err := s.enrich(ctx, b)
		if err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, nil
}

// Update edits declared details while editable.
func (s *Service) Update(ctx context.Context, actorID, id string, in Input, expectVersion int) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if acc, err := s.access(ctx, b, actorID); err != nil {
			return err
		} else if acc != "edit" {
			return ErrNotFound
		}
		if !verifcase.Editable(b.status) {
			return errNotEdit
		}
		if expectVersion > 0 && expectVersion != b.Version {
			return errs.New(errs.Conflict, "CASE_STATE_CHANGED", "The beneficiary changed in the meantime. Reload and try again.")
		}
		var det []errs.Detail
		display, full := b.DisplayName, b.FullName
		if in.DisplayName != nil {
			v, ok := cleanText(*in.DisplayName, 1, 100)
			if !ok {
				det = append(det, errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"})
			}
			display = v
		}
		if in.FullName != nil {
			v, ok := cleanText(*in.FullName, 2, 150)
			if !ok {
				det = append(det, errs.Detail{Field: "full_name", Code: "INVALID_LENGTH"})
			}
			full = &v
		}
		rel, desc, auth := b.Relationship.Type, "", b.AuthorityBasis
		if b.Relationship.Description != nil {
			desc = *b.Relationship.Description
		}
		relChanged := false
		if in.Relationship != nil {
			rel, desc, relChanged = in.Relationship.Type, strings.TrimSpace(in.Relationship.Description), true
			if !Relationships[rel] || (rel == "SELF") != (b.BeneficiaryType == "SELF") || (rel == "OTHER" && len(desc) < 3) {
				det = append(det, errs.Detail{Field: "relationship.type", Code: "INVALID_VALUE"})
			}
		}
		if in.AuthorityBasis != nil {
			auth, relChanged = *in.AuthorityBasis, true
			if !Authorities[auth] || (auth == "NOT_REQUIRED") != (b.BeneficiaryType == "SELF") {
				det = append(det, errs.Detail{Field: "authority_basis", Code: "INVALID_VALUE"})
			}
		}
		if len(det) > 0 {
			return httpx.Validation(det...)
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.beneficiaries SET display_name = $2, full_name = $3, relationship_type = $4,
			relationship_description = $5, authority_basis = $6 WHERE id = $1 RETURNING version`, id, display, full, rel, nullable(desc), auth).Scan(&version); err != nil {
			return err
		}
		if relChanged {
			if err := s.relationshipRow(ctx, tx, id, rel, desc, auth, actorID); err != nil {
				return err
			}
		}
		if err := s.event(ctx, tx, id, version, "UPDATED", b.status, b.status, ""); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "beneficiary.updated", TargetType: "beneficiary", TargetID: id})
	})
}

// LockEditable locks the beneficiary (in tx) and checks the actor may add evidence now.
func (s *Service) LockEditable(ctx context.Context, tx pgx.Tx, actorID, id string) error {
	b, err := s.load(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if acc, err := s.access(ctx, b, actorID); err != nil {
		return err
	} else if acc != "edit" {
		return ErrNotFound
	}
	if !verifcase.Editable(b.status) {
		return errNotEdit
	}
	return nil
}

// CanView reports whether actorID may see the beneficiary's documents (owner side).
func (s *Service) CanView(ctx context.Context, actorID, id string) (bool, error) {
	b, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return false, err
	}
	acc, err := s.access(ctx, b, actorID)
	return acc != "", err
}

// WithTx exposes a transaction on the module pool (document attachment holds the subject lock across the
// kyc write).
func (s *Service) WithTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return s.tx(ctx, fn)
}

// Submit submits the beneficiary for verification.
func (s *Service) Submit(ctx context.Context, actorID, id string) error {
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if acc, err := s.access(ctx, b, actorID); err != nil {
			return err
		} else if acc != "edit" {
			return ErrNotFound
		}
		if b.status == verifcase.Submitted {
			return nil
		}
		if !verifcase.Editable(b.status) {
			return errNotEdit
		}
		det, err := s.KYC.RequirementsCheck(ctx, "beneficiary_id", id, pol.BeneficiaryRule(b.BeneficiaryType).RequiredDocuments)
		if err != nil {
			return err
		}
		if len(det) > 0 {
			e := errs.New(errs.Unprocessable, "SUBMISSION_INCOMPLETE", "Some evidence is missing or not ready.")
			e.Details = det
			return e
		}
		if b.status == verifcase.AdditionalInfoRequired {
			if _, err := tx.Exec(ctx, `UPDATE app.beneficiary_information_requests SET responded_at = $2 WHERE beneficiary_id = $1 AND responded_at IS NULL`,
				id, s.now()); err != nil {
				return err
			}
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.beneficiaries SET status = 'SUBMITTED', submitted_at = $2, policy_version = $3 WHERE id = $1 RETURNING version`,
			id, s.now(), pol.Version).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "SUBMITTED", b.status, verifcase.Submitted, ""); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "beneficiary.verification.submitted", TargetType: "beneficiary", TargetID: id}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "beneficiaries.verification_submitted", b, nil)
	})
}

// Withdraw withdraws the verification (before a decision).
func (s *Service) Withdraw(ctx context.Context, actorID, id string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if acc, err := s.access(ctx, b, actorID); err != nil {
			return err
		} else if acc != "edit" {
			return ErrNotFound
		}
		if !verifcase.Can(b.status, verifcase.Withdrawn) {
			return errNotAllowed
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.beneficiaries SET status = 'WITHDRAWN', closed_at = $2, assigned_to = NULL, assigned_at = NULL
			WHERE id = $1 RETURNING version`, id, s.now()).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "WITHDRAWN", b.status, verifcase.Withdrawn, ""); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "beneficiary.verification.withdrawn", TargetType: "beneficiary", TargetID: id})
	})
}

// AssignedTo returns the assigned reviewer (nil when unassigned).
func (b Beneficiary) AssignedTo() *string { return b.assignedTo }

// Status returns the verification status.
func (b Beneficiary) Status() string { return b.status }
