package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Support cases (brief §38–§42). A support case is a customer or internal enquiry; it is not a risk investigation or a
// compliance case and never holds risk evidence, compliance notes, KYC documents or account numbers (the UI says so;
// support staff get no implicit access to those). Escalation hands the question to another team by reference while the
// support case keeps its owner and history.

// SupportEdges mirrors the 'ops_support_case' machine (keep identical; TestOpsEdgesMatchDatabase).
var SupportEdges = map[string][]string{
	"":                     {"OPEN"},
	"OPEN":                 {"TRIAGE"},
	"REOPENED":             {"TRIAGE"},
	"TRIAGE":               {"IN_PROGRESS", "ESCALATED", "RESOLVED"},
	"IN_PROGRESS":          {"WAITING_FOR_CUSTOMER", "ESCALATED", "RESOLVED"},
	"WAITING_FOR_CUSTOMER": {"IN_PROGRESS", "RESOLVED"},
	"ESCALATED":            {"IN_PROGRESS", "RESOLVED"},
	"RESOLVED":             {"CLOSED", "REOPENED"},
	"CLOSED":               {"REOPENED"},
}

var supportCategories = map[string]bool{"GENERAL": true, "ACCOUNT": true, "CAMPAIGN": true, "DONATION": true, "VERIFICATION": true, "WITHDRAWAL": true,
	"TECHNICAL": true}
var escalationTargets = map[string]bool{"CAMPAIGN_MODERATION": true, "VERIFICATION": true, "RISK_COMPLIANCE": true, "PAYMENTS": true, "PAYOUTS": true,
	"FINANCIAL_OPERATIONS": true, "TECHNICAL_OPERATIONS": true}
var priorities = map[string]bool{"P1": true, "P2": true, "P3": true, "P4": true}

// SupportCase is a support case.
type SupportCase struct {
	ID                   string     `json:"id"`
	CaseNumber           int64      `json:"case_number"`
	Subject              string     `json:"subject"`
	Category             string     `json:"category"`
	Priority             string     `json:"priority"`
	Status               string     `json:"status"`
	RequesterUserID      *string    `json:"requester_user_id"`
	AssignedTo           *string    `json:"assigned_to"`
	RelatedCampaignID    *string    `json:"related_campaign_id"`
	RelatedReferenceType *string    `json:"related_reference_type"`
	RelatedReferenceID   *string    `json:"related_reference_id"`
	EscalatedTo          *string    `json:"escalated_to"`
	EscalationReference  *string    `json:"escalation_reference"`
	Resolution           *string    `json:"resolution"`
	ResolvedAt           *time.Time `json:"resolved_at"`
	DueAt                *time.Time `json:"due_at"`
	CreatedBy            string     `json:"created_by"`
	Version              int        `json:"version"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

const supportCols = `id, case_number, subject, category, priority, status, requester_user_id::text, assigned_to::text, related_campaign_id::text,
	related_reference_type, related_reference_id, escalated_to, escalation_reference, resolution, resolved_at, due_at, created_by::text, version,
	created_at, updated_at`

func scanSupport(r pgx.Row) (SupportCase, error) {
	var c SupportCase
	err := r.Scan(&c.ID, &c.CaseNumber, &c.Subject, &c.Category, &c.Priority, &c.Status, &c.RequesterUserID, &c.AssignedTo, &c.RelatedCampaignID,
		&c.RelatedReferenceType, &c.RelatedReferenceID, &c.EscalatedTo, &c.EscalationReference, &c.Resolution, &c.ResolvedAt, &c.DueAt, &c.CreatedBy,
		&c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportCase{}, ErrNotFound
	}
	return c, err
}

// NewSupportCase opens a support case.
type NewSupportCase struct {
	Subject              string `json:"subject"`
	Category             string `json:"category"`
	Priority             string `json:"priority"`
	RequesterUserID      string `json:"requester_user_id"`
	RelatedCampaignID    string `json:"related_campaign_id"`
	RelatedReferenceType string `json:"related_reference_type"`
	RelatedReferenceID   string `json:"related_reference_id"`
	Note                 string `json:"note"`
}

var refTypes = map[string]bool{"DONATION_INTENT": true, "PAYMENT": true, "PAYOUT_REQUEST": true, "KYC_CASE": true, "KYB_CASE": true, "BENEFICIARY": true,
	"CAMPAIGN_REPORT": true, "OTHER": true}

// OpenSupportCase opens a case (ops.support.work). The due date comes from an approved SUPPORT_RESOLUTION target, if
// any (none is seeded: no target, no due date).
func (s *Service) OpenSupportCase(ctx context.Context, staffID string, in NewSupportCase) (SupportCase, error) {
	subject, err := text("subject", in.Subject, 3, 200)
	if err != nil {
		return SupportCase{}, err
	}
	if !supportCategories[in.Category] {
		return SupportCase{}, validation("category", "INVALID_VALUE")
	}
	if in.Priority == "" {
		in.Priority = "P3"
	}
	if !priorities[in.Priority] {
		return SupportCase{}, validation("priority", "INVALID_VALUE")
	}
	for f, v := range map[string]string{"requester_user_id": in.RequesterUserID, "related_campaign_id": in.RelatedCampaignID} {
		if v != "" && !ids.Valid(v) {
			return SupportCase{}, validation(f, "INVALID_FORMAT")
		}
	}
	if (in.RelatedReferenceType == "") != (in.RelatedReferenceID == "") || (in.RelatedReferenceType != "" && !refTypes[in.RelatedReferenceType]) ||
		len(in.RelatedReferenceID) > 200 {
		return SupportCase{}, validation("related_reference_type", "INVALID_VALUE")
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 4000 {
		return SupportCase{}, validation("note", "INVALID_LENGTH")
	}
	now := s.now()
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		due, err := slaDue(ctx, tx, "SUPPORT_RESOLUTION", now)
		if err != nil {
			return err
		}
		var number int64
		if err := tx.QueryRow(ctx, `INSERT INTO app.ops_support_cases (id, subject, category, priority, status, requester_user_id, related_campaign_id,
			related_reference_type, related_reference_id, due_at, created_by) VALUES ($1, $2, $3, $4, 'OPEN', nullif($5, '')::uuid, nullif($6, '')::uuid,
			nullif($7, ''), nullif($8, ''), $9, $10) RETURNING case_number`, id, subject, in.Category, in.Priority, in.RequesterUserID, in.RelatedCampaignID,
			in.RelatedReferenceType, in.RelatedReferenceID, due, staffID).Scan(&number); err != nil {
			return mapDB(err)
		}
		if err := supportEvent(ctx, tx, id, "OPENED", staffID, "", "OPEN", note, nil, now); err != nil {
			return err
		}
		// Stage 15: the requester's notification (ids and the case number only; never the subject or notes)
		if err := emit(ctx, tx, "support_case", id, EvSupportCaseOpened, map[string]any{"case_id": id, "case_number": number,
			"requester_user_id": in.RequesterUserID}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.support_case.opened", TargetType: "ops_support_case", TargetID: id,
			Metadata: map[string]any{"category": in.Category, "priority": in.Priority}})
	})
	if err != nil {
		return SupportCase{}, err
	}
	return s.SupportCase(ctx, id)
}

// SupportCase returns one case.
func (s *Service) SupportCase(ctx context.Context, id string) (SupportCase, error) {
	if !ids.Valid(id) {
		return SupportCase{}, ErrNotFound
	}
	return scanSupport(s.Pool.QueryRow(ctx, `SELECT `+supportCols+` FROM app.ops_support_cases WHERE id = $1`, id))
}

// SupportFilter filters the support queue.
type SupportFilter struct {
	Status     string // a status or OPEN_ANY
	Category   string
	Priority   string
	AssignedTo string // a staff id or "unassigned"
	Query      string // case number or subject fragment
	Limit      int
	Cursor     string // "<created_at RFC3339Nano>|<id>" (newest first)
}

// SupportCases lists cases newest first with a keyset cursor.
func (s *Service) SupportCases(ctx context.Context, f SupportFilter) ([]SupportCase, string, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
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
	if f.Category != "" {
		add("category = ?", f.Category)
	}
	if f.Priority != "" {
		add("priority = ?", f.Priority)
	}
	switch {
	case f.AssignedTo == "unassigned":
		where = append(where, "assigned_to IS NULL")
	case ids.Valid(f.AssignedTo):
		add("assigned_to = ?::uuid", f.AssignedTo)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		if n, err := strconv.ParseInt(q, 10, 64); err == nil {
			add("case_number = ?", n)
		} else {
			add("subject ILIKE '%' || ? || '%'", strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q))
		}
	}
	if f.Cursor != "" {
		parts := strings.SplitN(f.Cursor, "|", 2)
		at, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil || len(parts) != 2 || !ids.Valid(parts[1]) {
			return nil, "", validation("cursor", "INVALID_FORMAT")
		}
		args = append(args, at, parts[1])
		where = append(where, "(created_at, id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+"::uuid)")
	}
	args = append(args, f.Limit+1)
	rows, err := s.Pool.Query(ctx, `SELECT `+supportCols+` FROM app.ops_support_cases WHERE `+strings.Join(where, " AND ")+`
		ORDER BY created_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []SupportCase{}
	for rows.Next() {
		c, err := scanSupport(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return out, next, nil
}

// SupportOp is a staff action on a support case.
type SupportOp struct {
	StaffID         string
	CaseID          string
	ExpectedVersion int
	Note            string
}

func (s *Service) supportOp(ctx context.Context, op SupportOp, fn func(ctx context.Context, tx pgx.Tx, c SupportCase, now time.Time) error) (SupportCase, error) {
	if !ids.Valid(op.CaseID) {
		return SupportCase{}, ErrNotFound
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := scanSupport(tx.QueryRow(ctx, `SELECT `+supportCols+` FROM app.ops_support_cases WHERE id = $1 FOR UPDATE`, op.CaseID))
		if err != nil {
			return err
		}
		if op.ExpectedVersion > 0 && c.Version != op.ExpectedVersion {
			return ErrStale
		}
		return fn(ctx, tx, c, now)
	})
	if err != nil {
		return SupportCase{}, err
	}
	return s.SupportCase(ctx, op.CaseID)
}

// AssignSupportCase assigns a case (default: the caller). The assignee must hold ops.support.work.
func (s *Service) AssignSupportCase(ctx context.Context, op SupportOp, assignee string) (SupportCase, error) {
	if assignee == "" {
		assignee = op.StaffID
	}
	if !ids.Valid(assignee) {
		return SupportCase{}, validation("assignee_id", "INVALID_FORMAT")
	}
	if s.Perms != nil {
		ps, err := s.Perms.EffectivePermissions(ctx, assignee)
		if err != nil {
			return SupportCase{}, err
		}
		if !contains(ps, PermSupportWork) {
			return SupportCase{}, ErrAssigneeDenied
		}
	}
	return s.supportOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c SupportCase, now time.Time) error {
		if c.Status == "RESOLVED" || c.Status == "CLOSED" {
			return ErrState
		}
		set := `assigned_to = $2, version = version + 1`
		to := ""
		if c.Status == "OPEN" || c.Status == "REOPENED" {
			set, to = `assigned_to = $2, status = 'TRIAGE', version = version + 1`, "TRIAGE"
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_support_cases SET `+set+` WHERE id = $1`, c.ID, assignee); err != nil {
			return mapDB(err)
		}
		if err := supportEvent(ctx, tx, c.ID, "ASSIGNED", op.StaffID, c.Status, to, op.Note, map[string]any{"assigned_to": assignee}, now); err != nil {
			return err
		}
		if assignee != op.StaffID { // nobody is notified of taking a case themselves
			if err := emit(ctx, tx, "support_case", c.ID, EvSupportCaseAssigned, map[string]any{"case_id": c.ID, "case_number": c.CaseNumber,
				"assigned_to": assignee}, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.support_case.assigned", TargetType: "ops_support_case", TargetID: c.ID,
			Metadata: map[string]any{"assigned_to": assignee}})
	})
}

// TransitionSupportCase moves a case along its lifecycle. RESOLVED needs a resolution (the note); CLOSED and
// REOPENED need a reason.
func (s *Service) TransitionSupportCase(ctx context.Context, op SupportOp, to string) (SupportCase, error) {
	note := strings.TrimSpace(op.Note)
	if len([]rune(note)) > 2000 {
		return SupportCase{}, validation("note", "INVALID_LENGTH")
	}
	if (to == "RESOLVED" || to == "CLOSED" || to == "REOPENED") && len([]rune(note)) < 5 {
		return SupportCase{}, validation("note", "REQUIRED")
	}
	if to == "ESCALATED" {
		return SupportCase{}, validation("to", "USE_ESCALATE")
	}
	return s.supportOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c SupportCase, now time.Time) error {
		if !allowed(SupportEdges, c.Status, to) {
			return ErrState
		}
		var err error
		switch to {
		case "RESOLVED":
			_, err = tx.Exec(ctx, `UPDATE app.ops_support_cases SET status = 'RESOLVED', resolution = $2, resolved_at = $3, version = version + 1 WHERE id = $1`,
				c.ID, note, now)
		case "REOPENED":
			// the previous resolution stays in the history; the case can be resolved again
			_, err = tx.Exec(ctx, `UPDATE app.ops_support_cases SET status = 'REOPENED', resolution = NULL, resolved_at = NULL, version = version + 1 WHERE id = $1`, c.ID)
		default:
			_, err = tx.Exec(ctx, `UPDATE app.ops_support_cases SET status = $2, version = version + 1 WHERE id = $1`, c.ID, to)
		}
		if err != nil {
			return mapDB(err)
		}
		if err := supportEvent(ctx, tx, c.ID, "STATUS_"+to, op.StaffID, c.Status, to, note, nil, now); err != nil {
			return err
		}
		req := ""
		if c.RequesterUserID != nil {
			req = *c.RequesterUserID
		}
		if err := emit(ctx, tx, "support_case", c.ID, EvSupportCaseStatusChanged, map[string]any{"case_id": c.ID, "case_number": c.CaseNumber,
			"requester_user_id": req, "from": c.Status, "to": to}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.support_case.transitioned", TargetType: "ops_support_case", TargetID: c.ID,
			Metadata: map[string]any{"from": c.Status, "to": to}})
	})
}

// EscalateSupportCase hands the question to another team (by reference: e.g. a compliance case id or a campaign
// review). The support case keeps its owner and history.
func (s *Service) EscalateSupportCase(ctx context.Context, op SupportOp, target, reference string) (SupportCase, error) {
	if !escalationTargets[target] {
		return SupportCase{}, validation("escalated_to", "INVALID_VALUE")
	}
	if len(reference) > 200 {
		return SupportCase{}, validation("escalation_reference", "INVALID_LENGTH")
	}
	note, err := text("note", op.Note, 5, 2000)
	if err != nil {
		return SupportCase{}, err
	}
	return s.supportOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c SupportCase, now time.Time) error {
		if !allowed(SupportEdges, c.Status, "ESCALATED") {
			return ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_support_cases SET status = 'ESCALATED', escalated_to = $2, escalation_reference = nullif($3, ''),
			version = version + 1 WHERE id = $1`, c.ID, target, strings.TrimSpace(reference)); err != nil {
			return mapDB(err)
		}
		if err := supportEvent(ctx, tx, c.ID, "ESCALATED", op.StaffID, c.Status, "ESCALATED", note, map[string]any{"escalated_to": target,
			"reference": reference}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.support_case.escalated", TargetType: "ops_support_case", TargetID: c.ID,
			Metadata: map[string]any{"escalated_to": target}})
	})
}

// AddSupportNote appends an internal note (never shown to the requester).
func (s *Service) AddSupportNote(ctx context.Context, op SupportOp) (SupportCase, error) {
	note, err := text("note", op.Note, 3, 4000)
	if err != nil {
		return SupportCase{}, err
	}
	return s.supportOp(ctx, op, func(ctx context.Context, tx pgx.Tx, c SupportCase, now time.Time) error {
		if c.Status == "CLOSED" {
			return ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_support_cases SET version = version + 1 WHERE id = $1`, c.ID); err != nil {
			return err
		}
		return supportEvent(ctx, tx, c.ID, "NOTE_ADDED", op.StaffID, "", "", note, nil, now)
	})
}

// SupportHistory returns a case's history.
func (s *Service) SupportHistory(ctx context.Context, id string) ([]HistoryEntry, error) {
	return s.history(ctx, `SELECT event_type, actor_id::text, coalesce(from_status, ''), coalesce(to_status, ''), coalesce(note, ''), occurred_at
		FROM app.ops_support_events WHERE case_id = $1 ORDER BY occurred_at, id`, id)
}

func supportEvent(ctx context.Context, tx pgx.Tx, caseID, typ, actor, from, to, note string, detail map[string]any, at time.Time) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO app.ops_support_events (id, case_id, event_type, actor_id, from_status, to_status, note, detail, occurred_at)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, $9)`, ids.New(), caseID, typ, actor, from, to, note, b, at)
	return err
}

// slaDue returns now + the ACTIVE target for a metric, or nil when none is approved.
func slaDue(ctx context.Context, tx pgx.Tx, metric string, now time.Time) (*time.Time, error) {
	var secs int
	err := tx.QueryRow(ctx, `SELECT target_seconds FROM app.ops_sla_targets WHERE metric_key = $1 AND status = 'ACTIVE'`, metric).Scan(&secs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d := now.Add(time.Duration(secs) * time.Second)
	return &d, nil
}
