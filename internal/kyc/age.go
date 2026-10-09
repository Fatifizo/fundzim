package kyc

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Age attestation and BASIC_VERIFIED (ADR-037 §1–§2).
//
// An attestation is a self-declaration "I am at least adult_age years old" (ATTESTED) or the refusal to make it
// (DECLINED). It is never documentary proof of age: "verified age" exists only when the active, approved KYC
// identity carries a date of birth at or above adult_age (assurance DOCUMENT_VERIFIED). A verified date of
// birth below adult_age, or a later DECLINED row, overrides any attestation. adult_age is INTERNAL_POLICY
// configuration from the verification policy (LR-021 / LR-015 OPEN — LEGAL_REVIEW_REQUIRED).

// Attestation outcomes, sources and assurance levels.
const (
	AgeAttested = "ATTESTED"
	AgeDeclined = "DECLINED"

	AgeSourceRegistration = "REGISTRATION"
	AgeSourceDashboard    = "DASHBOARD"
	AgeSourceCampaignFlow = "CAMPAIGN_FLOW"

	AssuranceNone             = "NONE"
	AssuranceSelfAttested     = "SELF_ATTESTED"
	AssuranceDocumentVerified = "DOCUMENT_VERIFIED"

	// EvAgeAttested is emitted for every recorded attestation (ids and codes only).
	EvAgeAttested = "kyc.age_attested"
)

// Unmet BASIC_VERIFIED conditions reported by EvaluateBasic (stable codes, usable as eligibility reasons).
const (
	BasicUnmetEmail       = "EMAIL_NOT_VERIFIED"
	BasicUnmetPhone       = "PHONE_NOT_VERIFIED"
	BasicUnmetAttestation = "AGE_ATTESTATION_REQUIRED"
	BasicUnmetAge         = "AGE_NOT_ELIGIBLE"
	BasicUnmetAccount     = "ACCOUNT_NOT_ACTIVE"
	BasicUnmetPolicy      = "BASIC_POLICY_UNAVAILABLE"
)

var ageSources = map[string]bool{AgeSourceRegistration: true, AgeSourceDashboard: true, AgeSourceCampaignFlow: true}

// AgeAttestation is one recorded attestation.
type AgeAttestation struct {
	ID               string    `json:"id"`
	Outcome          string    `json:"outcome"`
	StatementVersion string    `json:"statement_version"`
	AdultAge         int       `json:"adult_age"`
	PolicyVersion    string    `json:"policy_version"`
	Source           string    `json:"source"`
	Assurance        string    `json:"assurance"`
	AttestedAt       time.Time `json:"attested_at"`
}

// AgeStatus is the user's age position under the current policy.
type AgeStatus struct {
	AdultAge                 int             `json:"adult_age"`
	StatementVersion         string          `json:"statement_version"` // the statement currently presented
	Attestation              *AgeAttestation `json:"attestation"`       // latest, nil when none
	Assurance                string          `json:"assurance"`         // NONE | SELF_ATTESTED | DOCUMENT_VERIFIED
	VerifiedDOBBelowAdultAge bool            `json:"verified_dob_below_adult_age"`
}

// BasicResult reports an EvaluateBasic run.
type BasicResult struct {
	Level   string   `json:"level"`   // the profile level after the run (UNVERIFIED when no profile exists)
	Met     bool     `json:"met"`     // every BASIC_VERIFIED condition holds
	Unmet   []string `json:"unmet"`   // codes of the conditions that do not hold
	Changed bool     `json:"changed"` // BASIC_VERIFIED was granted or withdrawn by this run
}

var errAgeUnavailable = errs.New(errs.Unavailable, "AGE_ATTESTATION_UNAVAILABLE",
	"Age confirmation is temporarily unavailable. Please retry shortly.")

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// latestAttestation returns the user's newest attestation (nil when none).
func latestAttestation(ctx context.Context, q querier, userID string) (*AgeAttestation, error) {
	var a AgeAttestation
	err := q.QueryRow(ctx, `SELECT id, outcome, statement_version, adult_age, policy_version, source, assurance, attested_at
		FROM kyc.age_attestations WHERE user_id = $1 ORDER BY attested_at DESC, id DESC LIMIT 1`, userID).
		Scan(&a.ID, &a.Outcome, &a.StatementVersion, &a.AdultAge, &a.PolicyVersion, &a.Source, &a.Assurance, &a.AttestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// verifiedDOB returns the date of birth of the user's ACTIVE (approved) identity, if any. The value is used for
// the comparison only and never leaves this package.
func (s *Service) verifiedDOB(ctx context.Context, q querier, userID string) (time.Time, bool, error) {
	var id string
	var ct []byte
	err := q.QueryRow(ctx, `SELECT i.id, i.dob_ciphertext FROM kyc.identities i JOIN kyc.verification_profiles p ON p.id = i.profile_id
		WHERE p.user_id = $1 AND i.status = 'ACTIVE' AND i.dob_ciphertext IS NOT NULL LIMIT 1`, userID).Scan(&id, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	pt, err := s.AEAD.Open(ct, aad("identities", id, "dob"))
	if err != nil {
		return time.Time{}, false, err
	}
	dob, err := time.Parse("2006-01-02", string(pt))
	if err != nil {
		return time.Time{}, false, nil // unreadable dates never count as verified
	}
	return dob, true, nil
}

// ageStatus computes the status with q (pool or transaction).
func (s *Service) ageStatus(ctx context.Context, q querier, pol Policy, userID string) (AgeStatus, error) {
	st := AgeStatus{AdultAge: pol.Rules.AdultAge, Assurance: AssuranceNone}
	if pol.Rules.Basic != nil {
		st.StatementVersion = pol.Rules.Basic.AgeStatementVersion
	}
	var err error
	if st.Attestation, err = latestAttestation(ctx, q, userID); err != nil {
		return st, err
	}
	dob, ok, err := s.verifiedDOB(ctx, q, userID)
	if err != nil {
		return st, err
	}
	switch {
	case ok && dob.AddDate(pol.Rules.AdultAge, 0, 0).After(s.now()):
		st.VerifiedDOBBelowAdultAge = true
	case ok:
		st.Assurance = AssuranceDocumentVerified
	case st.Attestation != nil && st.Attestation.Outcome == AgeAttested && st.Attestation.AdultAge >= pol.Rules.AdultAge:
		st.Assurance = AssuranceSelfAttested
	}
	return st, nil
}

// AgeStatus returns the user's age position: the current adult_age and statement version, the latest
// attestation, the assurance (NONE | SELF_ATTESTED | DOCUMENT_VERIFIED) and whether a verified date of birth is
// below adult_age.
func (s *Service) AgeStatus(ctx context.Context, userID string) (AgeStatus, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return AgeStatus{}, err
	}
	return s.ageStatus(ctx, s.Pool, pol, userID)
}

// RecordAgeAttestation appends an attestation for a personal account (outcome ATTESTED | DECLINED; source
// REGISTRATION | DASHBOARD | CAMPAIGN_FLOW; statementVersion must be the current policy's) and re-evaluates
// BASIC_VERIFIED in the same transaction. ip is the client address ("" when unknown).
func (s *Service) RecordAgeAttestation(ctx context.Context, userID, outcome, source, statementVersion, ip string) (AgeAttestation, error) {
	return s.recordAttestation(ctx, userID, outcome, source, statementVersion, ip, "", time.Time{})
}

// RecordRegistrationAttestation records the attestation given on the registration form, carried by the
// outbox event eventID (identity.registration_age_attested). Idempotent per event: a duplicate delivery is a
// no-op. at is the time of registration.
func (s *Service) RecordRegistrationAttestation(ctx context.Context, eventID, userID string, at time.Time) error {
	if !ids.Valid(eventID) || !ids.Valid(userID) {
		return errors.New("kyc: registration attestation needs an event id and a user id")
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	if pol.Rules.Basic == nil {
		return errAgeUnavailable
	}
	_, err = s.recordAttestation(ctx, userID, AgeAttested, AgeSourceRegistration, pol.Rules.Basic.AgeStatementVersion, "", eventID, at)
	if errors.Is(err, errDuplicateAttestation) {
		return nil
	}
	return err
}

var errDuplicateAttestation = errors.New("kyc: registration attestation already recorded")

func (s *Service) recordAttestation(ctx context.Context, userID, outcome, source, statementVersion, ip, eventID string,
	at time.Time) (AgeAttestation, error) {
	var det []errs.Detail
	if outcome != AgeAttested && outcome != AgeDeclined {
		det = append(det, errs.Detail{Field: "outcome", Code: "INVALID_VALUE"})
	}
	if !ageSources[source] {
		det = append(det, errs.Detail{Field: "source", Code: "INVALID_VALUE"})
	}
	if statementVersion == "" {
		det = append(det, errs.Detail{Field: "statement_version", Code: "REQUIRED"})
	}
	if len(det) > 0 {
		return AgeAttestation{}, httpx.Validation(det...)
	}
	var ipArg any
	if ip != "" {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			return AgeAttestation{}, httpx.Validation(errs.Detail{Field: "ip", Code: "INVALID_FORMAT"})
		}
		ipArg = a.String()
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return AgeAttestation{}, err
	}
	if pol.Rules.Basic == nil {
		return AgeAttestation{}, errAgeUnavailable
	}
	if statementVersion != pol.Rules.Basic.AgeStatementVersion {
		return AgeAttestation{}, errs.New(errs.Unprocessable, "STATEMENT_VERSION_NOT_CURRENT",
			"The age statement has changed. Reload the page and confirm the current statement.")
	}
	facts, err := s.basicFacts(ctx, userID)
	if err != nil {
		return AgeAttestation{}, err
	}
	if !facts.personal {
		return AgeAttestation{}, errs.New(errs.Forbidden, "PERMISSION_DENIED", "This action is available to personal accounts only.")
	}
	if at.IsZero() {
		at = s.now()
	}
	a := AgeAttestation{ID: ids.New(), Outcome: outcome, StatementVersion: statementVersion, AdultAge: pol.Rules.AdultAge,
		PolicyVersion: pol.Version, Source: source, Assurance: AssuranceSelfAttested, AttestedAt: at.UTC()}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var evArg any
		if eventID != "" {
			evArg = eventID
			var seen bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kyc.age_attestations WHERE source_event_id = $1)`, eventID).Scan(&seen); err != nil {
				return err
			}
			if seen {
				return errDuplicateAttestation
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.age_attestations (id, user_id, outcome, statement_version, adult_age, policy_version, source,
			assurance, ip, source_event_id, attested_at) VALUES ($1, $2, $3, $4, $5, $6, $7, 'SELF_ATTESTED', $8::inet, $9, $10)`,
			a.ID, userID, outcome, statementVersion, a.AdultAge, a.PolicyVersion, source, ipArg, evArg, a.AttestedAt); err != nil {
			if isUnique(err) {
				return errDuplicateAttestation
			}
			return err
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.age_attestation.recorded", TargetType: "user",
			TargetID: userID, Metadata: map[string]any{"attestation_id": a.ID, "outcome": outcome, "source": source,
				"statement_version": statementVersion, "adult_age": a.AdultAge, "policy_version": a.PolicyVersion}}); err != nil {
			return err
		}
		if err := s.emit(ctx, tx, EvAgeAttested, "user", userID, map[string]any{"user_id": userID, "attestation_id": a.ID,
			"outcome": outcome, "source": source, "policy_version": a.PolicyVersion}); err != nil {
			return err
		}
		_, err := s.evaluateBasicTx(ctx, tx, pol, userID, facts)
		return err
	})
	if err != nil {
		return AgeAttestation{}, err
	}
	return a, nil
}

// basicFacts are the account facts owned by the users module (read through its package with the app pool).
type basicFacts struct {
	exists, personal, active, emailVerified, phoneVerified bool
}

func (s *Service) basicFacts(ctx context.Context, userID string) (basicFacts, error) {
	var f basicFacts
	if !ids.Valid(userID) {
		return f, nil
	}
	acct, err := users.ByID(ctx, s.AppPool, userID)
	if errors.Is(err, users.ErrNotFound) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	f.exists = true
	f.personal = acct.Kind == users.KindUser && !acct.IsSystem
	f.active = acct.Status == users.StatusActive
	f.emailVerified = acct.EmailVerified
	ph, err := users.PrimaryPhone(ctx, s.AppPool, userID)
	switch {
	case errors.Is(err, users.ErrNotFound):
	case err != nil:
		return f, err
	default:
		f.phoneVerified = !ph.VerifiedAt.IsZero()
	}
	return f, nil
}

// EvaluateBasic grants or withdraws BASIC_VERIFIED for a personal account (ADR-037 §2). BASIC_VERIFIED is
// granted when the login email is verified, a phone is verified, the latest attestation is ATTESTED for an
// adult_age at least the current policy's (and no verified date of birth is below it), and the account is
// ACTIVE. It is withdrawn when any condition stops holding, only while the level is still BASIC_VERIFIED
// (higher levels have their own rules). Each change writes a profile_events row, a security audit event and
// kyc.level_changed in one transaction, like every other level change. Idempotent.
func (s *Service) EvaluateBasic(ctx context.Context, userID string) (BasicResult, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return BasicResult{}, err
	}
	facts, err := s.basicFacts(ctx, userID)
	if err != nil {
		return BasicResult{}, err
	}
	if !facts.exists || !facts.personal {
		return BasicResult{Level: LevelUnverified, Unmet: []string{BasicUnmetAccount}}, nil
	}
	var res BasicResult
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = s.evaluateBasicTx(ctx, tx, pol, userID, facts)
		return err
	})
	return res, err
}

func (s *Service) evaluateBasicTx(ctx context.Context, tx pgx.Tx, pol Policy, userID string, f basicFacts) (BasicResult, error) {
	res := BasicResult{Level: LevelUnverified, Unmet: []string{}}
	// serialise evaluations of one user (the profile may not exist yet)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('kyc-basic:' || $1::text, 0))`, userID); err != nil {
		return res, err
	}
	if pol.Rules.Basic == nil {
		res.Unmet = append(res.Unmet, BasicUnmetPolicy)
	}
	if !f.emailVerified {
		res.Unmet = append(res.Unmet, BasicUnmetEmail)
	}
	if !f.phoneVerified {
		res.Unmet = append(res.Unmet, BasicUnmetPhone)
	}
	if !f.active || !f.personal {
		res.Unmet = append(res.Unmet, BasicUnmetAccount)
	}
	age, err := s.ageStatus(ctx, tx, pol, userID)
	if err != nil {
		return res, err
	}
	switch {
	case age.VerifiedDOBBelowAdultAge:
		res.Unmet = append(res.Unmet, BasicUnmetAge)
	case age.Attestation == nil:
		res.Unmet = append(res.Unmet, BasicUnmetAttestation)
	case age.Attestation.Outcome != AgeAttested:
		res.Unmet = append(res.Unmet, BasicUnmetAge)
	case age.Attestation.AdultAge < pol.Rules.AdultAge:
		res.Unmet = append(res.Unmet, BasicUnmetAttestation) // attested under a lower adult_age: attest again
	}
	res.Met = len(res.Unmet) == 0

	var profileID, level, status string
	err = tx.QueryRow(ctx, `SELECT id, level, status FROM kyc.verification_profiles WHERE user_id = $1 FOR UPDATE`, userID).Scan(&profileID, &level, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		if !res.Met {
			return res, nil
		}
		if profileID, err = s.ensureProfile(ctx, tx, userID, pol.Version); err != nil {
			return res, err
		}
		err = tx.QueryRow(ctx, `SELECT level, status FROM kyc.verification_profiles WHERE id = $1 FOR UPDATE`, profileID).Scan(&level, &status)
	}
	if err != nil {
		return res, err
	}
	res.Level = level
	switch {
	case res.Met && level == LevelUnverified:
		if err := s.setProfile(ctx, tx, profileID, LevelBasic, status, "BASIC_CONDITIONS_MET", "", nil); err != nil {
			return res, err
		}
		res.Level, res.Changed = LevelBasic, true
	case !res.Met && level == LevelBasic:
		if err := s.setProfile(ctx, tx, profileID, LevelUnverified, status, "BASIC_CONDITION_NOT_MET", "", nil); err != nil {
			return res, err
		}
		res.Level, res.Changed = LevelUnverified, true
	}
	return res, nil
}
