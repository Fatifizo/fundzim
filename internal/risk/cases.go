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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Risk cases (ADR-045 §6; brief §32–§34). A case is how a human looks at what the engine (or a colleague) flagged.
// Outcomes never declare fraud and never apply a legal restriction: those belong to compliance, reached by
// escalation (risk.case_escalated → compliance RISK_REVIEW case). A decision is maker-checker: one person proposes,
// another (never linked to the proposer or the subject) approves with a fresh step-up.

// Case statuses.
const (
	CaseOpen          = "OPEN"
	CaseTriage        = "TRIAGE"
	CaseInvestigating = "UNDER_INVESTIGATION"
	CaseAwaitingInfo  = "AWAITING_INFORMATION"
	CaseEscalated     = "ESCALATED"
	CaseDecision      = "DECISION_PENDING"
	CaseResolved      = "RESOLVED"
	CaseClosed        = "CLOSED"
	CaseReopened      = "REOPENED"
)

// Case outcomes.
const (
	OutcomeNoFurtherAction = "NO_FURTHER_ACTION"
	OutcomeFalsePositive   = "FALSE_POSITIVE"
	OutcomeHoldMaintained  = "RISK_CONFIRMED_HOLD_MAINTAINED"
	OutcomeEscalated       = "ESCALATED_TO_COMPLIANCE"
)

// Case events (outbox).
const (
	EvCaseOpened    = "risk.case_opened"
	EvCaseEscalated = "risk.case_escalated"
	EvCaseResolved  = "risk.case_resolved"
)

// CaseEdges mirrors the 'risk_case' machine in app.status_transitions (keep identical; TestCaseEdgesMatchDatabase).
var CaseEdges = map[string][]string{
	"":                {CaseOpen},
	CaseOpen:          {CaseTriage},
	CaseTriage:        {CaseInvestigating, CaseEscalated, CaseDecision},
	CaseReopened:      {CaseTriage},
	CaseInvestigating: {CaseAwaitingInfo, CaseEscalated, CaseDecision},
	CaseAwaitingInfo:  {CaseInvestigating},
	CaseEscalated:     {CaseInvestigating, CaseDecision},
	CaseDecision:      {CaseInvestigating, CaseResolved},
	CaseResolved:      {CaseClosed, CaseReopened},
	CaseClosed:        {CaseReopened},
}

// CaseSLA is the internal target time to a decision by priority. These are ENGINEERING_DEFAULT operational targets
// for queue ordering and overdue monitoring — not regulatory deadlines (none is known: LR-074).
var CaseSLA = map[string]time.Duration{"P1": 24 * time.Hour, "P2": 72 * time.Hour, "P3": 7 * 24 * time.Hour, "P4": 14 * 24 * time.Hour}

var caseTypes = map[string]bool{"TRANSACTION_MONITORING": true, "CAMPAIGN_RISK": true, "ACCOUNT_TAKEOVER": true, "DESTINATION_RISK": true,
	"WITHDRAWAL_RISK": true, "SCREENING_REVIEW": true, "FEE_MANIPULATION": true, "IDENTITY_RISK": true, "OTHER": true}

// CaseSubjects are the subject types a case (and a rule) may be about.
var CaseSubjects = map[string]bool{"USER": true, "ORGANISATION": true, "CAMPAIGN": true, "BENEFICIARY": true, "PAYOUT_DESTINATION": true,
	"DONATION_INTENT": true, "PAYOUT": true, "AUTHENTICATION_SESSION": true}

// StaffChecker tells whether a staff member holds a permission (auth.Service.StaffHasPermission, wired in internal/app).
type StaffChecker interface {
	StaffHasPermission(ctx context.Context, staffID, perm string) (bool, error)
}

// Case is a risk case (staff view).
type Case struct {
	ID                string     `json:"id"`
	CaseNumber        int64      `json:"case_number"`
	CaseType          string     `json:"case_type"`
	SubjectType       string     `json:"subject_type"`
	SubjectID         string     `json:"subject_id"`
	Status            string     `json:"status"`
	Priority          string     `json:"priority"`
	RiskScore         *int       `json:"risk_score"`
	Classification    *string    `json:"classification"`
	RecommendedAction *string    `json:"recommended_action"`
	AssignedTeam      string     `json:"assigned_team"`
	AssignedTo        *string    `json:"assigned_to"`
	DueAt             *time.Time `json:"due_at"`
	ProposedOutcome   *string    `json:"proposed_outcome"`
	ProposedBy        *string    `json:"proposed_by"`
	ProposedAt        *time.Time `json:"proposed_at"`
	ProposedReason    *string    `json:"proposed_reason"`
	DecisionOutcome   *string    `json:"decision_outcome"`
	DecidedBy         *string    `json:"decided_by"`
	DecidedAt         *time.Time `json:"decided_at"`
	DecisionReason    *string    `json:"decision_reason"`
	EscalatedAt       *time.Time `json:"escalated_at"`
	OpenedByType      string     `json:"opened_by_type"`
	OpenedBy          string     `json:"opened_by"`
	Version           int        `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const caseCols = `id, case_number, case_type, subject_type, subject_id, status, priority, risk_score, classification, recommended_action,
	assigned_team, assigned_to, due_at, proposed_outcome, proposed_by, proposed_at, proposed_reason, decision_outcome, decided_by, decided_at,
	decision_reason, escalated_at, opened_by_type, opened_by, version, created_at, updated_at`

func scanCase(r pgx.Row) (Case, error) {
	var c Case
	var score *int16
	err := r.Scan(&c.ID, &c.CaseNumber, &c.CaseType, &c.SubjectType, &c.SubjectID, &c.Status, &c.Priority, &score, &c.Classification,
		&c.RecommendedAction, &c.AssignedTeam, &c.AssignedTo, &c.DueAt, &c.ProposedOutcome, &c.ProposedBy, &c.ProposedAt, &c.ProposedReason,
		&c.DecisionOutcome, &c.DecidedBy, &c.DecidedAt, &c.DecisionReason, &c.EscalatedAt, &c.OpenedByType, &c.OpenedBy, &c.Version,
		&c.CreatedAt, &c.UpdatedAt)
	if score != nil {
		v := int(*score)
		c.RiskScore = &v
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Case{}, ErrNotFound
	}
	return c, err
}

type caseSpec struct {
	CaseType, SubjectType, SubjectID, Priority string
	Score                                      int
	Classification, Recommended                string
	OpenedByType, OpenedBy                     string
}

func caseDedupe(caseType, subjectType, subjectID string) string {
	return caseType + ":" + subjectType + ":" + subjectID
}

// openOrLinkCase returns the open case for the subject and case type, creating it when there is none (one open case per
// subject and type: repeated events link to it). A linked case keeps the most urgent priority and highest score seen.
func (s *Service) openOrLinkCase(ctx context.Context, tx pgx.Tx, c caseSpec, now time.Time) (string, bool, error) {
	key := caseDedupe(c.CaseType, c.SubjectType, c.SubjectID)
	var score, class, rec any
	if c.Classification != "" {
		score, class, rec = c.Score, c.Classification, c.Recommended
	}
	for attempt := 0; attempt < 2; attempt++ {
		var id, prio string
		err := tx.QueryRow(ctx, `SELECT id, priority FROM risk.risk_cases WHERE dedupe_key = $1 AND status NOT IN ('RESOLVED','CLOSED') FOR UPDATE`,
			key).Scan(&id, &prio)
		if err == nil {
			if _, err := tx.Exec(ctx, `UPDATE risk.risk_cases SET
				priority = CASE WHEN $2 < priority THEN $2 ELSE priority END,
				risk_score = CASE WHEN $3::smallint IS NOT NULL AND (risk_score IS NULL OR $3::smallint > risk_score) THEN $3::smallint ELSE risk_score END,
				classification = CASE WHEN $3::smallint IS NOT NULL AND (risk_score IS NULL OR $3::smallint > risk_score) THEN $4 ELSE classification END,
				recommended_action = CASE WHEN $3::smallint IS NOT NULL AND (risk_score IS NULL OR $3::smallint > risk_score) THEN $5 ELSE recommended_action END,
				version = version + 1 WHERE id = $1`, id, c.Priority, score, class, rec); err != nil {
				return "", false, err
			}
			return id, false, riskEvent(ctx, tx, "risk_case", id, "CASE_LINKED", c.OpenedByType, c.OpenedBy, map[string]any{"priority": c.Priority,
				"score": score}, now)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", false, err
		}
		id = ids.New()
		tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_cases (id, case_type, subject_type, subject_id, status, priority, risk_score, classification,
			recommended_action, due_at, opened_by_type, opened_by, dedupe_key) VALUES ($1, $2, $3, $4, 'OPEN', $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (dedupe_key) WHERE status NOT IN ('RESOLVED','CLOSED') DO NOTHING`,
			id, c.CaseType, c.SubjectType, c.SubjectID, c.Priority, score, class, rec, now.Add(CaseSLA[c.Priority]), c.OpenedByType, c.OpenedBy, key)
		if err != nil {
			return "", false, mapDB(err)
		}
		if tag.RowsAffected() == 0 {
			continue // opened concurrently: link to it
		}
		if err := riskEvent(ctx, tx, "risk_case", id, "CASE_OPENED", c.OpenedByType, c.OpenedBy, map[string]any{"case_type": c.CaseType,
			"priority": c.Priority, "score": score}, now); err != nil {
			return "", false, err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_case", AggregateID: id, EventType: EvCaseOpened,
			Payload: map[string]any{"case_id": id, "case_type": c.CaseType, "subject_type": c.SubjectType, "subject_id": c.SubjectID,
				"priority": c.Priority}, OccurredAt: now}); err != nil {
			return "", false, err
		}
		actorType, actorID := "system", ""
		if c.OpenedByType == "STAFF" {
			actorType, actorID = "staff", c.OpenedBy
		}
		return id, true, audit.Record(ctx, tx, audit.Event{Action: "risk.case.opened", ActorType: actorType, ActorID: actorID, TargetType: "risk_case", TargetID: id,
			Metadata: map[string]any{"case_type": c.CaseType, "subject_type": c.SubjectType, "subject_id": c.SubjectID, "priority": c.Priority}})
	}
	return "", false, ErrCaseState
}

// canonicalJSON is the content as hashed: encoded, decoded into generic values (numbers verbatim) and encoded again, so
// object keys are sorted at every depth whatever Go types the content was built from — the same bytes VerifyEvidence
// rebuilds from the stored jsonb.
func canonicalJSON(v map[string]any) ([]byte, [32]byte, error) {
	b, err := json.Marshal(nonNil(v))
	if err != nil {
		return nil, [32]byte{}, err
	}
	b, err = canonicalBytes(b)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return b, sha256.Sum256(b), nil
}

func canonicalBytes(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	return json.Marshal(x)
}

// addEvidence attaches immutable, content-hashed evidence (a reference already attached to the case is not repeated).
func (s *Service) addEvidence(ctx context.Context, tx pgx.Tx, caseID, evType, refType, refID string, content map[string]any, source,
	addedByType, addedBy string, now time.Time) error {
	_, err := s.insertEvidence(ctx, tx, caseID, evType, refType, refID, content, source, addedByType, addedBy, "", now)
	return err
}

func (s *Service) insertEvidence(ctx context.Context, tx pgx.Tx, caseID, evType, refType, refID string, content map[string]any, source,
	addedByType, addedBy, supersedes string, now time.Time) (string, error) {
	b, h, err := canonicalJSON(content)
	if err != nil {
		return "", err
	}
	id := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO risk.risk_case_evidence (id, case_id, evidence_type, reference_type, reference_id, content, content_hash,
		source, added_by_type, added_by, supersedes_id, added_at) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8, $9, $10,
		nullif($11, '')::uuid, $12) ON CONFLICT DO NOTHING`, id, caseID, evType, refType, refID, b, h[:], source, addedByType, addedBy, supersedes, now)
	return id, mapDB(err)
}

// ---- staff operations ----------------------------------------------------------------------------------------------

// NewCase is a manually opened case.
type NewCase struct {
	CaseType    string `json:"case_type"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Priority    string `json:"priority"`
	Reason      string `json:"reason"`
}

// OpenCase opens (or links to) a case manually (risk.case.work).
func (s *Service) OpenCase(ctx context.Context, staffID string, in NewCase) (Case, error) {
	if !caseTypes[in.CaseType] {
		return Case{}, validation("case_type", "INVALID_VALUE")
	}
	if !CaseSubjects[in.SubjectType] || !ids.Valid(in.SubjectID) {
		return Case{}, validation("subject_id", "INVALID_VALUE")
	}
	if priorityRank[in.Priority] == 0 {
		return Case{}, validation("priority", "INVALID_VALUE")
	}
	r, err := reasonText(in.Reason)
	if err != nil {
		return Case{}, err
	}
	if self, err := s.linked(ctx, staffID, subjectUser(in.SubjectType, in.SubjectID)); err != nil {
		return Case{}, err
	} else if self {
		return Case{}, ErrConflictOfInt
	}
	now := s.now()
	var id string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, _, err = s.openOrLinkCase(ctx, tx, caseSpec{CaseType: in.CaseType, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
			Priority: in.Priority, OpenedByType: "STAFF", OpenedBy: staffID}, now)
		if err != nil {
			return err
		}
		_, err = s.insertEvidence(ctx, tx, id, "STAFF_NOTE", "", "", map[string]any{"note": r}, "staff", "STAFF", staffID, "", now)
		return err
	})
	if err != nil {
		return Case{}, err
	}
	return s.getCase(ctx, id)
}

func subjectUser(subjectType, subjectID string) string {
	if subjectType == ownerUser {
		return subjectID
	}
	return ""
}

func (s *Service) getCase(ctx context.Context, id string) (Case, error) {
	if !ids.Valid(id) {
		return Case{}, ErrNotFound
	}
	return scanCase(s.Pool.QueryRow(ctx, `SELECT `+caseCols+` FROM risk.risk_cases WHERE id = $1`, id))
}

// CaseDetail is a case with its evidence, assignments, signal reviews, holds and history.
type CaseDetail struct {
	Case          Case           `json:"case"`
	Evidence      []Evidence     `json:"evidence"`
	Assignments   []Assignment   `json:"assignments"`
	SignalReviews []SignalReview `json:"signal_reviews"`
	Holds         []Hold         `json:"holds"`
	Events        []HistoryEvent `json:"events"`
}

// Evidence is one immutable evidence record.
type Evidence struct {
	ID             string          `json:"id"`
	EvidenceType   string          `json:"evidence_type"`
	ReferenceType  *string         `json:"reference_type"`
	ReferenceID    *string         `json:"reference_id"`
	Content        json.RawMessage `json:"content"`
	ContentHash    string          `json:"content_hash"`
	Source         string          `json:"source"`
	AddedByType    string          `json:"added_by_type"`
	AddedBy        string          `json:"added_by"`
	RetentionClass string          `json:"retention_class"`
	SupersedesID   *string         `json:"supersedes_id"`
	SupersededBy   *string         `json:"superseded_by"`
	AddedAt        time.Time       `json:"added_at"`
}

// Assignment is one assignment record.
type Assignment struct {
	AssignedTo string    `json:"assigned_to"`
	AssignedBy string    `json:"assigned_by"`
	Reason     string    `json:"reason"`
	AssignedAt time.Time `json:"assigned_at"`
}

// SignalReview is a relevance review of a signal (signals are never deleted).
type SignalReview struct {
	ID             string    `json:"id"`
	SignalID       string    `json:"signal_id"`
	CaseID         *string   `json:"case_id"`
	Classification string    `json:"classification"`
	Explanation    string    `json:"explanation"`
	ReviewedBy     string    `json:"reviewed_by"`
	ReviewedAt     time.Time `json:"reviewed_at"`
}

// HistoryEvent is one risk domain history entry.
type HistoryEvent struct {
	EventType  string          `json:"event_type"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Detail     json.RawMessage `json:"detail"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// CaseView returns a case with everything an investigator needs; every view is logged (risk_case_access_log).
func (s *Service) CaseView(ctx context.Context, staffID, id string) (CaseDetail, error) {
	c, err := s.getCase(ctx, id)
	if err != nil {
		return CaseDetail{}, err
	}
	d := CaseDetail{Case: c, Evidence: []Evidence{}, Assignments: []Assignment{}, SignalReviews: []SignalReview{}, Holds: []Hold{}, Events: []HistoryEvent{}}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO risk.risk_case_access_log (id, case_id, staff_id, action, accessed_at) VALUES ($1, $2, $3, 'VIEW_CASE', $4)`,
		ids.New(), id, staffID, s.now()); err != nil {
		return CaseDetail{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT e.id, e.evidence_type, e.reference_type, e.reference_id, e.content, e.content_hash, e.source, e.added_by_type,
		e.added_by, e.retention_class, e.supersedes_id, (SELECT n.id FROM risk.risk_case_evidence n WHERE n.supersedes_id = e.id LIMIT 1), e.added_at
		FROM risk.risk_case_evidence e WHERE e.case_id = $1 ORDER BY e.added_at, e.id LIMIT 500`, id)
	if err != nil {
		return CaseDetail{}, err
	}
	for rows.Next() {
		var e Evidence
		var h []byte
		if err := rows.Scan(&e.ID, &e.EvidenceType, &e.ReferenceType, &e.ReferenceID, &e.Content, &h, &e.Source, &e.AddedByType, &e.AddedBy,
			&e.RetentionClass, &e.SupersedesID, &e.SupersededBy, &e.AddedAt); err != nil {
			rows.Close()
			return CaseDetail{}, err
		}
		e.ContentHash = hexs(h)
		d.Evidence = append(d.Evidence, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CaseDetail{}, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT assigned_to, assigned_by, reason, assigned_at FROM risk.risk_case_assignments WHERE case_id = $1
		ORDER BY assigned_at, id`, id)
	if err != nil {
		return CaseDetail{}, err
	}
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.AssignedTo, &a.AssignedBy, &a.Reason, &a.AssignedAt); err != nil {
			rows.Close()
			return CaseDetail{}, err
		}
		d.Assignments = append(d.Assignments, a)
	}
	rows.Close()
	if d.SignalReviews, err = s.signalReviews(ctx, id); err != nil {
		return CaseDetail{}, err
	}
	if d.Holds, err = s.holdsWhere(ctx, `case_id = $1`, id); err != nil {
		return CaseDetail{}, err
	}
	if d.Events, err = s.history(ctx, "risk_case", id); err != nil {
		return CaseDetail{}, err
	}
	return d, nil
}

func (s *Service) signalReviews(ctx context.Context, caseID string) ([]SignalReview, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, signal_id, case_id, classification, explanation, reviewed_by, reviewed_at
		FROM risk.risk_signal_reviews WHERE case_id = $1 ORDER BY reviewed_at, id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SignalReview{}
	for rows.Next() {
		var r SignalReview
		if err := rows.Scan(&r.ID, &r.SignalID, &r.CaseID, &r.Classification, &r.Explanation, &r.ReviewedBy, &r.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) history(ctx context.Context, aggType, aggID string) ([]HistoryEvent, error) {
	rows, err := s.Pool.Query(ctx, `SELECT event_type, actor_type, actor_id, detail, occurred_at FROM risk.risk_events
		WHERE aggregate_type = $1 AND aggregate_id = $2 ORDER BY occurred_at, id LIMIT 500`, aggType, aggID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEvent{}
	for rows.Next() {
		var e HistoryEvent
		if err := rows.Scan(&e.EventType, &e.ActorType, &e.ActorID, &e.Detail, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CaseFilter filters the case queue.
type CaseFilter struct {
	Status      string
	Priority    string
	CaseType    string
	AssignedTo  string // a staff id, "me" resolved by the handler, or "unassigned"
	SubjectType string
	SubjectID   string
	Limit       int
}

// Cases lists cases, most urgent first (priority, then due date).
func (s *Service) Cases(ctx context.Context, f CaseFilter) ([]Case, error) {
	where, args := []string{"true"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	switch f.Status {
	case "":
	case "OPEN_ANY":
		where = append(where, "status NOT IN ('RESOLVED','CLOSED')")
	default:
		add("status = ?", f.Status)
	}
	if f.Priority != "" {
		add("priority = ?", f.Priority)
	}
	if f.CaseType != "" {
		add("case_type = ?", f.CaseType)
	}
	switch {
	case f.AssignedTo == "unassigned":
		where = append(where, "assigned_to IS NULL")
	case ids.Valid(f.AssignedTo):
		add("assigned_to = ?", f.AssignedTo)
	}
	if f.SubjectType != "" && ids.Valid(f.SubjectID) {
		add("subject_type = ?", f.SubjectType)
		add("subject_id = ?", f.SubjectID)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	args = append(args, f.Limit)
	rows, err := s.Pool.Query(ctx, `SELECT `+caseCols+` FROM risk.risk_cases WHERE `+strings.Join(where, " AND ")+`
		ORDER BY (status IN ('RESOLVED','CLOSED')), priority, due_at NULLS LAST, created_at LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Case{}
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// lockCase loads a case FOR UPDATE and checks the caller's expected version (0: not checked).
func lockCase(ctx context.Context, tx pgx.Tx, id string, expect int) (Case, error) {
	if !ids.Valid(id) {
		return Case{}, ErrNotFound
	}
	c, err := scanCase(tx.QueryRow(ctx, `SELECT `+caseCols+` FROM risk.risk_cases WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return Case{}, err
	}
	if expect > 0 && c.Version != expect {
		return Case{}, ErrStale
	}
	return c, nil
}

// conflicted: the actor is (linked to) the case's USER subject.
func (s *Service) conflicted(ctx context.Context, staffID string, c Case) error {
	self, err := s.linked(ctx, staffID, subjectUser(c.SubjectType, c.SubjectID))
	if err != nil {
		return err
	}
	if self {
		return ErrConflictOfInt
	}
	return nil
}

// CaseOp is a staff action on a case.
type CaseOp struct {
	StaffID         string
	StepUpAt        time.Time
	CaseID          string
	ExpectedVersion int
	Reason          string
}

func (s *Service) caseOp(ctx context.Context, op CaseOp, fn func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error) (Case, error) {
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := lockCase(ctx, tx, op.CaseID, op.ExpectedVersion)
		if err != nil {
			return err
		}
		if err := s.conflicted(ctx, op.StaffID, c); err != nil {
			return err
		}
		return fn(ctx, tx, c, now)
	})
	if err != nil {
		return Case{}, err
	}
	return s.getCase(ctx, op.CaseID)
}

func setStatus(ctx context.Context, tx pgx.Tx, c Case, to, extra string, args ...any) error {
	ok := false
	for _, t := range CaseEdges[c.Status] {
		ok = ok || t == to
	}
	if !ok {
		return ErrCaseState
	}
	q := `UPDATE risk.risk_cases SET status = $2, version = version + 1` + extra + ` WHERE id = $1`
	_, err := tx.Exec(ctx, q, append([]any{c.ID, to}, args...)...)
	return mapDB(err)
}

// Assign assigns the case (default: to the caller). The assignee must hold risk.case.work and is never the subject or
// linked to it.
func (s *Service) Assign(ctx context.Context, op CaseOp, assignee string) (Case, error) {
	if assignee == "" {
		assignee = op.StaffID
	}
	if !ids.Valid(assignee) {
		return Case{}, validation("assignee_id", "INVALID_FORMAT")
	}
	if op.Reason == "" {
		op.Reason = "assigned"
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(op.Reason)); n < 3 || n > 500 {
		return Case{}, validation("reason", "INVALID_LENGTH")
	}
	if assignee != op.StaffID {
		if s.Staff == nil {
			return Case{}, validation("assignee_id", "NOT_ELIGIBLE")
		}
		ok, err := s.Staff.StaffHasPermission(ctx, assignee, "risk.case.work")
		if err != nil {
			return Case{}, err
		}
		if !ok {
			return Case{}, validation("assignee_id", "NOT_ELIGIBLE")
		}
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if c.Status == CaseResolved || c.Status == CaseClosed {
			return ErrCaseState
		}
		if err := s.conflicted(ctx, assignee, c); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_cases SET assigned_to = $2, version = version + 1 WHERE id = $1`, c.ID, assignee); err != nil {
			return mapDB(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO risk.risk_case_assignments (id, case_id, assigned_to, assigned_by, reason, assigned_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), c.ID, assignee, op.StaffID, strings.TrimSpace(op.Reason), now); err != nil {
			return err
		}
		if c.Status == CaseOpen || c.Status == CaseReopened {
			if err := setStatus(ctx, tx, Case{ID: c.ID, Status: c.Status}, CaseTriage, ""); err != nil {
				return err
			}
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "CASE_ASSIGNED", "STAFF", op.StaffID, map[string]any{"assigned_to": assignee}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.assigned", TargetType: "risk_case", TargetID: c.ID,
			Metadata: map[string]any{"assigned_to": assignee}})
	})
}

// manualTransitions are the status changes a case worker makes directly (decisions and escalation have their own steps).
var manualTransitions = map[string]bool{
	CaseOpen + ">" + CaseTriage: true, CaseTriage + ">" + CaseInvestigating: true, CaseReopened + ">" + CaseTriage: true,
	CaseInvestigating + ">" + CaseAwaitingInfo: true, CaseAwaitingInfo + ">" + CaseInvestigating: true,
	CaseEscalated + ">" + CaseInvestigating: true, CaseResolved + ">" + CaseClosed: true,
}

// Transition moves a case along a working edge (triage, investigate, await information, close).
func (s *Service) Transition(ctx context.Context, op CaseOp, to string) (Case, error) {
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if !manualTransitions[c.Status+">"+to] {
			return ErrCaseState
		}
		if err := setStatus(ctx, tx, c, to, ""); err != nil {
			return err
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "CASE_"+to, "STAFF", op.StaffID, map[string]any{"from": c.Status, "reason": op.Reason}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.transitioned", TargetType: "risk_case", TargetID: c.ID,
			Metadata: map[string]any{"from": c.Status, "to": to}})
	})
}

// NoteInput is a staff note or evidence reference. A correction names the evidence it supersedes (never an overwrite).
type NoteInput struct {
	EvidenceType  string `json:"evidence_type"` // STAFF_NOTE (default) or a *_REF type
	Note          string `json:"note"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	SupersedesID  string `json:"supersedes_id"`
}

var staffEvidenceTypes = map[string]bool{"STAFF_NOTE": true, "VERIFICATION_REF": true, "CAMPAIGN_REF": true, "PAYMENT_REF": true, "PAYOUT_REF": true,
	"DOCUMENT_REF": true, "PROVIDER_EVIDENCE": true}

var refTypeRE = codeRE

// AddEvidence adds a staff note or an evidence reference (immutable, hashed). Notes never carry identity documents or
// full account numbers (the UI says so; the length is bounded).
func (s *Service) AddEvidence(ctx context.Context, op CaseOp, in NoteInput) (Case, string, error) {
	if in.EvidenceType == "" {
		in.EvidenceType = "STAFF_NOTE"
	}
	if !staffEvidenceTypes[in.EvidenceType] {
		return Case{}, "", validation("evidence_type", "INVALID_VALUE")
	}
	in.Note = strings.TrimSpace(in.Note)
	if n := utf8.RuneCountInString(in.Note); n < 3 || n > 4000 {
		return Case{}, "", validation("note", "INVALID_LENGTH")
	}
	if in.EvidenceType != "STAFF_NOTE" && (!refTypeRE.MatchString(strings.ToUpper(in.ReferenceType)) || in.ReferenceID == "" || len(in.ReferenceID) > 200) {
		return Case{}, "", validation("reference_id", "INVALID_VALUE")
	}
	if in.SupersedesID != "" && !ids.Valid(in.SupersedesID) {
		return Case{}, "", validation("supersedes_id", "INVALID_FORMAT")
	}
	var evID string
	c, err := s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if c.Status == CaseClosed {
			return ErrCaseState
		}
		if in.SupersedesID != "" {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM risk.risk_case_evidence WHERE id = $1 AND case_id = $2`, in.SupersedesID, c.ID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return validation("supersedes_id", "NOT_FOUND")
			}
		}
		refType := ""
		if in.ReferenceType != "" {
			refType = strings.ToLower(in.ReferenceType)
		}
		var err error
		evID, err = s.insertEvidence(ctx, tx, c.ID, in.EvidenceType, refType, in.ReferenceID, map[string]any{"note": in.Note}, "staff", "STAFF",
			op.StaffID, in.SupersedesID, now)
		if err != nil {
			return err
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "EVIDENCE_ADDED", "STAFF", op.StaffID, map[string]any{"evidence_id": evID,
			"evidence_type": in.EvidenceType, "supersedes_id": in.SupersedesID}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.evidence_added", TargetType: "risk_case", TargetID: c.ID,
			Metadata: map[string]any{"evidence_id": evID, "evidence_type": in.EvidenceType}})
	})
	return c, evID, err
}

// ReviewSignal records a relevance review of a signal attached to the case. Signals are never deleted: a false
// positive stays, labelled (brief §47).
func (s *Service) ReviewSignal(ctx context.Context, op CaseOp, signalID, classification, explanation string) (Case, error) {
	switch classification {
	case "FALSE_POSITIVE", "RELEVANT", "INCONCLUSIVE":
	default:
		return Case{}, validation("classification", "INVALID_VALUE")
	}
	explanation = strings.TrimSpace(explanation)
	if n := utf8.RuneCountInString(explanation); n < 5 || n > 2000 {
		return Case{}, validation("explanation", "INVALID_LENGTH")
	}
	if !ids.Valid(signalID) {
		return Case{}, validation("signal_id", "INVALID_FORMAT")
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if c.Status == CaseClosed {
			return ErrCaseState
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM risk.risk_case_evidence WHERE case_id = $1 AND evidence_type = 'SIGNAL' AND reference_id = $2`,
			c.ID, signalID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return validation("signal_id", "NOT_IN_CASE")
		}
		tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_signal_reviews (id, signal_id, case_id, classification, explanation, reviewed_by, reviewed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT ON CONSTRAINT uq_risk_signal_reviews DO NOTHING`,
			ids.New(), signalID, c.ID, classification, explanation, op.StaffID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.Conflict, "RISK_SIGNAL_ALREADY_REVIEWED", "This signal was already reviewed in this case.")
		}
		if err := riskEvent(ctx, tx, "risk_signal", signalID, "SIGNAL_REVIEWED", "STAFF", op.StaffID, map[string]any{"case_id": c.ID,
			"classification": classification}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.signal.reviewed", TargetType: "risk_signal", TargetID: signalID,
			Metadata: map[string]any{"case_id": c.ID, "classification": classification}})
	})
}

// Escalate hands the case to compliance (risk.case_escalated → a compliance RISK_REVIEW case). Only compliance may
// apply a legal restriction.
func (s *Service) Escalate(ctx context.Context, op CaseOp) (Case, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return Case{}, err
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if err := setStatus(ctx, tx, c, CaseEscalated, `, escalated_at = $3, assigned_team = 'COMPLIANCE'`, now); err != nil {
			return err
		}
		return s.escalated(ctx, tx, c, "STAFF", op.StaffID, r, now)
	})
}

func (s *Service) escalated(ctx context.Context, tx pgx.Tx, c Case, actorType, actorID, reason string, now time.Time) error {
	if err := riskEvent(ctx, tx, "risk_case", c.ID, "CASE_ESCALATED", actorType, actorID, map[string]any{"reason": reason}, now); err != nil {
		return err
	}
	if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_case", AggregateID: c.ID, EventType: EvCaseEscalated,
		Payload: map[string]any{"case_id": c.ID, "case_number": c.CaseNumber, "case_type": c.CaseType, "subject_type": c.SubjectType,
			"subject_id": c.SubjectID, "priority": c.Priority, "reason_code": "RISK_CASE_ESCALATED"}, OccurredAt: now}); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{Action: "risk.case.escalated", TargetType: "risk_case", TargetID: c.ID, Reason: reason})
}

var outcomes = map[string]bool{OutcomeNoFurtherAction: true, OutcomeFalsePositive: true, OutcomeHoldMaintained: true, OutcomeEscalated: true}

// ProposeDecision is the maker step (→ DECISION_PENDING).
func (s *Service) ProposeDecision(ctx context.Context, op CaseOp, outcome string) (Case, error) {
	if !outcomes[outcome] {
		return Case{}, validation("outcome", "INVALID_VALUE")
	}
	r, err := reasonText(op.Reason)
	if err != nil {
		return Case{}, err
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if outcome == OutcomeEscalated && c.EscalatedAt == nil {
			return errs.New(errs.Conflict, "RISK_CASE_NOT_ESCALATED", "Escalate the case to compliance before resolving it as escalated.")
		}
		if err := setStatus(ctx, tx, c, CaseDecision, `, proposed_outcome = $3, proposed_by = $4, proposed_at = $5, proposed_reason = $6`,
			outcome, op.StaffID, now, r); err != nil {
			return err
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "DECISION_PROPOSED", "STAFF", op.StaffID, map[string]any{"outcome": outcome, "reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.decision_proposed", TargetType: "risk_case", TargetID: c.ID, Reason: r,
			Metadata: map[string]any{"outcome": outcome}})
	})
}

// DecideCase is the checker step (risk.case.approve; fresh step-up; never the proposer, linked to them or to the
// subject). Approval resolves the case and applies the outcome to its holds: NO_FURTHER_ACTION and FALSE_POSITIVE
// release them (the proposer requested, the checker released); RISK_CONFIRMED_HOLD_MAINTAINED and
// ESCALATED_TO_COMPLIANCE replace temporary automated holds with manual holds without expiry. Returning the
// proposal sends the case back to investigation.
func (s *Service) DecideCase(ctx context.Context, op CaseOp, approve bool) (Case, error) {
	if op.StepUpAt.IsZero() {
		return Case{}, ErrStepUp
	}
	r, err := reasonText(op.Reason)
	if err != nil {
		return Case{}, err
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		if c.Status != CaseDecision || c.ProposedOutcome == nil || c.ProposedBy == nil {
			return ErrCaseState
		}
		if self, err := s.linked(ctx, op.StaffID, *c.ProposedBy); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		if !approve {
			if err := setStatus(ctx, tx, c, CaseInvestigating, `, proposed_outcome = NULL, proposed_by = NULL, proposed_at = NULL, proposed_reason = NULL`); err != nil {
				return err
			}
			if err := riskEvent(ctx, tx, "risk_case", c.ID, "DECISION_RETURNED", "STAFF", op.StaffID, map[string]any{"proposed": *c.ProposedOutcome,
				"reason": r}, now); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Action: "risk.case.decision_returned", TargetType: "risk_case", TargetID: c.ID, Reason: r})
		}
		outcome := *c.ProposedOutcome
		if err := setStatus(ctx, tx, c, CaseResolved, `, decision_outcome = $3, decided_by = $4, decided_at = $5, decision_reason = $6,
			decided_step_up_at = $7`, outcome, op.StaffID, now, r, minTime(op.StepUpAt.UTC(), now)); err != nil {
			return err
		}
		affected, err := s.applyOutcomeToHolds(ctx, tx, c, outcome, *c.ProposedBy, op.StaffID, op.StepUpAt, r, now)
		if err != nil {
			return err
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "CASE_RESOLVED", "STAFF", op.StaffID, map[string]any{"outcome": outcome, "reason": r,
			"holds": affected}, now); err != nil {
			return err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_case", AggregateID: c.ID, EventType: EvCaseResolved,
			Payload: map[string]any{"case_id": c.ID, "case_type": c.CaseType, "subject_type": c.SubjectType, "subject_id": c.SubjectID,
				"outcome": outcome}, OccurredAt: now}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.resolved", TargetType: "risk_case", TargetID: c.ID, Reason: r,
			Metadata: map[string]any{"outcome": outcome, "holds": affected}})
	})
}

// Reopen reopens a resolved or closed case (the previous decision stays in the history).
func (s *Service) Reopen(ctx context.Context, op CaseOp) (Case, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return Case{}, err
	}
	return s.caseOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c Case, now time.Time) error {
		err := setStatus(ctx, tx, c, CaseReopened, `, proposed_outcome = NULL, proposed_by = NULL, proposed_at = NULL, proposed_reason = NULL,
			decision_outcome = NULL, decided_by = NULL, decided_at = NULL, decision_reason = NULL, decided_step_up_at = NULL, due_at = $3`,
			now.Add(CaseSLA[c.Priority]))
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return errs.New(errs.Conflict, "RISK_CASE_DUPLICATE_OPEN", "Another open case covers this subject and case type; work that case instead.")
		}
		if err != nil {
			return err
		}
		prev := ""
		if c.DecisionOutcome != nil {
			prev = *c.DecisionOutcome
		}
		if err := riskEvent(ctx, tx, "risk_case", c.ID, "CASE_REOPENED", "STAFF", op.StaffID, map[string]any{"previous_outcome": prev, "reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.case.reopened", TargetType: "risk_case", TargetID: c.ID, Reason: r})
	})
}

// EvidenceRecord returns one evidence record (logged as VIEW_EVIDENCE).
func (s *Service) EvidenceRecord(ctx context.Context, staffID, caseID, evidenceID string) (Evidence, error) {
	if !ids.Valid(caseID) || !ids.Valid(evidenceID) {
		return Evidence{}, ErrNotFound
	}
	var e Evidence
	var h []byte
	err := s.Pool.QueryRow(ctx, `SELECT id, evidence_type, reference_type, reference_id, content, content_hash, source, added_by_type, added_by,
		retention_class, supersedes_id, (SELECT n.id FROM risk.risk_case_evidence n WHERE n.supersedes_id = e.id LIMIT 1), added_at
		FROM risk.risk_case_evidence e WHERE id = $1 AND case_id = $2`, evidenceID, caseID).Scan(&e.ID, &e.EvidenceType, &e.ReferenceType,
		&e.ReferenceID, &e.Content, &h, &e.Source, &e.AddedByType, &e.AddedBy, &e.RetentionClass, &e.SupersedesID, &e.SupersededBy, &e.AddedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Evidence{}, ErrNotFound
	}
	if err != nil {
		return Evidence{}, err
	}
	e.ContentHash = hexs(h)
	_, err = s.Pool.Exec(ctx, `INSERT INTO risk.risk_case_access_log (id, case_id, staff_id, action, accessed_at) VALUES ($1, $2, $3, 'VIEW_EVIDENCE', $4)`,
		ids.New(), caseID, staffID, s.now())
	return e, err
}

// VerifyEvidence recomputes an evidence record's content hash (integrity check used by reconciliation and tests).
func VerifyEvidence(e Evidence) bool {
	b, err := canonicalBytes(e.Content)
	if err != nil {
		return false
	}
	h := sha256.Sum256(b)
	return hexs(h[:]) == e.ContentHash
}

func hexs(b []byte) string {
	const hx = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[2*i], out[2*i+1] = hx[c>>4], hx[c&15]
	}
	return string(out)
}
