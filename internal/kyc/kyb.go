package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
)

type kybRow struct {
	ID, KYBOrgID, OrganisationID, OrgType, TargetLevel, Status, RiskLevel, PolicyVersion, CreatedBy string
	SubmittedBy, AssignedTo, PendingOutcome, PendingDecidedBy, PendingReason                        *string
	RegisteredName, TradingName, RegistrationNumber, Registry, Country                              *string
	AddressCT                                                                                       []byte
	RequiresSecond                                                                                  bool
	Version                                                                                         int
	SubmittedAt, DecidedAt, ExpiresAt                                                               *time.Time
	CreatedAt, UpdatedAt                                                                            time.Time
}

const kybSelect = `SELECT c.id, c.kyb_organisation_id, o.organisation_id, o.org_type, c.target_level, c.status, c.risk_level, c.policy_version,
	c.created_by, c.submitted_by, c.assigned_to, c.pending_outcome, c.pending_decided_by, c.pending_reason_code, c.registered_name,
	c.trading_name, c.registration_number, c.registry, c.country_of_registration, c.registered_address_ciphertext, c.requires_second_approval,
	c.version, c.submitted_at, c.decided_at, c.expires_at, c.created_at, c.updated_at
	FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id`

func scanKYB(row pgx.Row) (kybRow, error) {
	var k kybRow
	err := row.Scan(&k.ID, &k.KYBOrgID, &k.OrganisationID, &k.OrgType, &k.TargetLevel, &k.Status, &k.RiskLevel, &k.PolicyVersion, &k.CreatedBy,
		&k.SubmittedBy, &k.AssignedTo, &k.PendingOutcome, &k.PendingDecidedBy, &k.PendingReason, &k.RegisteredName, &k.TradingName,
		&k.RegistrationNumber, &k.Registry, &k.Country, &k.AddressCT, &k.RequiresSecond, &k.Version, &k.SubmittedAt, &k.DecidedAt,
		&k.ExpiresAt, &k.CreatedAt, &k.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, ErrCaseNotFound
	}
	return k, err
}

func (s *Service) loadKYB(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (kybRow, error) {
	if !ids.Valid(id) {
		return kybRow{}, ErrCaseNotFound
	}
	sql := kybSelect + ` WHERE c.id = $1`
	if lock {
		sql += ` FOR UPDATE OF c`
	}
	return scanKYB(q.QueryRow(ctx, sql, id))
}

// ensureKYBOrg returns the KYB record for an organisation, creating it on first use.
func (s *Service) ensureKYBOrg(ctx context.Context, tx pgx.Tx, orgID, orgType, policyVersion string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM kyc.kyb_organisations WHERE organisation_id = $1`, orgID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id = ids.New()
	if _, err := tx.Exec(ctx, `SAVEPOINT ensure_kyb`); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kyc.kyb_organisations (id, organisation_id, org_type, policy_version) VALUES ($1, $2, $3, $4)`,
		id, orgID, orgType, policyVersion)
	if err == nil {
		err = s.profileEvent(ctx, tx, "", id, 1, "", OrgLevelUnverified, "", ProfileActive, "PROFILE_CREATED", "", "")
	}
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT ensure_kyb`)
		if isUnique(err) {
			return id, tx.QueryRow(ctx, `SELECT id FROM kyc.kyb_organisations WHERE organisation_id = $1`, orgID).Scan(&id)
		}
		return "", err
	}
	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT ensure_kyb`)
	return id, err
}

// OrganisationLevel returns an organisation's KYB level and status (ORG_UNVERIFIED when none).
func (s *Service) OrganisationLevel(ctx context.Context, orgID string) (Status, error) {
	st := Status{Level: OrgLevelUnverified, Status: ProfileActive, RiskLevel: RiskStandard}
	err := s.Pool.QueryRow(ctx, `SELECT level, status, risk_level, level_expires_at FROM kyc.kyb_organisations WHERE organisation_id = $1`, orgID).
		Scan(&st.Level, &st.Status, &st.RiskLevel, &st.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err == nil && st.ExpiresAt != nil && !st.ExpiresAt.After(s.now()) {
		st.Level = OrgLevelUnverified
	}
	return st, err
}

// CreateKYB starts a KYB case (the caller has been authorised as ORG_ADMIN with a verified email).
func (s *Service) CreateKYB(ctx context.Context, orgID, orgType, userID string) (string, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	id := ids.New()
	rule := pol.KYBRule(orgType)
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		kybOrg, err := s.ensureKYBOrg(ctx, tx, orgID, orgType, pol.Version)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT create_kyb`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kyc.kyb_cases (id, kyb_organisation_id, target_level, status, created_by, requires_second_approval, policy_version)
			VALUES ($1, $2, 'ORG_KYB_VERIFIED', 'DRAFT', $3, $4, $5)`, id, kybOrg, userID, rule.SecondApproval, pol.Version)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT create_kyb`)
			if isUnique(err) {
				return ErrCaseAlreadyOpen
			}
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, id, 1, "CREATED", "", verifcase.Draft, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.case.created", TargetType: "kyb_case", TargetID: id,
			Metadata: map[string]any{"organisation_id": orgID, "policy_version": pol.Version}})
		return err
	})
	return id, err
}

// CurrentKYBCaseID returns the organisation's open (or latest) KYB case id.
func (s *Service) CurrentKYBCaseID(ctx context.Context, orgID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT c.id FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id
		WHERE o.organisation_id = $1 ORDER BY (c.status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED')) DESC,
		c.created_at DESC LIMIT 1`, orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// KYBDetailsPatch is a partial update of the organisation's legal details.
type KYBDetailsPatch struct {
	RegisteredName        *string  `json:"registered_name"`
	TradingName           *string  `json:"trading_name"`
	RegistrationNumber    *string  `json:"registration_number"`
	Registry              *string  `json:"registry"`
	CountryOfRegistration *string  `json:"country_of_registration"`
	RegisteredAddress     *Address `json:"registered_address"`
}

var registries = map[string]bool{"COMPANIES_REGISTRY": true, "PVO_REGISTRAR": true, "HIGH_COURT": true, "DEEDS_REGISTRY": true,
	"MINISTRY_EDUCATION": true, "HEALTH_REGISTRY": true, "OTHER": true}

// openKYBForOrg loads and locks the organisation's open KYB case.
func (s *Service) openKYBForOrg(ctx context.Context, tx pgx.Tx, orgID string) (kybRow, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT c.id FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id
		WHERE o.organisation_id = $1 AND c.status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED')`, orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return kybRow{}, ErrCaseNotFound
	}
	if err != nil {
		return kybRow{}, err
	}
	return s.loadKYB(ctx, tx, id, true)
}

// UpdateKYB edits the open case's details (ORG_ADMIN authorised by the caller).
func (s *Service) UpdateKYB(ctx context.Context, orgID string, p KYBDetailsPatch, expectVersion int) error {
	var det []errs.Detail
	text := func(field string, v *string, min, max int) any {
		if v == nil {
			return nil
		}
		t := strings.Join(strings.Fields(*v), " ")
		if n := utf8.RuneCountInString(t); n < min || n > max || strings.ContainsAny(t, "<>{}") {
			det = append(det, errs.Detail{Field: field, Code: "INVALID_FORMAT"})
			return nil
		}
		return t
	}
	name := text("registered_name", p.RegisteredName, 2, 200)
	trading := text("trading_name", p.TradingName, 0, 200)
	regNo := text("registration_number", p.RegistrationNumber, 2, 64)
	var registry, country any
	if p.Registry != nil {
		if !registries[*p.Registry] {
			det = append(det, errs.Detail{Field: "registry", Code: "INVALID_VALUE"})
		} else {
			registry = *p.Registry
		}
	}
	if p.CountryOfRegistration != nil {
		c := strings.ToUpper(*p.CountryOfRegistration)
		if !countryRe.MatchString(c) {
			det = append(det, errs.Detail{Field: "country_of_registration", Code: "INVALID_FORMAT"})
		} else {
			country = c
		}
	}
	if p.RegisteredAddress != nil {
		a := *p.RegisteredAddress
		a.Country = strings.ToUpper(a.Country)
		if len(strings.TrimSpace(a.Line1)) < 3 || len(strings.TrimSpace(a.City)) < 2 || !countryRe.MatchString(a.Country) {
			det = append(det, errs.Detail{Field: "registered_address", Code: "INVALID_FORMAT"})
		}
	}
	if len(det) > 0 {
		return httpx.Validation(det...)
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		k, err := s.openKYBForOrg(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if !verifcase.Editable(k.Status) {
			return ErrCaseNotEditable
		}
		if expectVersion > 0 && expectVersion != k.Version {
			return ErrCaseStateChanged
		}
		var addrCT any
		var keyID any
		if p.RegisteredAddress != nil {
			a := *p.RegisteredAddress
			a.Country = strings.ToUpper(a.Country)
			b, _ := json.Marshal(a)
			ct, err := s.AEAD.Seal(b, aad("kyb_cases", k.ID, "registered_address"))
			if err != nil {
				return err
			}
			addrCT, keyID = ct, s.AEAD.KeyID()
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_cases SET registered_name = coalesce($2, registered_name),
			trading_name = CASE WHEN $3::text IS NULL THEN trading_name ELSE nullif($3, '') END,
			registration_number = coalesce($4, registration_number), registry = coalesce($5, registry),
			country_of_registration = coalesce($6, country_of_registration),
			registered_address_ciphertext = coalesce($7, registered_address_ciphertext), registered_address_key_id = coalesce($8, registered_address_key_id),
			version = version + 1 WHERE id = $1`, k.ID, name, trading, regNo, registry, country, addrCT, keyID); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, k.ID, k.Version+1, "UPDATED", k.Status, k.Status, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.case.updated", TargetType: "kyb_case", TargetID: k.ID})
		return err
	})
}

// PersonInput declares a director, trustee, beneficial owner or other connected person.
type PersonInput struct {
	FullName         string   `json:"full_name"`
	Roles            []string `json:"roles"`
	OwnershipBP      *int     `json:"ownership_bp"`
	DateOfBirth      string   `json:"date_of_birth"`
	Nationality      string   `json:"nationality"`
	IDDocumentType   string   `json:"id_document_type"`
	IDDocumentNumber string   `json:"id_document_number"`
}

var personRoles = map[string]bool{"DIRECTOR": true, "TRUSTEE": true, "OFFICE_BEARER": true, "BENEFICIAL_OWNER": true, "CONTROLLER": true,
	"REPRESENTATIVE": true}

// AddPerson adds a person to the open (editable) KYB case. Identity details are encrypted as a KYB_PERSON
// identity (no duplicate-identity uniqueness: a director may also be an account holder).
func (s *Service) AddPerson(ctx context.Context, orgID string, in PersonInput) (string, error) {
	var det []errs.Detail
	name := strings.Join(strings.Fields(in.FullName), " ")
	if n := utf8.RuneCountInString(name); n < 2 || n > 150 {
		det = append(det, errs.Detail{Field: "full_name", Code: "INVALID_LENGTH"})
	}
	if len(in.Roles) == 0 {
		det = append(det, errs.Detail{Field: "roles", Code: "MISSING_FIELD"})
	}
	for _, r := range in.Roles {
		if !personRoles[r] {
			det = append(det, errs.Detail{Field: "roles", Code: "INVALID_VALUE"})
		}
	}
	if in.OwnershipBP != nil && (*in.OwnershipBP < 0 || *in.OwnershipBP > 10000) {
		det = append(det, errs.Detail{Field: "ownership_bp", Code: "OUT_OF_RANGE"})
	}
	if in.DateOfBirth != "" {
		if _, err := time.Parse("2006-01-02", in.DateOfBirth); err != nil {
			det = append(det, errs.Detail{Field: "date_of_birth", Code: "INVALID_FORMAT"})
		}
	}
	nat := strings.ToUpper(in.Nationality)
	if nat != "" && !countryRe.MatchString(nat) {
		det = append(det, errs.Detail{Field: "nationality", Code: "INVALID_FORMAT"})
	}
	if (in.IDDocumentType == "") != (in.IDDocumentNumber == "") || (in.IDDocumentType != "" && !idTypes[in.IDDocumentType]) {
		det = append(det, errs.Detail{Field: "id_document_type", Code: "INVALID_VALUE"})
	}
	if in.IDDocumentNumber != "" && !idNumberRe.MatchString(strings.ToUpper(strings.TrimSpace(in.IDDocumentNumber))) {
		det = append(det, errs.Detail{Field: "id_document_number", Code: "INVALID_FORMAT"})
	}
	if len(det) > 0 {
		return "", httpx.Validation(det...)
	}
	personID := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		k, err := s.openKYBForOrg(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if !verifcase.Editable(k.Status) {
			return ErrCaseNotEditable
		}
		identityID := ids.New()
		nameJSON, _ := json.Marshal(map[string]string{"full": name})
		nameCT, err := s.AEAD.Seal(nameJSON, aad("identities", identityID, "legal_name"))
		if err != nil {
			return err
		}
		var dobCT, dobKey, idType, numCT, numKey, bidx, bidxKey, masked any
		if in.DateOfBirth != "" {
			ct, err := s.AEAD.Seal([]byte(in.DateOfBirth), aad("identities", identityID, "dob"))
			if err != nil {
				return err
			}
			dobCT, dobKey = ct, s.AEAD.KeyID()
		}
		if in.IDDocumentNumber != "" {
			norm := NormalizeIDNumber(in.IDDocumentNumber)
			ct, err := s.AEAD.Seal([]byte(norm), aad("identities", identityID, "id_number"))
			if err != nil {
				return err
			}
			idType, numCT, numKey = in.IDDocumentType, ct, s.AEAD.KeyID()
			bidx, bidxKey, masked = s.Keyed.Sum("kyc_id_number", in.IDDocumentType, norm), s.Keyed.KeyID(), MaskIDNumber(norm)
		}
		var natArg any
		if nat != "" {
			natArg = nat
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.identities (id, subject_kind, status, legal_name_ciphertext, legal_name_key_id, dob_ciphertext,
			dob_key_id, nationality, id_type, id_number_ciphertext, id_number_key_id, id_number_bidx, bidx_key_id, id_number_masked, source)
			VALUES ($1, 'KYB_PERSON', 'PENDING', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'OWNER_DECLARATION')`,
			identityID, nameCT, s.AEAD.KeyID(), dobCT, dobKey, natArg, idType, numCT, numKey, bidx, bidxKey, masked); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.organisation_persons (id, kyb_organisation_id, kyb_case_id, identity_id, roles, ownership_bp, valid_from)
			VALUES ($1, $2, $3, $4, $5, $6, current_date)`, personID, k.KYBOrgID, k.ID, identityID, in.Roles, in.OwnershipBP); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_cases SET version = version + 1 WHERE id = $1`, k.ID); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, k.ID, k.Version+1, "UPDATED", k.Status, k.Status, "", map[string]any{"person_added": personID}); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.person.added", TargetType: "kyb_case", TargetID: k.ID,
			Metadata: map[string]any{"person_id": personID, "roles": in.Roles}})
		return err
	})
	return personID, err
}

// RemovePerson ends a person's declaration on the open editable case.
func (s *Service) RemovePerson(ctx context.Context, orgID, personID string) error {
	if !ids.Valid(personID) {
		return errs.New(errs.NotFound, "PERSON_NOT_FOUND", "No such person.")
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		k, err := s.openKYBForOrg(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if !verifcase.Editable(k.Status) {
			return ErrCaseNotEditable
		}
		tag, err := tx.Exec(ctx, `UPDATE kyc.organisation_persons SET valid_to = current_date, ended_reason = 'REMOVED_BY_REPRESENTATIVE'
			WHERE id = $1 AND kyb_case_id = $2 AND valid_to IS NULL`, personID, k.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "PERSON_NOT_FOUND", "No such person.")
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_cases SET version = version + 1 WHERE id = $1`, k.ID); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, k.ID, k.Version+1, "UPDATED", k.Status, k.Status, "", map[string]any{"person_removed": personID}); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.person.removed", TargetType: "kyb_case", TargetID: k.ID,
			Metadata: map[string]any{"person_id": personID}})
		return err
	})
}

// SubmitKYB submits the open case. repLevel is the submitting representative's current KYC level (looked up
// by the caller through Level); the policy decides the minimum (a verified individual does not make the
// organisation verified, but an unverified representative cannot submit).
func (s *Service) SubmitKYB(ctx context.Context, orgID, userID string) error {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	rep, err := s.Level(ctx, userID)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		k, err := s.openKYBForOrg(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if k.Status == verifcase.Submitted {
			return nil
		}
		if !verifcase.Editable(k.Status) {
			return ErrCaseNotEditable
		}
		rule := pol.KYBRule(k.OrgType)
		var det []errs.Detail
		if rule.RepresentativeMinLevel != "" && (!AtLeast(rep.Level, rule.RepresentativeMinLevel) || rep.Status != ProfileActive) {
			e := errs.New(errs.Unprocessable, "REPRESENTATIVE_NOT_VERIFIED", "Complete your own identity verification before submitting for the organisation.")
			return e
		}
		if k.RegisteredName == nil {
			det = append(det, errs.Detail{Field: "registered_name", Code: "MISSING_FIELD"})
		}
		if k.RegistrationNumber == nil {
			det = append(det, errs.Detail{Field: "registration_number", Code: "MISSING_FIELD"})
		}
		if k.Country == nil {
			det = append(det, errs.Detail{Field: "country_of_registration", Code: "MISSING_FIELD"})
		}
		if k.AddressCT == nil {
			det = append(det, errs.Detail{Field: "registered_address", Code: "MISSING_FIELD"})
		}
		var persons int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM kyc.organisation_persons WHERE kyb_case_id = $1 AND valid_to IS NULL`, k.ID).Scan(&persons); err != nil {
			return err
		}
		if persons < max(rule.MinPersons, 1) {
			det = append(det, errs.Detail{Field: "persons", Code: "MISSING_FIELD"})
		}
		docDet, err := s.requirementsMet(ctx, tx, "kyb_case_id", k.ID, rule.RequiredDocuments)
		if err != nil {
			return err
		}
		det = append(det, docDet...)
		if len(det) > 0 {
			e := errs.New(errs.Unprocessable, "SUBMISSION_INCOMPLETE", "Some information or documents are missing or not ready.")
			e.Details = det
			return e
		}
		evType := "SUBMITTED"
		if k.Status == verifcase.AdditionalInfoRequired {
			evType = "RESUBMITTED"
			if _, err := tx.Exec(ctx, `UPDATE kyc.information_requests SET responded_at = $2 WHERE kyb_case_id = $1 AND responded_at IS NULL`,
				k.ID, s.now()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_cases SET status = 'SUBMITTED', submitted_by = $2, submitted_at = $3, requires_second_approval = $4,
			policy_version = $5, version = version + 1 WHERE id = $1`, k.ID, userID, s.now(), k.RequiresSecond || rule.SecondApproval, pol.Version); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, k.ID, k.Version+1, evType, k.Status, verifcase.Submitted, "", nil); err != nil {
			return err
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.case.submitted", TargetType: "kyb_case", TargetID: k.ID,
			Metadata: map[string]any{"organisation_id": orgID, "policy_version": pol.Version}}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "kyc.case_submitted", "kyb_case", k.ID, map[string]any{"case_id": k.ID, "kind": KindKYB,
			"subject_type": "ORGANISATION", "subject_id": orgID})
	})
}

// WithdrawKYB withdraws the open case.
func (s *Service) WithdrawKYB(ctx context.Context, orgID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		k, err := s.openKYBForOrg(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if !verifcase.Can(k.Status, verifcase.Withdrawn) {
			return ErrActionNotAllowed
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_cases SET status = 'WITHDRAWN', closed_at = $2, assigned_to = NULL, assigned_at = NULL,
			version = version + 1 WHERE id = $1`, k.ID, s.now()); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYB, k.ID, k.Version+1, "WITHDRAWN", k.Status, verifcase.Withdrawn, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyb.case.withdrawn", TargetType: "kyb_case", TargetID: k.ID})
		return err
	})
}
