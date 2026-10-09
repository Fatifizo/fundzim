package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Address is a residential or registered address (C3: stored encrypted only).
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	Province   string `json:"province,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country"`
}

// IdentityDraft is what the subject enters (encrypted on the case until submission).
type IdentityDraft struct {
	LegalFirstName     string   `json:"legal_first_name,omitempty"`
	LegalLastName      string   `json:"legal_last_name,omitempty"`
	DateOfBirth        string   `json:"date_of_birth,omitempty"` // YYYY-MM-DD
	Nationality        string   `json:"nationality,omitempty"`
	CountryOfResidence string   `json:"country_of_residence,omitempty"`
	IDDocumentType     string   `json:"id_document_type,omitempty"`
	IDDocumentNumber   string   `json:"id_document_number,omitempty"`
	IDDocumentExpiry   string   `json:"id_document_expiry,omitempty"`
	ResidentialAddress *Address `json:"residential_address,omitempty"`
}

// IdentityPatch is a partial update (nil = unchanged).
type IdentityPatch struct {
	LegalFirstName     *string  `json:"legal_first_name"`
	LegalLastName      *string  `json:"legal_last_name"`
	DateOfBirth        *string  `json:"date_of_birth"`
	Nationality        *string  `json:"nationality"`
	CountryOfResidence *string  `json:"country_of_residence"`
	IDDocumentType     *string  `json:"id_document_type"`
	IDDocumentNumber   *string  `json:"id_document_number"`
	IDDocumentExpiry   *string  `json:"id_document_expiry"`
	ResidentialAddress *Address `json:"residential_address"`
}

var (
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	// Format sanity only (letters, digits, separators). National ID and passport structure rules are NOT
	// hard-coded as legal requirements (brief §4); the reviewer checks the document.
	idNumberRe = regexp.MustCompile(`^[A-Z0-9][A-Z0-9 /-]{3,28}[A-Z0-9]$`)
	idTypes    = map[string]bool{"ZW_NATIONAL_ID": true, "ZW_PASSPORT": true, "FOREIGN_PASSPORT": true}
)

func cleanName(v string) string { return strings.Join(strings.Fields(v), " ") }

// NormalizeIDNumber upper-cases and strips separators (the blind index is computed over this form).
func NormalizeIDNumber(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	return strings.NewReplacer(" ", "", "-", "", "/", "").Replace(v)
}

// MaskIDNumber keeps the last two characters (C2 display form).
func MaskIDNumber(v string) string {
	n := NormalizeIDNumber(v)
	if len(n) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(n)-2) + n[len(n)-2:]
}

func (p IdentityPatch) apply(d *IdentityDraft) []errs.Detail {
	var det []errs.Detail
	name := func(field string, in *string, out *string) {
		if in == nil {
			return
		}
		v := cleanName(*in)
		if n := utf8.RuneCountInString(v); n < 1 || n > 100 || strings.ContainsAny(v, "<>{}") {
			det = append(det, errs.Detail{Field: field, Code: "INVALID_FORMAT"})
			return
		}
		*out = v
	}
	name("legal_first_name", p.LegalFirstName, &d.LegalFirstName)
	name("legal_last_name", p.LegalLastName, &d.LegalLastName)
	date := func(field string, in *string, out *string, allowEmpty bool) {
		if in == nil {
			return
		}
		if *in == "" && allowEmpty {
			*out = ""
			return
		}
		if _, err := time.Parse("2006-01-02", *in); err != nil {
			det = append(det, errs.Detail{Field: field, Code: "INVALID_FORMAT"})
			return
		}
		*out = *in
	}
	date("date_of_birth", p.DateOfBirth, &d.DateOfBirth, false)
	date("id_document_expiry", p.IDDocumentExpiry, &d.IDDocumentExpiry, true)
	country := func(field string, in *string, out *string) {
		if in == nil {
			return
		}
		v := strings.ToUpper(strings.TrimSpace(*in))
		if !countryRe.MatchString(v) {
			det = append(det, errs.Detail{Field: field, Code: "INVALID_FORMAT"})
			return
		}
		*out = v
	}
	country("nationality", p.Nationality, &d.Nationality)
	country("country_of_residence", p.CountryOfResidence, &d.CountryOfResidence)
	if p.IDDocumentType != nil {
		if !idTypes[*p.IDDocumentType] {
			det = append(det, errs.Detail{Field: "id_document_type", Code: "INVALID_VALUE"})
		} else {
			d.IDDocumentType = *p.IDDocumentType
		}
	}
	if p.IDDocumentNumber != nil {
		v := strings.ToUpper(strings.TrimSpace(*p.IDDocumentNumber))
		if !idNumberRe.MatchString(v) {
			det = append(det, errs.Detail{Field: "id_document_number", Code: "INVALID_FORMAT"})
		} else {
			d.IDDocumentNumber = v
		}
	}
	if p.ResidentialAddress != nil {
		a := *p.ResidentialAddress
		a.Country = strings.ToUpper(strings.TrimSpace(a.Country))
		if len(strings.TrimSpace(a.Line1)) < 3 || len(strings.TrimSpace(a.City)) < 2 || !countryRe.MatchString(a.Country) ||
			len(a.Line1)+len(a.Line2)+len(a.City)+len(a.Province)+len(a.PostalCode) > 400 {
			det = append(det, errs.Detail{Field: "residential_address", Code: "INVALID_FORMAT"})
		} else {
			d.ResidentialAddress = &a
		}
	}
	return det
}

// completeness checks a draft before submission (now injected for age/expiry).
func (d IdentityDraft) completeness(now time.Time, minAge int) []errs.Detail {
	var det []errs.Detail
	req := func(field, v string) {
		if v == "" {
			det = append(det, errs.Detail{Field: field, Code: "MISSING_FIELD"})
		}
	}
	req("legal_first_name", d.LegalFirstName)
	req("legal_last_name", d.LegalLastName)
	req("date_of_birth", d.DateOfBirth)
	req("nationality", d.Nationality)
	req("country_of_residence", d.CountryOfResidence)
	req("id_document_type", d.IDDocumentType)
	req("id_document_number", d.IDDocumentNumber)
	if d.ResidentialAddress == nil {
		det = append(det, errs.Detail{Field: "residential_address", Code: "MISSING_FIELD"})
	}
	if dob, err := time.Parse("2006-01-02", d.DateOfBirth); err == nil {
		if dob.After(now) {
			det = append(det, errs.Detail{Field: "date_of_birth", Code: "INVALID_FORMAT"})
		} else if dob.AddDate(minAge, 0, 0).After(now) {
			// account holders must be adults (kyc-architecture §3.1, LR-021); minors are beneficiaries via a guardian
			det = append(det, errs.Detail{Field: "date_of_birth", Code: "UNDERAGE"})
		}
	}
	if d.IDDocumentExpiry != "" {
		if exp, err := time.Parse("2006-01-02", d.IDDocumentExpiry); err == nil && !exp.After(now) {
			det = append(det, errs.Detail{Field: "id_document_expiry", Code: "DOCUMENT_EXPIRED"})
		}
	}
	return det
}

// ---- profiles --------------------------------------------------------------------------------------------

// ensureProfile returns the user's profile id, creating it (UNVERIFIED, ACTIVE) on first use.
func (s *Service) ensureProfile(ctx context.Context, tx pgx.Tx, userID, policyVersion string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM kyc.verification_profiles WHERE user_id = $1`, userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id = ids.New()
	if _, err := tx.Exec(ctx, `SAVEPOINT ensure_profile`); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO kyc.verification_profiles (id, user_id, policy_version) VALUES ($1, $2, $3)`, id, userID, policyVersion)
	if err == nil {
		err = s.profileEvent(ctx, tx, id, "", 1, "", LevelUnverified, "", ProfileActive, "PROFILE_CREATED", "", "")
	}
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT ensure_profile`)
		if isUnique(err) { // created concurrently
			return id, tx.QueryRow(ctx, `SELECT id FROM kyc.verification_profiles WHERE user_id = $1`, userID).Scan(&id)
		}
		return "", err
	}
	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT ensure_profile`)
	return id, err
}

// Status is the subject's verification overview.
type Status struct {
	Level     string     `json:"level"`
	Status    string     `json:"status"`
	RiskLevel string     `json:"risk_level"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// Level returns the user's current level and status (UNVERIFIED/ACTIVE when no profile exists). An expired
// level counts as UNVERIFIED (the sweep records the downgrade; this read never trusts a stale level).
func (s *Service) Level(ctx context.Context, userID string) (Status, error) {
	st := Status{Level: LevelUnverified, Status: ProfileActive, RiskLevel: RiskStandard}
	err := s.Pool.QueryRow(ctx, `SELECT level, status, risk_level, level_expires_at FROM kyc.verification_profiles WHERE user_id = $1`, userID).
		Scan(&st.Level, &st.Status, &st.RiskLevel, &st.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err == nil && st.ExpiresAt != nil && !st.ExpiresAt.After(s.now()) {
		st.Level = LevelUnverified
	}
	return st, err
}

// ---- KYC cases (subject side) ---------------------------------------------------------------------------

type caseRow struct {
	ID, ProfileID, UserID, TargetLevel, Status, RiskLevel, PolicyVersion    string
	IdentityID, AssignedTo, PendingOutcome, PendingDecidedBy, PendingReason *string
	DraftCT                                                                 []byte
	RequiresSecond                                                          bool
	Version                                                                 int
	SubmittedAt, DecidedAt, ExpiresAt                                       *time.Time
	CreatedAt, UpdatedAt                                                    time.Time
}

const kycCaseSelect = `SELECT c.id, c.profile_id, p.user_id, c.target_level, c.status, c.risk_level, c.policy_version, c.identity_id,
	c.assigned_to, c.pending_outcome, c.pending_decided_by, c.pending_reason_code, c.draft_ciphertext, c.requires_second_approval,
	c.version, c.submitted_at, c.decided_at, c.expires_at, c.created_at, c.updated_at
	FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id`

func scanCase(row pgx.Row) (caseRow, error) {
	var c caseRow
	err := row.Scan(&c.ID, &c.ProfileID, &c.UserID, &c.TargetLevel, &c.Status, &c.RiskLevel, &c.PolicyVersion, &c.IdentityID, &c.AssignedTo,
		&c.PendingOutcome, &c.PendingDecidedBy, &c.PendingReason, &c.DraftCT, &c.RequiresSecond, &c.Version, &c.SubmittedAt, &c.DecidedAt,
		&c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrCaseNotFound
	}
	return c, err
}

func (s *Service) loadCase(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (caseRow, error) {
	if !ids.Valid(id) {
		return caseRow{}, ErrCaseNotFound
	}
	sql := kycCaseSelect + ` WHERE c.id = $1`
	if lock {
		sql += ` FOR UPDATE OF c`
	}
	return scanCase(q.QueryRow(ctx, sql, id))
}

func (s *Service) draft(c caseRow) (IdentityDraft, error) {
	var d IdentityDraft
	if c.DraftCT == nil {
		return d, nil
	}
	pt, err := s.AEAD.Open(c.DraftCT, aad("kyc_cases", c.ID, "draft"))
	if err != nil {
		return d, err
	}
	return d, json.Unmarshal(pt, &d)
}

// CreateCase starts a KYC case for the user (verified email and phone required: basic contactability, not
// identity). Concurrent starts collide on the one-open-case index.
func (s *Service) CreateCase(ctx context.Context, userID, targetLevel string) (string, error) {
	if targetLevel != LevelIdentity {
		return "", httpx.Validation(errs.Detail{Field: "target_level", Code: "INVALID_VALUE"})
	}
	acct, err := users.ByID(ctx, s.AppPool, userID)
	if err != nil {
		return "", err
	}
	var det []errs.Detail
	if !acct.EmailVerified {
		det = append(det, errs.Detail{Field: "email", Code: "EMAIL_NOT_VERIFIED"})
	}
	if _, err := users.PrimaryPhone(ctx, s.AppPool, userID); errors.Is(err, users.ErrNotFound) {
		det = append(det, errs.Detail{Field: "phone", Code: "PHONE_NOT_VERIFIED"})
	} else if err != nil {
		return "", err
	}
	if len(det) > 0 {
		e := errs.New(errs.Unprocessable, "PREREQUISITES_NOT_MET", "Verify your email address and phone number first.")
		e.Details = det
		return "", e
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		profileID, err := s.ensureProfile(ctx, tx, userID, pol.Version)
		if err != nil {
			return err
		}
		var risk string
		if err := tx.QueryRow(ctx, `SELECT risk_level FROM kyc.verification_profiles WHERE id = $1`, profileID).Scan(&risk); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT create_case`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO kyc.kyc_cases (id, profile_id, target_level, status, risk_level, requires_second_approval, policy_version)
			VALUES ($1, $2, $3, 'DRAFT', $4, $5, $6)`, id, profileID, targetLevel, risk, pol.KYCRule(risk).SecondApproval, pol.Version)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT create_case`)
			if isUnique(err) {
				return ErrCaseAlreadyOpen
			}
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYC, id, 1, "CREATED", "", verifcase.Draft, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.case.created", TargetType: "kyc_case", TargetID: id,
			Metadata: map[string]any{"target_level": targetLevel, "policy_version": pol.Version}})
		return err
	})
	return id, err
}

// CurrentCase returns the user's open case, or the most recent one.
func (s *Service) CurrentCaseID(ctx context.Context, userID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT c.id FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
		WHERE p.user_id = $1 ORDER BY (c.status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED')) DESC,
		c.created_at DESC LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ownCase loads and locks a case owned by userID (another user's case is reported as not found: no IDOR).
func (s *Service) ownCase(ctx context.Context, tx pgx.Tx, userID, caseID string) (caseRow, error) {
	c, err := s.loadCase(ctx, tx, caseID, true)
	if err != nil {
		return c, err
	}
	if c.UserID != userID {
		return caseRow{}, ErrCaseNotFound
	}
	return c, nil
}

// UpdateDraft applies a partial identity update while the case is editable. expectVersion (optional) gives
// optimistic concurrency (If-Match).
func (s *Service) UpdateDraft(ctx context.Context, userID, caseID string, patch IdentityPatch, expectVersion int) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.ownCase(ctx, tx, userID, caseID)
		if err != nil {
			return err
		}
		if !verifcase.Editable(c.Status) {
			return ErrCaseNotEditable
		}
		if expectVersion > 0 && expectVersion != c.Version {
			return ErrCaseStateChanged
		}
		d, err := s.draft(c)
		if err != nil {
			return err
		}
		if det := patch.apply(&d); len(det) > 0 {
			return httpx.Validation(det...)
		}
		pt, _ := json.Marshal(d)
		ct, err := s.AEAD.Seal(pt, aad("kyc_cases", c.ID, "draft"))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyc_cases SET draft_ciphertext = $2, draft_key_id = $3, version = version + 1 WHERE id = $1`,
			c.ID, ct, s.AEAD.KeyID()); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYC, c.ID, c.Version+1, "UPDATED", c.Status, c.Status, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.case.updated", TargetType: "kyc_case", TargetID: c.ID})
		return err
	})
}

// Submit validates and submits a case for review. Re-submitting an already SUBMITTED case is a no-op
// (idempotent retry).
func (s *Service) Submit(ctx context.Context, userID, caseID string) error {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.ownCase(ctx, tx, userID, caseID)
		if err != nil {
			return err
		}
		if c.Status == verifcase.Submitted {
			return nil
		}
		if !verifcase.Editable(c.Status) {
			return ErrCaseNotEditable
		}
		d, err := s.draft(c)
		if err != nil {
			return err
		}
		det := d.completeness(s.now(), pol.Rules.AdultAge)
		rule := pol.KYCRule(c.RiskLevel)
		docDet, err := s.requirementsMet(ctx, tx, "kyc_case_id", c.ID, rule.RequiredDocuments)
		if err != nil {
			return err
		}
		det = append(det, docDet...)
		if len(det) > 0 {
			e := errs.New(errs.Unprocessable, "SUBMISSION_INCOMPLETE", "Some information or documents are missing or not ready.")
			e.Details = det
			return e
		}
		identityID, dupes, err := s.freezeIdentity(ctx, tx, c, d)
		if err != nil {
			return err
		}
		requiresSecond := c.RequiresSecond || rule.SecondApproval
		risk := c.RiskLevel
		if dupes > 0 { // a matching ID number on another account: never auto-reject, route to enhanced review
			requiresSecond = true
			if risk == RiskLow || risk == RiskStandard {
				risk = RiskEnhanced
			}
		}
		evType := "SUBMITTED"
		if c.Status == verifcase.AdditionalInfoRequired {
			evType = "RESUBMITTED"
			if _, err := tx.Exec(ctx, `UPDATE kyc.information_requests SET responded_at = $2 WHERE kyc_case_id = $1 AND responded_at IS NULL`,
				c.ID, s.now()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyc_cases SET status = 'SUBMITTED', identity_id = $2, submitted_at = $3, risk_level = $4,
			requires_second_approval = $5, policy_version = $6, version = version + 1 WHERE id = $1`,
			c.ID, identityID, s.now(), risk, requiresSecond, pol.Version); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, KindKYC, c.ID, c.Version+1, evType, c.Status, verifcase.Submitted, "",
			map[string]any{"duplicate_identity_matches": dupes}); err != nil {
			return err
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.case.submitted", TargetType: "kyc_case", TargetID: c.ID,
			Metadata: map[string]any{"policy_version": pol.Version, "risk_level": risk}}); err != nil {
			return err
		}
		if dupes > 0 {
			if _, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.duplicate_identity.detected",
				TargetType: "kyc_case", TargetID: c.ID, Metadata: map[string]any{"matches": dupes}}); err != nil {
				return err
			}
			if err := s.emit(ctx, tx, "kyc.duplicate_identity_detected", "kyc_case", c.ID, map[string]any{"case_id": c.ID,
				"profile_id": c.ProfileID, "subject_type": "USER", "subject_id": c.UserID, "other_profile_count": dupes}); err != nil {
				return err
			}
		}
		return s.emit(ctx, tx, "kyc.case_submitted", "kyc_case", c.ID, map[string]any{"case_id": c.ID, "kind": KindKYC,
			"subject_type": "USER", "subject_id": c.UserID})
	})
}

// freezeIdentity encrypts the draft into a PENDING identity row (replacing an earlier pending one of the
// case) and counts ACTIVE identities of OTHER profiles with the same document number (blind index).
func (s *Service) freezeIdentity(ctx context.Context, tx pgx.Tx, c caseRow, d IdentityDraft) (string, int, error) {
	id := ids.New()
	seal := func(field string, v []byte) ([]byte, error) { return s.AEAD.Seal(v, aad("identities", id, field)) }
	nameJSON, _ := json.Marshal(map[string]string{"first": d.LegalFirstName, "last": d.LegalLastName})
	nameCT, err := seal("legal_name", nameJSON)
	if err != nil {
		return "", 0, err
	}
	dobCT, err := seal("dob", []byte(d.DateOfBirth))
	if err != nil {
		return "", 0, err
	}
	addrJSON, _ := json.Marshal(d.ResidentialAddress)
	addrCT, err := seal("address", addrJSON)
	if err != nil {
		return "", 0, err
	}
	norm := NormalizeIDNumber(d.IDDocumentNumber)
	numCT, err := seal("id_number", []byte(norm))
	if err != nil {
		return "", 0, err
	}
	bidx := s.Keyed.Sum("kyc_id_number", d.IDDocumentType, norm)
	var expiry any
	if d.IDDocumentExpiry != "" {
		expiry = d.IDDocumentExpiry
	}
	if c.IdentityID != nil {
		if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'SUPERSEDED' WHERE id = $1 AND status = 'PENDING'`, *c.IdentityID); err != nil {
			return "", 0, err
		}
	}
	var supersedes any
	if c.IdentityID != nil {
		supersedes = *c.IdentityID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.identities (id, subject_kind, profile_id, status, legal_name_ciphertext, legal_name_key_id,
		dob_ciphertext, dob_key_id, nationality, residence_country, id_type, id_number_ciphertext, id_number_key_id, id_number_bidx, bidx_key_id,
		id_number_masked, doc_expiry, address_ciphertext, address_key_id, source, supersedes_id)
		VALUES ($1, 'ACCOUNT_HOLDER', $2, 'PENDING', $3, $4, $5, $4, $6, $7, $8, $9, $4, $10, $11, $12, $13, $14, $4, 'USER_ENTRY', $15)`,
		id, c.ProfileID, nameCT, s.AEAD.KeyID(), dobCT, d.Nationality, d.CountryOfResidence, d.IDDocumentType, numCT, bidx, s.Keyed.KeyID(),
		MaskIDNumber(norm), expiry, addrCT, supersedes); err != nil {
		return "", 0, err
	}
	var dupes int
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT profile_id) FROM kyc.identities WHERE id_type = $1 AND id_number_bidx = $2
		AND status IN ('ACTIVE','PENDING') AND subject_kind = 'ACCOUNT_HOLDER' AND profile_id <> $3`, d.IDDocumentType, bidx, c.ProfileID).Scan(&dupes); err != nil {
		return "", 0, err
	}
	return id, dupes, nil
}

// Withdraw withdraws an open case before a decision.
func (s *Service) Withdraw(ctx context.Context, userID, caseID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.ownCase(ctx, tx, userID, caseID)
		if err != nil {
			return err
		}
		if !verifcase.Can(c.Status, verifcase.Withdrawn) {
			return ErrActionNotAllowed
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyc_cases SET status = 'WITHDRAWN', closed_at = $2, assigned_to = NULL, assigned_at = NULL,
			version = version + 1 WHERE id = $1`, c.ID, s.now()); err != nil {
			return err
		}
		if c.IdentityID != nil {
			if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'INACTIVE' WHERE id = $1 AND status = 'PENDING'`, *c.IdentityID); err != nil {
				return err
			}
		}
		if err := s.caseEvent(ctx, tx, KindKYC, c.ID, c.Version+1, "WITHDRAWN", c.Status, verifcase.Withdrawn, "", nil); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.case.withdrawn", TargetType: "kyc_case", TargetID: c.ID})
		return err
	})
}

// BeneficiaryIdentity stores a beneficiary's name and (optional) date of birth as an encrypted BENEFICIARY
// identity and returns its id (the beneficiaries module holds only this reference).
func (s *Service) BeneficiaryIdentity(ctx context.Context, fullName, dob string) (string, error) {
	id := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		nameJSON, _ := json.Marshal(map[string]string{"full": fullName})
		nameCT, err := s.AEAD.Seal(nameJSON, aad("identities", id, "legal_name"))
		if err != nil {
			return err
		}
		var dobCT, dobKey any
		if dob != "" {
			ct, err := s.AEAD.Seal([]byte(dob), aad("identities", id, "dob"))
			if err != nil {
				return err
			}
			dobCT, dobKey = ct, s.AEAD.KeyID()
		}
		_, err = tx.Exec(ctx, `INSERT INTO kyc.identities (id, subject_kind, status, legal_name_ciphertext, legal_name_key_id, dob_ciphertext, dob_key_id, source)
			VALUES ($1, 'BENEFICIARY', 'PENDING', $2, $3, $4, $5, 'OWNER_DECLARATION')`, id, nameCT, s.AEAD.KeyID(), dobCT, dobKey)
		return err
	})
	return id, err
}

// BeneficiaryDOB returns the decrypted date of birth of a BENEFICIARY identity ("" when none).
func (s *Service) BeneficiaryDOB(ctx context.Context, identityID string) (string, error) {
	var ct []byte
	if err := s.Pool.QueryRow(ctx, `SELECT dob_ciphertext FROM kyc.identities WHERE id = $1 AND subject_kind = 'BENEFICIARY'`, identityID).Scan(&ct); err != nil {
		return "", err
	}
	if ct == nil {
		return "", nil
	}
	pt, err := s.AEAD.Open(ct, aad("identities", identityID, "dob"))
	return string(pt), err
}
