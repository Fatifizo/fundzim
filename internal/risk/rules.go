package risk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Rule administration (ADR-045 §3–§4; brief §14–§17). A rule is a declarative JSON definition (engine.go) — never
// code. Versions are immutable once submitted (definition_hash); approval is maker-checker with a fresh step-up
// (never the maker, the submitter or an account linked to either); activation, suspension and retirement need
// step-up; reactivation is by someone other than the suspender. No rule is seeded: nothing is ACTIVE until two people
// approve it.

// Rule version statuses.
const (
	RVDraft     = "DRAFT"
	RVPending   = "PENDING_APPROVAL"
	RVApproved  = "APPROVED"
	RVRejected  = "REJECTED"
	RVActive    = "ACTIVE"
	RVSuspended = "SUSPENDED"
	RVRetired   = "RETIRED"
)

// EvRuleVersionChanged is published on every rule version lifecycle change.
const EvRuleVersionChanged = "risk.rule_version_changed"

// RuleVersionEdges mirrors the 'risk_rule_version' machine (keep identical; TestRuleEdgesMatchDatabase).
var RuleVersionEdges = map[string][]string{
	"":          {RVDraft},
	RVDraft:     {RVPending, RVRetired},
	RVPending:   {RVApproved, RVRejected},
	RVApproved:  {RVActive, RVRetired},
	RVActive:    {RVSuspended, RVRetired},
	RVSuspended: {RVActive, RVRetired},
}

var categories = map[string]bool{CatIdentity: true, CatCampaign: true, CatPayment: true, CatWithdrawal: true, CatDestination: true, CatSession: true,
	CatDevice: true, CatVelocity: true, CatCompliance: true, CatProvider: true, CatFee: true, CatChargeback: true}

// RuleVersionInput is a DRAFT rule version.
type RuleVersionInput struct {
	Definition          json.RawMessage `json:"definition"`
	Contribution        int             `json:"contribution"`
	Severity            string          `json:"severity"`
	RecommendedAction   string          `json:"recommended_action"`
	HoldType            *string         `json:"hold_type"`
	HoldDurationSeconds *int            `json:"hold_duration_seconds"`
	CasePriority        string          `json:"case_priority"`
}

// NewRule creates a rule with its first DRAFT version.
type NewRule struct {
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	SubjectType string           `json:"subject_type"`
	Category    string           `json:"category"`
	Version     RuleVersionInput `json:"version"`
}

// RiskRule is a rule with its versions.
type RiskRule struct {
	ID          string        `json:"id"`
	Code        string        `json:"code"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	SubjectType string        `json:"subject_type"`
	Category    string        `json:"category"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	Versions    []RuleVersion `json:"versions"`
	// ActiveVersionID is the version in force (nil: none).
	ActiveVersionID *string `json:"active_version_id"`
}

// RuleVersion is an immutable (once submitted) rule version.
type RuleVersion struct {
	ID                  string          `json:"id"`
	RuleID              string          `json:"rule_id"`
	VersionNo           int             `json:"version_no"`
	Status              string          `json:"status"`
	Definition          json.RawMessage `json:"definition"`
	Contribution        int             `json:"contribution"`
	Severity            string          `json:"severity"`
	RecommendedAction   string          `json:"recommended_action"`
	HoldType            *string         `json:"hold_type"`
	HoldDurationSeconds *int            `json:"hold_duration_seconds"`
	CasePriority        string          `json:"case_priority"`
	EngineVersion       string          `json:"engine_version"`
	DefinitionHash      *string         `json:"definition_hash"`
	CreatedBy           string          `json:"created_by"`
	SubmittedBy         *string         `json:"submitted_by"`
	SubmittedAt         *time.Time      `json:"submitted_at"`
	ApprovedBy          *string         `json:"approved_by"`
	ApprovedAt          *time.Time      `json:"approved_at"`
	ApprovalReason      *string         `json:"approval_reason"`
	RejectedBy          *string         `json:"rejected_by"`
	RejectionReason     *string         `json:"rejection_reason"`
	ActivatedBy         *string         `json:"activated_by"`
	ActivatedAt         *time.Time      `json:"activated_at"`
	SuspendedBy         *string         `json:"suspended_by"`
	SuspensionReason    *string         `json:"suspension_reason"`
	RetiredBy           *string         `json:"retired_by"`
	RetirementReason    *string         `json:"retirement_reason"`
	Version             int             `json:"version"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	// set by lockRuleVersion (not serialised)
	subjectType string
}

const ruleVersionCols = `v.id, v.rule_id, v.version_no, v.status, v.definition, v.contribution, v.severity, v.recommended_action, v.hold_type,
	v.hold_duration_seconds, v.case_priority, v.engine_version, v.definition_hash, v.created_by, v.submitted_by, v.submitted_at, v.approved_by,
	v.approved_at, v.approval_reason, v.rejected_by, v.rejection_reason, v.activated_by, v.activated_at, v.suspended_by, v.suspension_reason,
	v.retired_by, v.retirement_reason, v.version, v.created_at, v.updated_at, r.subject_type`

func scanRuleVersion(r pgx.Row) (RuleVersion, error) {
	var v RuleVersion
	var contrib int16
	var h []byte
	err := r.Scan(&v.ID, &v.RuleID, &v.VersionNo, &v.Status, &v.Definition, &contrib, &v.Severity, &v.RecommendedAction, &v.HoldType,
		&v.HoldDurationSeconds, &v.CasePriority, &v.EngineVersion, &h, &v.CreatedBy, &v.SubmittedBy, &v.SubmittedAt, &v.ApprovedBy, &v.ApprovedAt,
		&v.ApprovalReason, &v.RejectedBy, &v.RejectionReason, &v.ActivatedBy, &v.ActivatedAt, &v.SuspendedBy, &v.SuspensionReason, &v.RetiredBy,
		&v.RetirementReason, &v.Version, &v.CreatedAt, &v.UpdatedAt, &v.subjectType)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleVersion{}, ErrNotFound
	}
	v.Contribution = int(contrib)
	if h != nil {
		x := hexs(h)
		v.DefinitionHash = &x
	}
	return v, err
}

// validateVersion checks a version's attributes and definition (rule safety: bounded, declarative, catalogued signals).
func validateVersion(in *RuleVersionInput) error {
	var det []errs.Detail
	bad := func(f, c string) { det = append(det, errs.Detail{Field: f, Code: c}) }
	if len(in.Definition) == 0 || len(in.Definition) > 16384 {
		bad("definition", "INVALID_LENGTH")
	} else if d, err := ParseDefinition(in.Definition, func(s string) bool { return SignalMetaOf(s).Known }); err != nil {
		e := errs.New(errs.Unprocessable, "RISK_RULE_INVALID", strings.TrimPrefix(err.Error(), ErrRule.Error()+": "))
		e.Details = []errs.Detail{{Field: "definition", Code: "INVALID_RULE"}}
		return e
	} else {
		canon, _ := json.Marshal(d)
		in.Definition = canon // stored canonical: what is hashed is what runs
	}
	if in.Contribution < 0 || in.Contribution > 100 {
		bad("contribution", "OUT_OF_RANGE")
	}
	switch in.Severity {
	case "LOW", "MEDIUM", "HIGH", "CRITICAL":
	default:
		bad("severity", "INVALID_VALUE")
	}
	switch in.RecommendedAction {
	case ActMonitor, ActReview, ActEscalate, ActBlock:
		if in.HoldType != nil || in.HoldDurationSeconds != nil {
			bad("hold_type", "ONLY_FOR_TEMPORARY_HOLD")
		}
	case ActTemporaryHold:
		if in.HoldType == nil || (*in.HoldType != HoldWithdrawal && *in.HoldType != HoldDestination) {
			bad("hold_type", "AUTOMATED_HOLD_TYPE_NOT_ALLOWED")
		}
		if in.HoldDurationSeconds == nil || *in.HoldDurationSeconds < 3600 || *in.HoldDurationSeconds > 604800 {
			bad("hold_duration_seconds", "OUT_OF_RANGE")
		}
	default:
		bad("recommended_action", "INVALID_VALUE")
	}
	if priorityRank[in.CasePriority] == 0 {
		bad("case_priority", "INVALID_VALUE")
	}
	if len(det) > 0 {
		e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
		e.Details = det
		return e
	}
	return nil
}

// CreateRule creates a rule and its first DRAFT version (risk.rule.request).
func (s *Service) CreateRule(ctx context.Context, staffID string, in NewRule) (RiskRule, error) {
	in.Code, in.Name = strings.ToUpper(strings.TrimSpace(in.Code)), strings.TrimSpace(in.Name)
	if !codeRE.MatchString(in.Code) || strings.Contains(in.Code, "FRAUD") {
		return RiskRule{}, validation("code", "INVALID_FORMAT")
	}
	if n := utf8.RuneCountInString(in.Name); n < 3 || n > 120 {
		return RiskRule{}, validation("name", "INVALID_LENGTH")
	}
	// explanations name the rule; no engine output is ever worded as a fraud finding (ck_risk_evaluations_no_fraud_claim)
	if strings.Contains(strings.ToLower(in.Name), "fraud") {
		return RiskRule{}, validation("name", "FRAUD_WORDING_NOT_ALLOWED")
	}
	if utf8.RuneCountInString(in.Description) > 2000 {
		return RiskRule{}, validation("description", "INVALID_LENGTH")
	}
	if !CaseSubjects[in.SubjectType] {
		return RiskRule{}, validation("subject_type", "INVALID_VALUE")
	}
	if !categories[in.Category] {
		return RiskRule{}, validation("category", "INVALID_VALUE")
	}
	if err := validateVersion(&in.Version); err != nil {
		return RiskRule{}, err
	}
	id := ids.New()
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_rules (id, code, name, description, subject_type, category, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, in.Code, in.Name, in.Description, in.SubjectType, in.Category, staffID); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_rule", id, "RULE_CREATED", "STAFF", staffID, map[string]any{"code": in.Code}, now); err != nil {
			return err
		}
		if _, err := insertVersion(ctx, tx, staffID, id, in.Version, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.rule.created", TargetType: "risk_rule", TargetID: id,
			Metadata: map[string]any{"code": in.Code, "subject_type": in.SubjectType}})
	})
	if err != nil {
		return RiskRule{}, err
	}
	return s.RiskRule(ctx, id)
}

func insertVersion(ctx context.Context, tx pgx.Tx, staffID, ruleID string, in RuleVersionInput, now time.Time) (string, error) {
	id := ids.New()
	_, err := tx.Exec(ctx, `INSERT INTO risk.risk_rule_versions (id, rule_id, version_no, status, definition, contribution, severity, recommended_action,
		hold_type, hold_duration_seconds, case_priority, engine_version, created_by)
		VALUES ($1, $2, (SELECT coalesce(max(version_no), 0) + 1 FROM risk.risk_rule_versions WHERE rule_id = $2), 'DRAFT', $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, ruleID, []byte(in.Definition), in.Contribution, in.Severity, in.RecommendedAction, in.HoldType, in.HoldDurationSeconds, in.CasePriority,
		EngineVersion, staffID)
	if err != nil {
		return "", mapDB(err)
	}
	return id, riskEvent(ctx, tx, "risk_rule_version", id, "VERSION_DRAFTED", "STAFF", staffID, map[string]any{"rule_id": ruleID}, now)
}

// CreateRuleVersion adds a DRAFT version to a rule (one draft or pending version at a time).
func (s *Service) CreateRuleVersion(ctx context.Context, staffID, ruleID string, in RuleVersionInput) (RuleVersion, error) {
	if !ids.Valid(ruleID) {
		return RuleVersion{}, ErrNotFound
	}
	if err := validateVersion(&in); err != nil {
		return RuleVersion{}, err
	}
	var id string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM risk.risk_rules WHERE id = $1`, ruleID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		var err error
		id, err = insertVersion(ctx, tx, staffID, ruleID, in, s.now())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.rule_version.drafted", TargetType: "risk_rule_version", TargetID: id})
	})
	if err != nil {
		return RuleVersion{}, err
	}
	return s.RuleVersion(ctx, id)
}

// UpdateRuleDraft edits a DRAFT version (definition and attributes are editable only in DRAFT).
func (s *Service) UpdateRuleDraft(ctx context.Context, staffID, versionID string, expect int, in RuleVersionInput) (RuleVersion, error) {
	if err := validateVersion(&in); err != nil {
		return RuleVersion{}, err
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		v, err := lockRuleVersion(ctx, tx, versionID, expect)
		if err != nil {
			return err
		}
		if v.Status != RVDraft {
			return ErrRuleState
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET definition = $2, contribution = $3, severity = $4, recommended_action = $5,
			hold_type = $6, hold_duration_seconds = $7, case_priority = $8, version = version + 1 WHERE id = $1`, versionID, []byte(in.Definition),
			in.Contribution, in.Severity, in.RecommendedAction, in.HoldType, in.HoldDurationSeconds, in.CasePriority); err != nil {
			return mapDB(err)
		}
		return riskEvent(ctx, tx, "risk_rule_version", versionID, "DRAFT_UPDATED", "STAFF", staffID, map[string]any{}, s.now())
	})
	if err != nil {
		return RuleVersion{}, err
	}
	return s.RuleVersion(ctx, versionID)
}

func lockRuleVersion(ctx context.Context, tx pgx.Tx, id string, expect int) (RuleVersion, error) {
	if !ids.Valid(id) {
		return RuleVersion{}, ErrNotFound
	}
	v, err := scanRuleVersion(tx.QueryRow(ctx, `SELECT `+ruleVersionCols+` FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id
		WHERE v.id = $1 FOR UPDATE OF v`, id))
	if err != nil {
		return RuleVersion{}, err
	}
	if expect > 0 && v.Version != expect {
		return RuleVersion{}, ErrStale
	}
	return v, nil
}

// definitionHash hashes everything that decides what a version does.
func definitionHash(v RuleVersion) [32]byte {
	var buf bytes.Buffer
	doc := map[string]any{"definition": json.RawMessage(v.Definition), "contribution": v.Contribution, "severity": v.Severity,
		"recommended_action": v.RecommendedAction, "hold_type": v.HoldType, "hold_duration_seconds": v.HoldDurationSeconds,
		"case_priority": v.CasePriority, "engine_version": v.EngineVersion}
	b, _ := json.Marshal(doc)
	buf.Write(b)
	return sha256.Sum256(buf.Bytes())
}

// RuleOp is a staff action on a rule version.
type RuleOp struct {
	StaffID         string
	StepUpAt        time.Time
	VersionID       string
	ExpectedVersion int
	Reason          string
	// DefinitionHash (approve): the hash the checker reviewed; a mismatch refuses the approval.
	DefinitionHash string
}

// SubmitRuleVersion freezes a DRAFT (→ PENDING_APPROVAL) and hashes it.
func (s *Service) SubmitRuleVersion(ctx context.Context, op RuleOp) (RuleVersion, error) {
	return s.ruleOp(ctx, op, false, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		if v.Status != RVDraft {
			return "", ErrRuleState
		}
		h := definitionHash(v)
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'PENDING_APPROVAL', submitted_by = $2, submitted_at = $3, definition_hash = $4,
			version = version + 1 WHERE id = $1`, v.ID, op.StaffID, now, h[:])
		return "VERSION_SUBMITTED", mapDB(err)
	})
}

// DecideRuleVersion is the checker step (risk.rule.approve; fresh step-up).
func (s *Service) DecideRuleVersion(ctx context.Context, op RuleOp, approve bool) (RuleVersion, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return RuleVersion{}, err
	}
	return s.ruleOp(ctx, op, true, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		if v.Status != RVPending {
			return "", ErrRuleState
		}
		if op.DefinitionHash != "" && (v.DefinitionHash == nil || *v.DefinitionHash != op.DefinitionHash) {
			return "", errs.New(errs.Conflict, "RISK_RULE_CHANGED", "The rule you reviewed is not the rule of this version.")
		}
		sub := ""
		if v.SubmittedBy != nil {
			sub = *v.SubmittedBy
		}
		if self, err := s.linked(ctx, op.StaffID, v.CreatedBy, sub); err != nil {
			return "", err
		} else if self {
			return "", ErrSelfApproval
		}
		if approve {
			_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'APPROVED', approved_by = $2, approved_at = $3, approved_step_up_at = $4,
				approval_reason = $5, version = version + 1 WHERE id = $1`, v.ID, op.StaffID, now, minTime(op.StepUpAt.UTC(), now), r)
			return "VERSION_APPROVED", mapDB(err)
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'REJECTED', rejected_by = $2, rejected_at = $3, rejection_reason = $4,
			version = version + 1 WHERE id = $1`, v.ID, op.StaffID, now, r)
		return "VERSION_REJECTED", mapDB(err)
	})
}

// ActivateRuleVersion puts an APPROVED version in force (step-up); the rule's previously ACTIVE version is retired in
// the same transaction (one active version per rule).
func (s *Service) ActivateRuleVersion(ctx context.Context, op RuleOp) (RuleVersion, error) {
	return s.ruleOp(ctx, op, true, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		if v.Status != RVApproved {
			return "", ErrRuleState
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'RETIRED', retired_by = $1, retired_at = $2,
			retirement_reason = 'superseded by version ' || $3::text, version = version + 1 WHERE rule_id = $4 AND status IN ('ACTIVE','SUSPENDED')`,
			op.StaffID, now, strconv.Itoa(v.VersionNo), v.RuleID); err != nil {
			return "", mapDB(err)
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'ACTIVE', activated_by = $2, activated_at = $3, version = version + 1
			WHERE id = $1`, v.ID, op.StaffID, now)
		return "VERSION_ACTIVATED", mapDB(err)
	})
}

// SuspendRuleVersion takes an ACTIVE version out of force (step-up, reason).
func (s *Service) SuspendRuleVersion(ctx context.Context, op RuleOp) (RuleVersion, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return RuleVersion{}, err
	}
	return s.ruleOp(ctx, op, true, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		if v.Status != RVActive {
			return "", ErrRuleState
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'SUSPENDED', suspended_by = $2, suspended_at = $3, suspension_reason = $4,
			reactivated_by = NULL, reactivated_at = NULL, version = version + 1 WHERE id = $1`, v.ID, op.StaffID, now, r)
		return "VERSION_SUSPENDED", mapDB(err)
	})
}

// ReactivateRuleVersion returns a SUSPENDED version to force (risk.rule.approve; never its suspender or anyone linked).
func (s *Service) ReactivateRuleVersion(ctx context.Context, op RuleOp) (RuleVersion, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return RuleVersion{}, err
	}
	return s.ruleOp(ctx, op, true, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		if v.Status != RVSuspended {
			return "", ErrRuleState
		}
		sus := ""
		if v.SuspendedBy != nil {
			sus = *v.SuspendedBy
		}
		if self, err := s.linked(ctx, op.StaffID, sus); err != nil {
			return "", err
		} else if self {
			return "", ErrSelfApproval
		}
		_ = r
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'ACTIVE', reactivated_by = $2, reactivated_at = $3, version = version + 1
			WHERE id = $1`, v.ID, op.StaffID, now)
		return "VERSION_REACTIVATED", mapDB(err)
	})
}

// RetireRuleVersion ends a version for good.
func (s *Service) RetireRuleVersion(ctx context.Context, op RuleOp) (RuleVersion, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return RuleVersion{}, err
	}
	return s.ruleOp(ctx, op, true, func(ctx context.Context, tx pgx.Tx, v RuleVersion, now time.Time) (string, error) {
		switch v.Status {
		case RVDraft, RVApproved, RVActive, RVSuspended:
		default:
			return "", ErrRuleState
		}
		_, err := tx.Exec(ctx, `UPDATE risk.risk_rule_versions SET status = 'RETIRED', retired_by = $2, retired_at = $3, retirement_reason = $4,
			version = version + 1 WHERE id = $1`, v.ID, op.StaffID, now, r)
		return "VERSION_RETIRED", mapDB(err)
	})
}

func (s *Service) ruleOp(ctx context.Context, op RuleOp, stepUp bool, fn func(context.Context, pgx.Tx, RuleVersion, time.Time) (string, error)) (RuleVersion, error) {
	if stepUp && op.StepUpAt.IsZero() {
		return RuleVersion{}, ErrStepUp
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		v, err := lockRuleVersion(ctx, tx, op.VersionID, op.ExpectedVersion)
		if err != nil {
			return err
		}
		ev, err := fn(ctx, tx, v, now)
		if err != nil {
			return err
		}
		if err := riskEvent(ctx, tx, "risk_rule_version", v.ID, ev, "STAFF", op.StaffID, map[string]any{"reason": op.Reason, "from": v.Status}, now); err != nil {
			return err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_rule_version", AggregateID: v.ID, EventType: EvRuleVersionChanged,
			Payload: map[string]any{"rule_version_id": v.ID, "rule_id": v.RuleID, "change": ev}, OccurredAt: now}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.rule_version." + strings.ToLower(strings.TrimPrefix(ev, "VERSION_")),
			TargetType: "risk_rule_version", TargetID: v.ID, Reason: op.Reason})
	})
	if err != nil {
		return RuleVersion{}, err
	}
	return s.RuleVersion(ctx, op.VersionID)
}

// RuleVersion returns a version.
func (s *Service) RuleVersion(ctx context.Context, id string) (RuleVersion, error) {
	if !ids.Valid(id) {
		return RuleVersion{}, ErrNotFound
	}
	return scanRuleVersion(s.Pool.QueryRow(ctx, `SELECT `+ruleVersionCols+` FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id
		WHERE v.id = $1`, id))
}

// RiskRule returns a rule with its versions (newest first).
func (s *Service) RiskRule(ctx context.Context, id string) (RiskRule, error) {
	if !ids.Valid(id) {
		return RiskRule{}, ErrNotFound
	}
	var r RiskRule
	err := s.Pool.QueryRow(ctx, `SELECT id, code, name, description, subject_type, category, created_by, created_at FROM risk.risk_rules WHERE id = $1`,
		id).Scan(&r.ID, &r.Code, &r.Name, &r.Description, &r.SubjectType, &r.Category, &r.CreatedBy, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RiskRule{}, ErrNotFound
	}
	if err != nil {
		return RiskRule{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+ruleVersionCols+` FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id
		WHERE v.rule_id = $1 ORDER BY v.version_no DESC`, id)
	if err != nil {
		return RiskRule{}, err
	}
	defer rows.Close()
	r.Versions = []RuleVersion{}
	for rows.Next() {
		v, err := scanRuleVersion(rows)
		if err != nil {
			return RiskRule{}, err
		}
		if v.Status == RVActive {
			x := v.ID
			r.ActiveVersionID = &x
		}
		r.Versions = append(r.Versions, v)
	}
	return r, rows.Err()
}

// RuleSummary is a rule list row.
type RuleSummary struct {
	ID              string  `json:"id"`
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	SubjectType     string  `json:"subject_type"`
	Category        string  `json:"category"`
	ActiveVersionNo *int    `json:"active_version_no"`
	PendingVersion  *int    `json:"pending_version_no"`
	ActiveAction    *string `json:"active_action"`
}

// Rules lists rules.
func (s *Service) Rules(ctx context.Context) ([]RuleSummary, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.id, r.code, r.name, r.subject_type, r.category,
		(SELECT version_no FROM risk.risk_rule_versions WHERE rule_id = r.id AND status = 'ACTIVE'),
		(SELECT version_no FROM risk.risk_rule_versions WHERE rule_id = r.id AND status = 'PENDING_APPROVAL'),
		(SELECT recommended_action FROM risk.risk_rule_versions WHERE rule_id = r.id AND status = 'ACTIVE')
		FROM risk.risk_rules r ORDER BY r.code LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuleSummary{}
	for rows.Next() {
		var x RuleSummary
		if err := rows.Scan(&x.ID, &x.Code, &x.Name, &x.SubjectType, &x.Category, &x.ActiveVersionNo, &x.PendingVersion, &x.ActiveAction); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SimulateRule evaluates a version (any status) against a subject's recorded signals and current state without
// persisting anything or acting (rule testing before approval; brief §17).
func (s *Service) SimulateRule(ctx context.Context, versionID, subjectID string) (EngineResult, error) {
	v, err := s.RuleVersion(ctx, versionID)
	if err != nil {
		return EngineResult{}, err
	}
	if !ids.Valid(subjectID) {
		return EngineResult{}, validation("subject_id", "INVALID_FORMAT")
	}
	d, err := ParseDefinition(v.Definition, func(x string) bool { return SignalMetaOf(x).Known })
	if err != nil {
		return EngineResult{}, err
	}
	hold := ""
	if v.HoldType != nil {
		hold = *v.HoldType
	}
	rule := RuleInput{VersionID: v.ID, RuleCode: "SIMULATION", RuleName: "simulation", Definition: d, Contribution: v.Contribution,
		Severity: v.Severity, Action: v.RecommendedAction, HoldType: hold, Priority: v.CasePriority}
	var ev EngineResult
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		now := s.now()
		sigs, err := subjectSignals(ctx, tx, v.subjectType, subjectID, now.Add(-MaxWindowOf([]RuleInput{rule})))
		if err != nil {
			return err
		}
		st, err := subjectState(ctx, tx, v.subjectType, subjectID)
		if err != nil {
			return err
		}
		bands, err := bandsAt(ctx, tx, now)
		if err != nil {
			return err
		}
		ev = EvaluateRules(now, sigs, st, []RuleInput{rule}, bands)
		return nil
	})
	return ev, err
}
