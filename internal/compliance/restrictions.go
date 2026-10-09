package compliance

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Compliance restrictions (ADR-037 §3). An APPROVED restricting resolution restricts every PRIMARY_SUBJECT party
// of the case, in the same transaction as the approval; the projection lives in compliance.subject_restrictions.
// A restriction is lifted only when the same case is reopened and resolved again with CLEARED or EDD_CONDITIONS
// (approved); another approved restricting decision supersedes it. Other modules read levels only, through
// Restrictions. Nothing here carries a reason, a note or the case's confidentiality: restrictions from
// RESTRICTED_STR cases apply like any other and must not tip the subject off, so callers tell users only that
// the action is not available on their account.

// Level is a restriction level. The empty level means "not restricted".
type Level string

// Restriction levels, in increasing severity.
const (
	LevelNone       Level = ""
	LevelRestricted Level = "RESTRICTED"
	LevelSuspended  Level = "SUSPENDED"
	LevelOffboarded Level = "OFFBOARDED"
)

var levelRank = map[Level]int{LevelNone: 0, LevelRestricted: 1, LevelSuspended: 2, LevelOffboarded: 3}

// Max returns the more severe of two levels.
func Max(a, b Level) Level {
	if levelRank[b] > levelRank[a] {
		return b
	}
	return a
}

// LevelForDecision maps an approved resolution decision to its restriction level ("" for CLEARED and
// EDD_CONDITIONS, which lift).
func LevelForDecision(decision string) Level {
	switch decision {
	case "RESTRICT":
		return LevelRestricted
	case "SUSPEND":
		return LevelSuspended
	case "OFFBOARD", "CONFIRMED_FRAUD":
		return LevelOffboarded
	}
	return LevelNone
}

// Subject is a party that can be restricted (USER, ORGANISATION, BENEFICIARY, PAYOUT_DESTINATION, CAMPAIGN).
type Subject struct {
	Type string
	ID   string
}

// Restriction events (ids and levels only).
const (
	EvRestrictionApplied = "compliance.restriction_applied"
	EvRestrictionLifted  = "compliance.restriction_lifted"
)

// Restrictions returns the effective restriction level of each subject (the most severe active restriction
// across all cases; LevelNone when unrestricted). Every requested subject is present in the result. It reads
// the projection only and never reveals which case, decision or reason applies.
func (s *Service) Restrictions(ctx context.Context, subjects []Subject) (map[Subject]Level, error) {
	out := make(map[Subject]Level, len(subjects))
	var types, idList []string
	for _, sub := range subjects {
		out[sub] = LevelNone
		if PartyTypes[sub.Type] && ids.Valid(sub.ID) {
			types = append(types, sub.Type)
			idList = append(idList, sub.ID)
		}
	}
	if len(types) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT r.subject_type, r.subject_id::text, r.level FROM compliance.subject_restrictions r
		WHERE r.lifted_at IS NULL AND (r.subject_type, r.subject_id) IN (SELECT * FROM unnest($1::text[], $2::uuid[]))`, types, idList)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sub Subject
		var lvl string
		if err := rows.Scan(&sub.Type, &sub.ID, &lvl); err != nil {
			return nil, err
		}
		out[sub] = Max(out[sub], Level(lvl))
	}
	return out, rows.Err()
}

type activeRestriction struct {
	id    string
	sub   Subject
	level Level
}

// syncRestrictions brings the case's restrictions in line with its newly APPROVED decision (called inside the
// approving transaction, after the case update): lifts what no longer applies and applies what is missing,
// with an audit event and an outbox event for each change.
func (s *Service) syncRestrictions(ctx context.Context, tx pgx.Tx, actor Actor, caseID, decision string, version int) error {
	want := LevelForDecision(decision)
	rows, err := tx.Query(ctx, `SELECT id, subject_type, subject_id::text, level FROM compliance.subject_restrictions
		WHERE case_id = $1 AND lifted_at IS NULL ORDER BY subject_type, subject_id`, caseID)
	if err != nil {
		return err
	}
	active, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (activeRestriction, error) {
		var a activeRestriction
		var lvl string
		err := r.Scan(&a.id, &a.sub.Type, &a.sub.ID, &lvl)
		a.level = Level(lvl)
		return a, err
	})
	if err != nil {
		return err
	}
	primaries := map[Subject]bool{}
	if want != LevelNone {
		links, err := s.currentLinks(ctx, tx, caseID)
		if err != nil {
			return err
		}
		for _, l := range links {
			if l.Role == "PRIMARY_SUBJECT" && PartyTypes[l.SubjectType] {
				primaries[Subject{Type: l.SubjectType, ID: l.SubjectID}] = true
			}
		}
	}
	keep := map[Subject]bool{}
	for _, a := range active {
		reason := ""
		switch {
		case want == LevelNone:
			reason = decision // CLEARED | EDD_CONDITIONS
		case !primaries[a.sub]:
			reason = "SUBJECT_UNLINKED"
		case a.level != want:
			reason = "SUPERSEDED"
		default:
			keep[a.sub] = true
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE compliance.subject_restrictions SET lifted_at = $2, lift_reason = $3, lifted_case_version = $4
			WHERE id = $1`, a.id, s.now(), reason, version); err != nil {
			return err
		}
		if err := s.restrictionChanged(ctx, tx, actor, EvRestrictionLifted, "compliance.restriction.lifted", caseID, a.sub, a.level); err != nil {
			return err
		}
	}
	subs := make([]Subject, 0, len(primaries))
	for sub := range primaries {
		if !keep[sub] {
			subs = append(subs, sub)
		}
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].Type+subs[i].ID < subs[j].Type+subs[j].ID })
	for _, sub := range subs {
		if _, err := tx.Exec(ctx, `INSERT INTO compliance.subject_restrictions (id, subject_type, subject_id, level, case_id, decision,
			applied_case_version, applied_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			ids.New(), sub.Type, sub.ID, string(want), caseID, decision, version, s.now()); err != nil {
			return err
		}
		if err := s.restrictionChanged(ctx, tx, actor, EvRestrictionApplied, "compliance.restriction.applied", caseID, sub, want); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restrictionChanged(ctx context.Context, tx pgx.Tx, actor Actor, event, action, caseID string, sub Subject, lvl Level) error {
	payload := map[string]any{"subject_type": sub.Type, "subject_id": sub.ID, "level": string(lvl), "case_id": caseID}
	if _, err := s.audit(ctx, tx, actor, action, caseID, payload); err != nil {
		return err
	}
	return s.emit(ctx, tx, event, caseID, payload)
}
