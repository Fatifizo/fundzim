// Package compliance is FundZim's compliance case module (Stage 5, interface contract §4 and §7.4,
// ADR-034 §5, ADR-035). It opens cases from escalations (KYC/KYB, beneficiary verification, risk engine) and
// from staff, and runs them through the compliance_case state machine with assignment, information requests,
// escalation, resolution with maker-checker for decisions that need a checker, closure and reopening.
//
// It uses ONLY the compliance pool (role fundzim_compliance): its own schema, SELECT on the risk outputs,
// and the SECURITY DEFINER gateways for audit (audit.RecordGateway) and outbox (outbox.WriteGateway).
// RESTRICTED_STR cases are hidden by row-level security unless a transaction sets fundzim.str_access after
// the caller's str.prepare permission (with a fresh step-up) was checked. Case notes are AES-256-GCM
// ciphertext (AAD = note id), only ever returned by staff endpoints and never logged.
package compliance

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Case statuses (ADR-034 §5).
const (
	StatusOpen                = "OPEN"
	StatusAssigned            = "ASSIGNED"
	StatusInReview            = "IN_REVIEW"
	StatusAwaitingInformation = "AWAITING_INFORMATION"
	StatusEscalated           = "ESCALATED"
	StatusResolved            = "RESOLVED"
	StatusClosed              = "CLOSED"
)

// Transitions is the compliance_case machine; it must stay identical to the app.status_transitions rows
// inserted by migration 20261009150600 (CLAUDE.md: Go and SQL edge lists identical).
var Transitions = map[string][]string{
	"":                        {StatusOpen},
	StatusOpen:                {StatusAssigned},
	StatusAssigned:            {StatusInReview},
	StatusInReview:            {StatusAwaitingInformation, StatusEscalated, StatusResolved},
	StatusAwaitingInformation: {StatusInReview},
	StatusEscalated:           {StatusInReview, StatusResolved},
	StatusResolved:            {StatusClosed, StatusInReview},
	StatusClosed:              {StatusInReview},
}

// CanTransition reports whether from -> to is an edge of the machine.
func CanTransition(from, to string) bool {
	for _, t := range Transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Resolution decisions. The ones in CheckerRequired take effect only after a different staff member
// approves them (compliance-case-management §4; ck_compliance_cases_checker_required).
var Decisions = map[string]bool{"CLEARED": true, "EDD_CONDITIONS": true, "RESTRICT": true, "SUSPEND": true, "OFFBOARD": true,
	"CONFIRMED_FRAUD": true}

// CheckerRequired lists the decisions that need maker-checker approval.
var CheckerRequired = map[string]bool{"RESTRICT": true, "SUSPEND": true, "OFFBOARD": true, "CONFIRMED_FRAUD": true}

// Resolution statuses.
const (
	ResolutionProposed = "PROPOSED"
	ResolutionApproved = "APPROVED"
)

// Case types, severities, sources and subject types accepted by the schema.
var (
	CaseTypes = map[string]bool{"KYC_REVIEW": true, "KYB_REVIEW": true, "BENEFICIARY_REVIEW": true, "PAYOUT_DESTINATION_REVIEW": true,
		"RISK_REVIEW": true, "SANCTIONS": true, "PEP_EDD": true, "FRAUD_CAMPAIGN": true, "ACCOUNT_TAKEOVER": true, "AML_MONITORING": true,
		"PAYOUT_REVIEW": true, "FUNDRAISING_AUTHORITY": true, "REGULATOR_REQUEST": true, "OTHER": true}
	Severities = map[string]int{"S3": 1, "S2": 2, "S1": 3} // higher = more severe
	// party subjects (PRIMARY_SUBJECT / RELATED_SUBJECT) and related objects (RELATED_OBJECT)
	PartyTypes  = map[string]bool{"USER": true, "ORGANISATION": true, "BENEFICIARY": true, "PAYOUT_DESTINATION": true, "CAMPAIGN": true}
	ObjectTypes = map[string]bool{"KYC_CASE": true, "KYB_CASE": true, "RISK_DECISION": true, "RISK_ASSESSMENT": true, "PAYMENT": true,
		"PAYOUT": true}
)

// Confidentiality levels.
const (
	ConfNormal        = "NORMAL"
	ConfRestrictedSTR = "RESTRICTED_STR"
)

// Outbox events emitted (through the gateway; prefix compliance.*).
const (
	EvCaseOpened   = "compliance.case_opened"
	EvCaseResolved = "compliance.case_resolved"
)

// Permissions used by the HTTP API (seeded by migrations 20261008140300 / 20261009150200).
const (
	PermView   = "compliance.case.view"
	PermManage = "case.manage"
	PermCreate = "case.create"
	PermSTR    = "str.prepare" // sees RESTRICTED_STR cases (with a fresh step-up)
)

var reasonCodeRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// ValidReasonCode reports whether s is an UPPER_SNAKE reason code (3-64 chars).
func ValidReasonCode(s string) bool { return reasonCodeRE.MatchString(s) }

// Relations answers questions about people that only the app pool can answer. The composition root
// implements it with the users / organisations / auth modules. Without it, decisions on cases that have
// ORGANISATION subjects and assignments to other staff fail closed (503 SUBJECT_CHECK_UNAVAILABLE).
type Relations interface {
	// PersonalAccount returns the personal (USER) account linked to a staff account, or "" if none.
	PersonalAccount(ctx context.Context, staffUserID string) (string, error)
	// IsOrganisationMember reports whether a personal account is an active member of the organisation.
	IsOrganisationMember(ctx context.Context, organisationID, userID string) (bool, error)
	// StaffHasPermission reports whether an active staff account currently holds the permission.
	StaffHasPermission(ctx context.Context, staffUserID, permission string) (bool, error)
}

// Deps are the service's dependencies.
type Deps struct {
	Pool         *pgxpool.Pool // compliance pool (fundzim_compliance) — never the app pool
	Clock        clock.Clock
	Logger       *slog.Logger
	NotesAEAD    *crypto.AEAD // COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY
	Relations    Relations    // optional; see Relations
	StepUpMaxAge time.Duration
}

// Service is the compliance module.
type Service struct {
	pool      *pgxpool.Pool
	clock     clock.Clock
	logger    *slog.Logger
	aead      *crypto.AEAD
	relations Relations
	stepUpAge time.Duration
}

// New validates the dependencies and returns the service.
func New(d Deps) (*Service, error) {
	if d.Pool == nil || d.NotesAEAD == nil {
		return nil, errors.New("compliance: pool and notes AEAD are required")
	}
	if d.Clock == nil {
		d.Clock = clock.System
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.StepUpMaxAge <= 0 {
		return nil, errors.New("compliance: StepUpMaxAge is required")
	}
	return &Service{pool: d.Pool, clock: d.Clock, logger: d.Logger, aead: d.NotesAEAD, relations: d.Relations, stepUpAge: d.StepUpMaxAge}, nil
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

// tx runs fn in a compliance-pool transaction; strAccess sets fundzim.str_access for this transaction only
// (callers decide it after the permission check).
func (s *Service) tx(ctx context.Context, strAccess bool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.WithTx(ctx, s.pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if strAccess {
			if _, err := tx.Exec(ctx, `SELECT set_config('fundzim.str_access', 'on', true)`); err != nil {
				return err
			}
		}
		return fn(ctx, tx)
	})
}

// Errors (stable codes; contract §7.3 / §7.4).
func errNotFound() error {
	return errs.New(errs.NotFound, "CASE_NOT_FOUND", "No such compliance case.")
}
func errStateChanged() error {
	return errs.New(errs.Conflict, "CASE_STATE_CHANGED", "The case changed meanwhile. Reload it and try again.")
}
func errNotAssigned() error {
	return errs.New(errs.Conflict, "NOT_ASSIGNED", "Only the assigned compliance officer can do this.")
}
func errSelfDecision() error {
	return errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You are connected to a subject of this case and cannot act on it.")
}
func errSelfApproval() error {
	return errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "A resolution must be approved by someone other than its decider.")
}
func errStepUp() error {
	return errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
}
func errSubjectCheckUnavailable() error {
	return errs.New(errs.Unavailable, "SUBJECT_CHECK_UNAVAILABLE", "The conflict-of-interest check is unavailable. Please retry shortly.")
}

// mapDBError turns guard/constraint violations raised by the database into stable API errors. The
// database is the last line of defence; the service normally refuses first.
func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch {
	case pe.ConstraintName == "ck_compliance_cases_actor_not_subject":
		return errSelfDecision()
	case pe.ConstraintName == "ck_compliance_cases_maker_checker":
		return errSelfApproval()
	case pe.Code == "23514" && strings.Contains(pe.Message, "illegal compliance_case transition"):
		return errStateChanged()
	}
	return err
}
