package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
)

// IdentityView is the identity shown to the subject (their own data) or a reviewer. The full ID number is
// never included (masked only; reviewers use the audited reveal endpoint).
type IdentityView struct {
	LegalFirstName         string   `json:"legal_first_name"`
	LegalLastName          string   `json:"legal_last_name"`
	DateOfBirth            string   `json:"date_of_birth"`
	Nationality            string   `json:"nationality"`
	CountryOfResidence     string   `json:"country_of_residence"`
	IDDocumentType         string   `json:"id_document_type"`
	IDDocumentNumberMasked string   `json:"id_document_number_masked"`
	IDDocumentExpiry       string   `json:"id_document_expiry"`
	ResidentialAddress     *Address `json:"residential_address"`
}

// InformationRequest is shown to subjects and reviewers.
type InformationRequest struct {
	ID          string     `json:"id"`
	Message     string     `json:"message"`
	Items       []string   `json:"items"`
	RequestedAt time.Time  `json:"requested_at"`
	RespondedAt *time.Time `json:"responded_at"`
}

// DecisionView is the latest decision as shown to the subject (no internal notes or reviewer identity).
type DecisionView struct {
	Outcome    string    `json:"outcome"`
	ReasonCode string    `json:"reason_code"`
	Message    string    `json:"message"`
	DecidedAt  time.Time `json:"decided_at"`
}

// CaseView is the subject's view of a KYC case (contract §7.2 KycCase).
type CaseView struct {
	ID                  string               `json:"id"`
	Kind                string               `json:"kind"`
	Status              string               `json:"status"`
	TargetLevel         string               `json:"target_level"`
	PolicyVersion       string               `json:"policy_version"`
	Identity            *IdentityView        `json:"identity"`
	Documents           []Document           `json:"documents"`
	Requirements        []RequirementStatus  `json:"requirements"`
	InformationRequests []InformationRequest `json:"information_requests"`
	Decision            *DecisionView        `json:"decision"`
	SubmittedAt         *time.Time           `json:"submitted_at"`
	ExpiresAt           *time.Time           `json:"expires_at"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	Version             int                  `json:"version"`
}

func viewFromDraft(d IdentityDraft) *IdentityView {
	v := &IdentityView{LegalFirstName: d.LegalFirstName, LegalLastName: d.LegalLastName, DateOfBirth: d.DateOfBirth, Nationality: d.Nationality,
		CountryOfResidence: d.CountryOfResidence, IDDocumentType: d.IDDocumentType, IDDocumentExpiry: d.IDDocumentExpiry,
		ResidentialAddress: d.ResidentialAddress}
	if d.IDDocumentNumber != "" {
		v.IDDocumentNumberMasked = MaskIDNumber(d.IDDocumentNumber)
	}
	return v
}

// identityView decrypts an identity row (everything except the full number).
func (s *Service) identityView(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, identityID string) (*IdentityView, error) {
	var nameCT, dobCT, addrCT []byte
	var nat, res, typ, masked *string
	var exp *time.Time
	if err := q.QueryRow(ctx, `SELECT legal_name_ciphertext, dob_ciphertext, address_ciphertext, nationality, residence_country, id_type,
		id_number_masked, doc_expiry FROM kyc.identities WHERE id = $1`, identityID).Scan(&nameCT, &dobCT, &addrCT, &nat, &res, &typ, &masked, &exp); err != nil {
		return nil, err
	}
	v := &IdentityView{}
	if pt, err := s.AEAD.Open(nameCT, aad("identities", identityID, "legal_name")); err == nil {
		var n map[string]string
		_ = json.Unmarshal(pt, &n)
		v.LegalFirstName, v.LegalLastName = n["first"], n["last"]
		if n["full"] != "" {
			v.LegalFirstName = n["full"]
		}
	} else {
		return nil, err
	}
	if dobCT != nil {
		pt, err := s.AEAD.Open(dobCT, aad("identities", identityID, "dob"))
		if err != nil {
			return nil, err
		}
		v.DateOfBirth = string(pt)
	}
	if addrCT != nil {
		pt, err := s.AEAD.Open(addrCT, aad("identities", identityID, "address"))
		if err != nil {
			return nil, err
		}
		var a Address
		_ = json.Unmarshal(pt, &a)
		v.ResidentialAddress = &a
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	v.Nationality, v.CountryOfResidence, v.IDDocumentType, v.IDDocumentNumberMasked = deref(nat), deref(res), deref(typ), deref(masked)
	if exp != nil {
		v.IDDocumentExpiry = exp.Format("2006-01-02")
	}
	return v, nil
}

func (s *Service) infoRequests(ctx context.Context, col, id string) ([]InformationRequest, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, message, items, requested_at, responded_at FROM kyc.information_requests WHERE `+col+` = $1
		ORDER BY requested_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InformationRequest{}
	for rows.Next() {
		var r InformationRequest
		if err := rows.Scan(&r.ID, &r.Message, &r.Items, &r.RequestedAt, &r.RespondedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var userMessages = map[string]string{"APPROVE_WITH_CONDITIONS": "Your verification was approved.", "APPROVE": "Your verification was approved.",
	"SUSPEND": "Your verification is suspended pending review.", "REVOKE": "Your verification was revoked.", "EXPIRE": "Your verification has expired."}

func (s *Service) latestDecision(ctx context.Context, col, id string) (*DecisionView, error) {
	var d DecisionView
	var msg *string
	err := s.Pool.QueryRow(ctx, `SELECT outcome, reason_code, user_message, decided_at FROM kyc.kyc_decisions WHERE `+col+` = $1
		AND outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REJECT','SUSPEND','REVOKE','EXPIRE') ORDER BY decided_at DESC LIMIT 1`, id).
		Scan(&d.Outcome, &d.ReasonCode, &msg, &d.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if msg != nil {
		d.Message = *msg
	} else {
		d.Message = userMessages[d.Outcome]
	}
	if d.Outcome == "APPROVE_WITH_CONDITIONS" { // the subject sees "approved"; conditions are internal
		d.Outcome = "APPROVE"
	}
	return &d, nil
}

// CaseView builds the subject's (or reviewer's) view of a KYC case.
func (s *Service) CaseView(ctx context.Context, caseID string) (CaseView, error) {
	c, err := s.loadCase(ctx, s.Pool, caseID, false)
	if err != nil {
		return CaseView{}, err
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return CaseView{}, err
	}
	v := CaseView{ID: c.ID, Kind: KindKYC, Status: c.Status, TargetLevel: c.TargetLevel, PolicyVersion: c.PolicyVersion, SubmittedAt: c.SubmittedAt,
		ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version}
	if verifcase.Editable(c.Status) || c.IdentityID == nil {
		d, err := s.draft(c)
		if err != nil {
			return CaseView{}, err
		}
		v.Identity = viewFromDraft(d)
	} else if v.Identity, err = s.identityView(ctx, s.Pool, *c.IdentityID); err != nil {
		return CaseView{}, err
	}
	if v.Documents, err = s.DocumentsFor(ctx, "kyc_case_id", c.ID); err != nil {
		return CaseView{}, err
	}
	if v.Requirements, err = s.Requirements(ctx, "kyc_case_id", c.ID, pol.KYCRule(c.RiskLevel).RequiredDocuments); err != nil {
		return CaseView{}, err
	}
	if v.InformationRequests, err = s.infoRequests(ctx, "kyc_case_id", c.ID); err != nil {
		return CaseView{}, err
	}
	v.Decision, err = s.latestDecision(ctx, "kyc_case_id", c.ID)
	return v, err
}

// CaseOwner returns the user id owning a KYC case.
func (s *Service) CaseOwner(ctx context.Context, caseID string) (string, error) {
	c, err := s.loadCase(ctx, s.Pool, caseID, false)
	return c.UserID, err
}

// ---- KYB views ---------------------------------------------------------------------------------------------

// KYBPerson is a declared person (ID number masked).
type KYBPerson struct {
	ID                     string   `json:"id"`
	FullName               string   `json:"full_name"`
	Roles                  []string `json:"roles"`
	OwnershipBP            *int     `json:"ownership_bp"`
	IDDocumentType         *string  `json:"id_document_type"`
	IDDocumentNumberMasked *string  `json:"id_document_number_masked"`
}

// KYBDetails are the organisation's declared legal details.
type KYBDetails struct {
	RegisteredName        *string  `json:"registered_name"`
	TradingName           *string  `json:"trading_name"`
	RegistrationNumber    *string  `json:"registration_number"`
	Registry              *string  `json:"registry"`
	CountryOfRegistration *string  `json:"country_of_registration"`
	RegisteredAddress     *Address `json:"registered_address"`
}

// KYBCaseView is the organisation's view of a KYB case (contract §7.2 KybCase).
type KYBCaseView struct {
	ID                  string               `json:"id"`
	Kind                string               `json:"kind"`
	OrganisationID      string               `json:"organisation_id"`
	Status              string               `json:"status"`
	TargetLevel         string               `json:"target_level"`
	PolicyVersion       string               `json:"policy_version"`
	Details             KYBDetails           `json:"details"`
	Persons             []KYBPerson          `json:"persons"`
	Documents           []Document           `json:"documents"`
	Requirements        []RequirementStatus  `json:"requirements"`
	InformationRequests []InformationRequest `json:"information_requests"`
	Decision            *DecisionView        `json:"decision"`
	SubmittedAt         *time.Time           `json:"submitted_at"`
	ExpiresAt           *time.Time           `json:"expires_at"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	Version             int                  `json:"version"`
}

// KYBCaseView builds the view; full=false (ORG_MEMBER) omits persons, documents and address.
func (s *Service) KYBCaseView(ctx context.Context, caseID string, full bool) (KYBCaseView, error) {
	k, err := s.loadKYB(ctx, s.Pool, caseID, false)
	if err != nil {
		return KYBCaseView{}, err
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return KYBCaseView{}, err
	}
	v := KYBCaseView{ID: k.ID, Kind: KindKYB, OrganisationID: k.OrganisationID, Status: k.Status, TargetLevel: k.TargetLevel,
		PolicyVersion: k.PolicyVersion, Details: KYBDetails{RegisteredName: k.RegisteredName, TradingName: k.TradingName,
			RegistrationNumber: k.RegistrationNumber, Registry: k.Registry, CountryOfRegistration: k.Country},
		SubmittedAt: k.SubmittedAt, ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt, Version: k.Version,
		Persons: []KYBPerson{}, Documents: []Document{}, Requirements: []RequirementStatus{}}
	if v.InformationRequests, err = s.infoRequests(ctx, "kyb_case_id", k.ID); err != nil {
		return v, err
	}
	if v.Decision, err = s.latestDecision(ctx, "kyb_case_id", k.ID); err != nil {
		return v, err
	}
	if !full {
		return v, nil
	}
	if k.AddressCT != nil {
		pt, err := s.AEAD.Open(k.AddressCT, aad("kyb_cases", k.ID, "registered_address"))
		if err != nil {
			return v, err
		}
		var a Address
		_ = json.Unmarshal(pt, &a)
		v.Details.RegisteredAddress = &a
	}
	if v.Persons, err = s.persons(ctx, k.ID); err != nil {
		return v, err
	}
	if v.Documents, err = s.DocumentsFor(ctx, "kyb_case_id", k.ID); err != nil {
		return v, err
	}
	v.Requirements, err = s.Requirements(ctx, "kyb_case_id", k.ID, pol.KYBRule(k.OrgType).RequiredDocuments)
	return v, err
}

func (s *Service) persons(ctx context.Context, kybCaseID string) ([]KYBPerson, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id, p.roles, p.ownership_bp, i.id, i.legal_name_ciphertext, i.id_type, i.id_number_masked
		FROM kyc.organisation_persons p JOIN kyc.identities i ON i.id = p.identity_id WHERE p.kyb_case_id = $1 AND p.valid_to IS NULL
		ORDER BY p.created_at`, kybCaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KYBPerson{}
	for rows.Next() {
		var p KYBPerson
		var identityID string
		var ct []byte
		if err := rows.Scan(&p.ID, &p.Roles, &p.OwnershipBP, &identityID, &ct, &p.IDDocumentType, &p.IDDocumentNumberMasked); err != nil {
			return nil, err
		}
		pt, err := s.AEAD.Open(ct, aad("identities", identityID, "legal_name"))
		if err != nil {
			return nil, err
		}
		var n map[string]string
		_ = json.Unmarshal(pt, &n)
		p.FullName = n["full"]
		out = append(out, p)
	}
	return out, rows.Err()
}

// KYBCaseOrganisation returns the organisation id of a KYB case.
func (s *Service) KYBCaseOrganisation(ctx context.Context, caseID string) (string, error) {
	k, err := s.loadKYB(ctx, s.Pool, caseID, false)
	return k.OrganisationID, err
}

// ---- reviewer queue and detail -----------------------------------------------------------------------------

// QueueItem is a reviewer queue row (no C3).
type QueueItem struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	RiskLevel   string     `json:"risk_level"`
	AssignedTo  *string    `json:"assigned_to"`
	SubmittedAt *time.Time `json:"submitted_at"`
	FourEyes    bool       `json:"requires_second_approval"`
	Pending     bool       `json:"awaiting_second_approval"`
}

// QueueFilter selects queue rows.
type QueueFilter struct {
	Statuses []string
	Assigned string // me | unassigned | any
	ActorID  string
	Limit    int
	Before   *time.Time // keyset cursor: (submitted_at, id) strictly after (Before, AfterID)
	AfterID  string
}

// CursorID is the id half of the keyset cursor (the zero UUID when absent).
func (f QueueFilter) CursorID() string {
	if f.AfterID == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return f.AfterID
}

// Queue lists KYC (kind=KYC) or KYB (kind=KYB) cases for review, oldest first.
func (s *Service) Queue(ctx context.Context, kind string, f QueueFilter) ([]QueueItem, error) {
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = []string{verifcase.Submitted, verifcase.UnderReview, verifcase.Escalated}
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var sql string
	if kind == KindKYB {
		sql = `SELECT c.id, 'KYB', c.status, 'ORGANISATION', o.organisation_id, c.risk_level, c.assigned_to, c.submitted_at, c.requires_second_approval,
			c.pending_outcome IS NOT NULL FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id`
	} else {
		sql = `SELECT c.id, 'KYC', c.status, 'USER', p.user_id, c.risk_level, c.assigned_to, c.submitted_at, c.requires_second_approval,
			c.pending_outcome IS NOT NULL FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id`
	}
	sql += ` WHERE c.status = ANY($1) AND ($2 = 'any' OR ($2 = 'me' AND c.assigned_to = $3::uuid) OR ($2 = 'unassigned' AND c.assigned_to IS NULL))
		AND ($4::timestamptz IS NULL OR (c.submitted_at, c.id) > ($4, $6::uuid)) ORDER BY c.submitted_at NULLS LAST, c.id LIMIT $5`
	assigned := f.Assigned
	if assigned == "" {
		assigned = "any"
	}
	var actor any
	if f.ActorID != "" {
		actor = f.ActorID
	}
	rows, err := s.Pool.Query(ctx, sql, statuses, assigned, actor, f.Before, limit, f.CursorID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var q QueueItem
		if err := rows.Scan(&q.ID, &q.Type, &q.Status, &q.SubjectType, &q.SubjectID, &q.RiskLevel, &q.AssignedTo, &q.SubmittedAt, &q.FourEyes, &q.Pending); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// HistoryItem is a timeline row.
type HistoryItem struct {
	EventType  string         `json:"event_type"`
	FromStatus *string        `json:"from_status"`
	ToStatus   string         `json:"to_status"`
	ActorType  string         `json:"actor_type"`
	ActorID    *string        `json:"actor_id"`
	ReasonCode *string        `json:"reason_code"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// History returns a case's timeline.
func (s *Service) History(ctx context.Context, kind, caseID string) ([]HistoryItem, error) {
	col := "kyc_case_id"
	if kind == KindKYB {
		col = "kyb_case_id"
	}
	rows, err := s.Pool.Query(ctx, `SELECT event_type, from_status, to_status, actor_type, actor_id, reason_code, payload, occurred_at
		FROM kyc.case_events WHERE `+col+` = $1 ORDER BY case_version`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var h HistoryItem
		if err := rows.Scan(&h.EventType, &h.FromStatus, &h.ToStatus, &h.ActorType, &h.ActorID, &h.ReasonCode, &h.Payload, &h.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Check is a recorded check result.
type Check struct {
	Code       string    `json:"code"`
	Result     string    `json:"result"`
	ReasonCode *string   `json:"reason_code"`
	CreatedAt  time.Time `json:"created_at"`
}

// Checks returns a case's check results.
func (s *Service) Checks(ctx context.Context, kind, caseID string) ([]Check, error) {
	table, col := "kyc.kyc_checks", "kyc_case_id"
	if kind == KindKYB {
		table, col = "kyc.kyb_checks", "kyb_case_id"
	}
	rows, err := s.Pool.Query(ctx, `SELECT check_code, result, reason_code, created_at FROM `+table+` WHERE `+col+` = $1 ORDER BY created_at`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.Code, &c.Result, &c.ReasonCode, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReviewSubject returns the subject (user or organisation) and assignment of a case for reviewer views.
func (s *Service) ReviewSubject(ctx context.Context, kind, caseID string) (subjectType, subjectID string, err error) {
	if kind == KindKYB {
		id, err := s.KYBCaseOrganisation(ctx, caseID)
		return "ORGANISATION", id, err
	}
	id, err := s.CaseOwner(ctx, caseID)
	return "USER", id, err
}

// RevealIDNumber decrypts the full ID number of a KYC case's identity for an authorised reviewer, with a
// mandatory justification and a security audit event (kyc.identity_number.reveal, step-up).
func (s *Service) RevealIDNumber(ctx context.Context, caseID, actorID, justification string) (string, error) {
	justification = strings.TrimSpace(justification)
	if len(justification) < 10 || len(justification) > 1000 {
		return "", httpx.Validation(errs.Detail{Field: "justification", Code: "INVALID_LENGTH"})
	}
	actors, err := s.ActorIdentities(ctx, actorID) // a staff account's linked personal account counts too
	if err != nil {
		return "", err
	}
	var number string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.loadCase(ctx, tx, caseID, false)
		if err != nil {
			return err
		}
		for _, a := range actors {
			if c.UserID == a {
				return ErrSelfDecision
			}
		}
		if c.IdentityID == nil {
			return errs.New(errs.Conflict, "NO_IDENTITY_SUBMITTED", "No identity has been submitted on this case.")
		}
		var ct []byte
		if err := tx.QueryRow(ctx, `SELECT id_number_ciphertext FROM kyc.identities WHERE id = $1`, *c.IdentityID).Scan(&ct); err != nil {
			return err
		}
		pt, err := s.AEAD.Open(ct, aad("identities", *c.IdentityID, "id_number"))
		if err != nil {
			return err
		}
		number = string(pt)
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.identity_number.revealed", TargetType: "kyc_case",
			TargetID: c.ID, Justification: justification})
		return err
	})
	return number, err
}

// ---- lifecycle sweeps (worker) ------------------------------------------------------------------------------

// SweepResult counts what a sweep changed.
type SweepResult struct{ Expired, InfoExpired int }

// Sweep expires APPROVED cases past expires_at (level drops) and closes ADDITIONAL_INFORMATION_REQUIRED cases
// with no response within the policy period. Stale verification never stays trusted (brief §19).
func (s *Service) Sweep(ctx context.Context, limit int) (SweepResult, error) {
	var res SweepResult
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return res, err
	}
	infoCutoff := s.now().AddDate(0, 0, -pol.Rules.InformationRequestExpiryDays)
	for _, kind := range []string{KindKYC, KindKYB} {
		table := map[string]string{KindKYC: "kyc.kyc_cases", KindKYB: "kyc.kyb_cases"}[kind]
		rows, err := s.Pool.Query(ctx, `SELECT id, status FROM `+table+` WHERE (status = 'APPROVED' AND expires_at <= $1)
			OR (status = 'ADDITIONAL_INFORMATION_REQUIRED' AND updated_at <= $2) LIMIT $3`, s.now(), infoCutoff, limit)
		if err != nil {
			return res, err
		}
		type item struct{ id, status string }
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.status); err != nil {
				rows.Close()
				return res, err
			}
			items = append(items, it)
		}
		rows.Close()
		for _, it := range items {
			if err := s.expireOne(ctx, kind, it.id); err != nil {
				return res, err
			}
			if it.status == verifcase.Approved {
				res.Expired++
			} else {
				res.InfoExpired++
			}
		}
	}
	return res, nil
}

func (s *Service) expireOne(ctx context.Context, kind, id string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		now := s.now()
		if c.status == verifcase.Approved {
			var exp *time.Time
			if err := tx.QueryRow(ctx, `SELECT expires_at FROM `+c.table()+` WHERE id = $1`, c.id).Scan(&exp); err != nil {
				return err
			}
			if exp == nil || exp.After(now) {
				return nil // changed concurrently
			}
		} else if c.status != verifcase.AdditionalInfoRequired {
			return nil
		}
		wasApproved := c.status == verifcase.Approved
		if err := s.transition(ctx, tx, c, verifcase.Expired, "EXPIRED", "VERIFICATION_EXPIRED", "closed_at = $3, assigned_to = NULL, assigned_at = NULL", now); err != nil {
			return err
		}
		if !wasApproved && c.identityID != nil {
			if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'INACTIVE' WHERE id = $1 AND status = 'PENDING'`, *c.identityID); err != nil {
				return err
			}
		}
		if wasApproved {
			lvl := LevelUnverified
			if kind == KindKYB {
				lvl = OrgLevelUnverified
			}
			if c.identityID != nil {
				if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'INACTIVE' WHERE id = $1 AND status = 'ACTIVE'`, *c.identityID); err != nil {
					return err
				}
			}
			if err := s.setSubject(ctx, tx, c, lvl, ProfileActive, "VERIFICATION_EXPIRED", nil); err != nil {
				return err
			}
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: c.actionPrefix() + ".case.expired",
			TargetType: c.targetType(), TargetID: c.id, Metadata: map[string]any{"was_approved": wasApproved}}); err != nil {
			return err
		}
		st, sid := c.subject()
		return s.emit(ctx, tx, "kyc.verification_expired", c.targetType(), c.id, map[string]any{"case_id": c.id, "kind": c.kind,
			"subject_type": st, "subject_id": sid})
	})
}

// ---- payout-verified upgrade (consumer of payouts.destination_verified) -------------------------------------

// RecordPayoutOwnership records a PAYOUT_DESTINATION_OWNERSHIP PASS on the user's approved case and raises an
// IDENTITY_VERIFIED profile to PAYOUT_VERIFIED (kyc-architecture §3.1). Idempotent.
func (s *Service) RecordPayoutOwnership(ctx context.Context, userID, destinationID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var profileID, level, status, caseID string
		err := tx.QueryRow(ctx, `SELECT p.id, p.level, p.status, c.id FROM kyc.verification_profiles p
			JOIN kyc.kyc_cases c ON c.profile_id = p.id AND c.status = 'APPROVED'
			WHERE p.user_id = $1 ORDER BY c.decided_at DESC LIMIT 1 FOR UPDATE OF p`, userID).Scan(&profileID, &level, &status, &caseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not identity-verified: ownership alone grants nothing
		}
		if err != nil {
			return err
		}
		if level != LevelIdentity || status != ProfileActive {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.kyc_checks (id, kyc_case_id, check_code, result, rule_version, performed_by_type, reason_code)
			VALUES ($1, $2, 'PAYOUT_DESTINATION_OWNERSHIP', 'PASS', 'v1', 'SYSTEM', NULL)`, ids.New(), caseID); err != nil {
			return err
		}
		var exp *time.Time
		if err := tx.QueryRow(ctx, `SELECT level_expires_at FROM kyc.verification_profiles WHERE id = $1`, profileID).Scan(&exp); err != nil {
			return err
		}
		return s.setProfile(ctx, tx, profileID, LevelPayout, ProfileActive, "PAYOUT_DESTINATION_VERIFIED", caseID, exp)
	})
}
