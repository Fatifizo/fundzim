package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Requirement is a set of alternative document types; at least one CLEAN document of one of them must be
// attached (e.g. ["ZW_NATIONAL_ID","ZW_PASSPORT","FOREIGN_PASSPORT"]).
type Requirement []string

// Rule holds the requirements for one subject category.
type Rule struct {
	RequiredDocuments      []Requirement `json:"required_documents"`
	ValidityDays           int           `json:"validity_days,omitempty"`
	SecondApproval         bool          `json:"second_approval"`
	MinPersons             int           `json:"min_persons,omitempty"`
	RepresentativeMinLevel string        `json:"representative_min_level,omitempty"`
}

// PolicyRules is the versioned verification policy document (kyc.verification_policies.rules). It is an
// internal policy, not a statement of legal requirements (brief §6).
type PolicyRules struct {
	KYC               map[string]Rule `json:"kyc"`         // by risk level
	KYB               map[string]Rule `json:"kyb"`         // by organisation type, "default"
	Beneficiary       map[string]Rule `json:"beneficiary"` // by beneficiary type
	PayoutDestination struct {
		OwnershipEvidence []string `json:"ownership_evidence"`
		ValidityDays      int      `json:"validity_days"`
	} `json:"payout_destination"`
	InformationRequestExpiryDays int `json:"information_request_expiry_days"`
	// AdultAge is the age from which an individual may verify themselves and is not a MINOR beneficiary. It is
	// INTERNAL_POLICY configuration, not a legal conclusion (LR-021, LR-015 remain OPEN).
	AdultAge  int `json:"adult_age"`
	Screening struct {
		Provider *string `json:"provider"`
		Status   string  `json:"status"`
	} `json:"screening"`
}

// Policy is an approved policy version.
type Policy struct {
	Version string
	Rules   PolicyRules
}

var docTypes = map[string]bool{"ZW_NATIONAL_ID": true, "ZW_PASSPORT": true, "FOREIGN_PASSPORT": true, "ZW_BIRTH_CERTIFICATE": true,
	"SELFIE": true, "PROOF_OF_ADDRESS": true, "REGISTRATION_CERTIFICATE": true, "REGISTRY_EXTRACT": true, "CONSTITUTION": true,
	"TRUST_DEED": true, "DIRECTOR_REGISTER": true, "BO_DECLARATION": true, "BOARD_RESOLUTION": true, "AUTHORITY_LETTER": true,
	"PVO_CERTIFICATE": true, "GUARDIANSHIP_EVIDENCE": true, "BIRTH_CERTIFICATE": true, "RELATIONSHIP_EVIDENCE": true,
	"CONSENT_FORM": true, "MEDICAL_EVIDENCE": true, "BANK_LETTER": true, "BANK_STATEMENT": true, "MOBILE_MONEY_STATEMENT": true, "OTHER": true}

// IsDocumentType reports whether t is a known verification document type.
func IsDocumentType(t string) bool { return docTypes[t] }

// Validate checks a policy document before it can be proposed (the engine refuses unknown types and
// incomplete risk coverage so a bad policy can never be approved).
func (r PolicyRules) Validate() error {
	var problems []string
	for _, lvl := range []string{RiskLow, RiskStandard, RiskEnhanced, RiskRestricted} {
		if _, ok := r.KYC[lvl]; !ok {
			problems = append(problems, "kyc."+lvl+" missing")
		}
	}
	if _, ok := r.KYB["default"]; !ok {
		problems = append(problems, "kyb.default missing")
	}
	for _, t := range []string{"SELF", "INDIVIDUAL", "MINOR", "INCAPACITATED_ADULT", "ORGANISATION", "COMMUNITY_GROUP"} {
		if _, ok := r.Beneficiary[t]; !ok {
			problems = append(problems, "beneficiary."+t+" missing")
		}
	}
	for _, m := range []map[string]Rule{r.KYC, r.KYB, r.Beneficiary} {
		for name, rule := range m {
			for _, req := range rule.RequiredDocuments {
				if len(req) == 0 {
					problems = append(problems, name+": empty requirement")
				}
				for _, t := range req {
					if !docTypes[t] {
						problems = append(problems, name+": unknown document type "+t)
					}
				}
			}
			if rule.ValidityDays < 0 || rule.ValidityDays > 3650 {
				problems = append(problems, name+": validity_days out of range")
			}
		}
	}
	// minors and incapacitated adults always need a second approver (beneficiary-verification §5)
	if !r.Beneficiary["MINOR"].SecondApproval || !r.Beneficiary["INCAPACITATED_ADULT"].SecondApproval {
		problems = append(problems, "beneficiary MINOR and INCAPACITATED_ADULT must require second approval")
	}
	if !r.KYC[RiskRestricted].SecondApproval {
		problems = append(problems, "kyc.RESTRICTED must require second approval")
	}
	if r.AdultAge < 1 || r.AdultAge > 30 {
		problems = append(problems, "adult_age missing or out of range")
	}
	if r.InformationRequestExpiryDays < 1 || r.InformationRequestExpiryDays > 365 {
		problems = append(problems, "information_request_expiry_days out of range")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("invalid verification policy: %s", strings.Join(problems, "; "))
	}
	return nil
}

// KYCRule returns the rule for a risk level (STANDARD when unknown).
func (p Policy) KYCRule(risk string) Rule {
	if r, ok := p.Rules.KYC[risk]; ok {
		return r
	}
	return p.Rules.KYC[RiskStandard]
}

// KYBRule returns the rule for an organisation type (default when not specified).
func (p Policy) KYBRule(orgType string) Rule {
	if r, ok := p.Rules.KYB[orgType]; ok {
		return r
	}
	return p.Rules.KYB["default"]
}

// BeneficiaryRule returns the rule for a beneficiary type.
func (p Policy) BeneficiaryRule(t string) Rule { return p.Rules.Beneficiary[t] }

// ---- loading and maker-checker --------------------------------------------------------------------------

var (
	policyMu     sync.Mutex
	policyCache  *Policy
	policyLoaded time.Time
)

// ActivePolicy returns the APPROVED policy (cached for 30 seconds). Decisions record its version, so a later
// version never changes historical decisions.
func (s *Service) ActivePolicy(ctx context.Context) (Policy, error) {
	policyMu.Lock()
	defer policyMu.Unlock()
	if policyCache != nil && time.Since(policyLoaded) < 30*time.Second {
		return *policyCache, nil
	}
	var p Policy
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT policy_version, rules FROM kyc.verification_policies WHERE status = 'APPROVED'`).Scan(&p.Version, &raw)
	if err != nil {
		return Policy{}, fmt.Errorf("kyc: no approved verification policy: %w", err)
	}
	if err := json.Unmarshal(raw, &p.Rules); err != nil {
		return Policy{}, fmt.Errorf("kyc: approved policy is unreadable: %w", err)
	}
	policyCache, policyLoaded = &p, time.Now()
	return p, nil
}

func invalidatePolicyCache() {
	policyMu.Lock()
	policyCache = nil
	policyMu.Unlock()
}

// PolicyVersion is a policy row for the admin API.
type PolicyVersion struct {
	ID            string          `json:"id"`
	PolicyVersion string          `json:"policy_version"`
	Status        string          `json:"status"`
	Rules         json.RawMessage `json:"rules"`
	MadeBy        string          `json:"made_by"`
	ApprovedBy    *string         `json:"approved_by"`
	ApprovedAt    *time.Time      `json:"approved_at"`
	ChangeReason  string          `json:"change_reason"`
	DecisionRef   string          `json:"decision_ref"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ListPolicies returns all versions, newest first.
func (s *Service) ListPolicies(ctx context.Context) ([]PolicyVersion, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, policy_version, status, rules, made_by, approved_by, approved_at, change_reason, decision_ref, created_at
		FROM kyc.verification_policies ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PolicyVersion{}
	for rows.Next() {
		var p PolicyVersion
		if err := rows.Scan(&p.ID, &p.PolicyVersion, &p.Status, &p.Rules, &p.MadeBy, &p.ApprovedBy, &p.ApprovedAt, &p.ChangeReason,
			&p.DecisionRef, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var decisionRefRe = regexp.MustCompile(`^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2}|ADR-[0-9]{3})$`)
var policyVersionRe = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)?$`)

// ProposePolicy records a new PROPOSED version (maker).
func (s *Service) ProposePolicy(ctx context.Context, maker, version string, rules json.RawMessage, reason, decisionRef string) (string, error) {
	var pr PolicyRules
	if !policyVersionRe.MatchString(version) || !decisionRefRe.MatchString(decisionRef) || len(strings.TrimSpace(reason)) < 10 {
		return "", errs.New(errs.Unprocessable, "VALIDATION_FAILED", "Provide a version (vN), a decision reference and a reason of at least 10 characters.")
	}
	dec := json.NewDecoder(strings.NewReader(string(rules)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pr); err != nil {
		return "", errs.New(errs.Unprocessable, "POLICY_INVALID", "The policy document could not be read: "+err.Error())
	}
	if err := pr.Validate(); err != nil {
		return "", errs.New(errs.Unprocessable, "POLICY_INVALID", err.Error())
	}
	id := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.verification_policies (id, policy_version, rules, status, made_by, change_reason, decision_ref, effective_from)
			VALUES ($1, $2, $3, 'PROPOSED', $4, $5, $6, $7)`, id, version, rules, maker, strings.TrimSpace(reason), decisionRef, s.now()); err != nil {
			if isUnique(err) {
				return errs.New(errs.Conflict, "POLICY_VERSION_EXISTS", "That policy version already exists.")
			}
			return err
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.policy.proposed", TargetType: "verification_policy",
			TargetID: id, Justification: reason, Metadata: map[string]any{"policy_version": version, "decision_ref": decisionRef}})
		return err
	})
	return id, err
}

// ApprovePolicy approves a PROPOSED version (checker ≠ maker, DB-enforced too) and retires the previous one.
func (s *Service) ApprovePolicy(ctx context.Context, checker, policyID string, approve bool) error {
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var maker, status, version string
		err := tx.QueryRow(ctx, `SELECT made_by, status, policy_version FROM kyc.verification_policies WHERE id = $1 FOR UPDATE`, policyID).
			Scan(&maker, &status, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.NotFound, "POLICY_NOT_FOUND", "No such policy version.")
		}
		if err != nil {
			return err
		}
		if status != "PROPOSED" {
			return errs.New(errs.Conflict, "POLICY_ALREADY_DECIDED", "This policy version has already been decided.")
		}
		if maker == checker {
			return errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "The proposer cannot approve their own policy.")
		}
		now := s.now()
		if approve {
			if _, err := tx.Exec(ctx, `UPDATE kyc.verification_policies SET status = 'RETIRED', retired_at = $1 WHERE status = 'APPROVED'`, now); err != nil {
				return err
			}
		}
		newStatus := map[bool]string{true: "APPROVED", false: "REJECTED"}[approve]
		if _, err := tx.Exec(ctx, `UPDATE kyc.verification_policies SET status = $2, approved_by = $3, approved_at = $4 WHERE id = $1`,
			policyID, newStatus, checker, now); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.policy." + strings.ToLower(newStatus),
			TargetType: "verification_policy", TargetID: policyID, Metadata: map[string]any{"policy_version": version}})
		return err
	})
	if err == nil {
		invalidatePolicyCache()
	}
	return err
}
