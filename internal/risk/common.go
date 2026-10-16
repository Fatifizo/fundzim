package risk

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Identities resolves an actor's linked accounts (implemented over kyc.Service.ActorIdentities in internal/app).
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// Stage 13 errors (stable codes).
var (
	ErrNotFound       = errs.New(errs.NotFound, "RISK_NOT_FOUND", "No such risk record.")
	ErrStepUp         = errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	ErrSelfApproval   = errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "You cannot approve a risk action you made, one made by an account linked to yours, or one about yourself.")
	ErrConflictOfInt  = errs.New(errs.Forbidden, "RISK_CONFLICT_OF_INTEREST", "You cannot work a risk record about yourself or an account linked to yours.")
	ErrRuleState      = errs.New(errs.Conflict, "RISK_RULE_STATE", "The risk rule version is not in a state that allows this action.")
	ErrRuleConflict   = errs.New(errs.Conflict, "RISK_RULE_CONFLICT", "This rule already has a draft or pending version, or the code is taken.")
	ErrCaseState      = errs.New(errs.Conflict, "RISK_CASE_STATE", "The risk case is not in a state that allows this action.")
	ErrHoldState      = errs.New(errs.Conflict, "RISK_HOLD_STATE", "The risk hold is not in a state that allows this action.")
	ErrHoldExists     = errs.New(errs.Conflict, "RISK_HOLD_EXISTS", "An open hold of this type already exists for this subject.")
	ErrScreeningState = errs.New(errs.Conflict, "RISK_SCREENING_STATE", "The screening request is not in a state that allows this action.")
	ErrScreeningOff   = errs.New(errs.Unavailable, "SCREENING_UNAVAILABLE", "Screening is not available: no screening provider is selected (TEST simulator only).")
	ErrStale          = errs.New(errs.Conflict, "RISK_RECORD_CHANGED", "The record changed since you loaded it. Reload and try again.")
	// ErrHeld is returned by enforcement points when an ACTIVE risk hold blocks the action (no detail is exposed).
	ErrHeld = errs.New(errs.Conflict, "RISK_HOLD_ACTIVE", "This action is on hold pending a review.")
)

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

func validation(field, code string) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = []errs.Detail{{Field: field, Code: code}}
	return e
}

func reasonText(r string) (string, error) {
	r = strings.TrimSpace(r)
	if n := utf8.RuneCountInString(r); n < 5 || n > 2000 {
		return "", validation("reason", "INVALID_LENGTH")
	}
	return r, nil
}

// linked reports whether actor is (or is linked to) any of others.
func (s *Service) linked(ctx context.Context, actor string, others ...string) (bool, error) {
	ids := []string{actor}
	if s.Identities != nil {
		var err error
		if ids, err = s.Identities.ActorIdentities(ctx, actor); err != nil {
			return false, err
		}
	}
	for _, id := range ids {
		for _, o := range others {
			if o != "" && id == o {
				return true, nil
			}
		}
	}
	return false, nil
}

func minTime(a, b time.Time) time.Time {
	if a.After(b) {
		return b
	}
	return a
}

// mapDB turns database refusals of risk invariants into stable errors (the Go checks normally fire first).
func mapDB(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch {
	case pg.Code == "23514" && strings.Contains(pg.Message, "maker-checker"):
		return ErrSelfApproval
	case pg.Code == "23514" && strings.Contains(pg.Message, "conflict of interest"):
		return ErrConflictOfInt
	case pg.Code == "23505" && strings.Contains(pg.ConstraintName, "uq_risk_holds_open"):
		return ErrHoldExists
	case pg.Code == "23505" && (strings.Contains(pg.ConstraintName, "uq_risk_rule_versions_one") || pg.ConstraintName == "uq_risk_rules_code"):
		return ErrRuleConflict
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal risk_rule_version"):
		return ErrRuleState
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal risk_case"):
		return ErrCaseState
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal risk_hold"):
		return ErrHoldState
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal risk_screening"):
		return ErrScreeningState
	}
	return err
}
