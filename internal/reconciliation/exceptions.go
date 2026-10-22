package reconciliation

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
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Exceptions (docs/stage-17/exceptions.md). One live case per problem (dedupe key); later runs bump occurrences. The
// lifecycle is guarded in the database; history is append-only; a case closes without a resolution only when a later
// run finds its (timing) condition cleared. Nothing here changes money.

// Outbox events (consumed by the Stage 15 notification engine; staff, in-app only).
const (
	EventExceptionOpened   = "reconciliation.exception_opened"
	EventExceptionAssigned = "reconciliation.exception_assigned"
	EventImportFailed      = "reconciliation.import_failed"
	EventResolutionPending = "reconciliation.resolution_requested"
	EventUnknownOutcome    = "reconciliation.unknown_outcome_escalated"
)

// ExceptionEdges mirrors app.status_transitions machine recon_exception (the archtest-style test keeps them identical).
var ExceptionEdges = [][2]string{
	{"", "OPEN"}, {"OPEN", "ASSIGNED"}, {"REOPENED", "ASSIGNED"}, {"ASSIGNED", "INVESTIGATING"}, {"INVESTIGATING", "AWAITING_EVIDENCE"},
	{"AWAITING_EVIDENCE", "INVESTIGATING"}, {"INVESTIGATING", "PENDING_APPROVAL"}, {"PENDING_APPROVAL", "INVESTIGATING"},
	{"PENDING_APPROVAL", "RESOLVED"}, {"RESOLVED", "CLOSED"}, {"RESOLVED", "REOPENED"}, {"OPEN", "CLOSED"}, {"ASSIGNED", "CLOSED"},
	{"INVESTIGATING", "CLOSED"}, {"AWAITING_EVIDENCE", "CLOSED"}, {"REOPENED", "CLOSED"},
}

// ClosableTypes close by themselves when a later run finds the condition cleared (recon.exceptions_close_guard).
var ClosableTypes = set("MISSING_SETTLEMENT", "MISSING_LEDGER_ENTRY", "MISSING_BANK_CREDIT", "STATUS_MISMATCH", "UNKNOWN_PAYMENT_OUTCOME",
	"UNKNOWN_PAYOUT_OUTCOME", "STALE_RECONCILIATION")

var severityRank = map[string]int{SevLow: 1, SevMedium: 2, SevHigh: 3, SevCritical: 4}

// Exception is a reconciliation exception as staff see it.
type Exception struct {
	ID                  string           `json:"id"`
	Type                string           `json:"exception_type"`
	Severity            string           `json:"severity"`
	Status              string           `json:"status"`
	SourceCode          string           `json:"source_code"`
	SourceKind          string           `json:"source_kind"`
	Currency            string           `json:"currency"`
	Occurrences         int              `json:"occurrences"`
	Generation          int              `json:"generation"`
	ItemID              *string          `json:"item_id"`
	SubjectType         *string          `json:"subject_type"`
	SubjectID           *string          `json:"subject_id"`
	SettlementBatchID   *string          `json:"settlement_batch_id"`
	LedgerTransactionID *string          `json:"ledger_transaction_id"`
	ExpectedAmount      *string          `json:"expected_amount_minor"`
	ActualAmount        *string          `json:"actual_amount_minor"`
	ActualCurrencyRaw   *string          `json:"actual_currency_raw"`
	Detail              map[string]any   `json:"detail"`
	DetectedAt          time.Time        `json:"detected_at"`
	LastSeenAt          time.Time        `json:"last_seen_at"`
	AgeHours            int              `json:"age_hours"`
	AssignedTo          *string          `json:"assigned_to"`
	AssignedAt          *time.Time       `json:"assigned_at"`
	DueAt               *time.Time       `json:"due_at"`
	EscalatedAt         *time.Time       `json:"escalated_at"`
	ResolvedAt          *time.Time       `json:"resolved_at"`
	ClosedAt            *time.Time       `json:"closed_at"`
	CloseReason         *string          `json:"close_reason"`
	ReopenCount         int              `json:"reopen_count"`
	Version             int              `json:"version"`
	Label               string           `json:"label"`
	Item                *ItemView        `json:"item,omitempty"`
	Events              []ExceptionEvent `json:"events,omitempty"`
	Resolutions         []Resolution     `json:"resolutions,omitempty"`
}

// ExceptionEvent is one history entry.
type ExceptionEvent struct {
	ID           string    `json:"id"`
	Type         string    `json:"event_type"`
	From         *string   `json:"from_status"`
	To           *string   `json:"to_status"`
	ActorType    string    `json:"actor_type"`
	ActorID      *string   `json:"actor_id"`
	RunID        *string   `json:"run_id"`
	ResolutionID *string   `json:"resolution_id"`
	Note         *string   `json:"note"`
	EvidenceRef  *string   `json:"evidence_ref"`
	At           time.Time `json:"created_at"`
}

// openException creates the case for a problem or, when a live case exists, records that it was seen again. It returns
// whether a new case was opened. runID may be "" (import-time conflicts, staleness).
func (s *Service) openException(ctx context.Context, tx pgx.Tx, runID string, d ExceptionDraft, sourceID, currency string) (bool, error) {
	now := s.now()
	var run any
	if runID != "" {
		run = runID
	}
	var id, sev, status string
	err := tx.QueryRow(ctx, `SELECT id::text, severity, status FROM recon.exceptions WHERE dedupe_key = $1 AND status <> 'CLOSED' FOR UPDATE`,
		d.DedupeKey).Scan(&id, &sev, &status)
	if err == nil {
		newSev := sev
		if severityRank[d.Severity] > severityRank[sev] {
			newSev = d.Severity
		}
		if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET occurrences = occurrences + 1, last_run_id = coalesce($2, last_run_id),
			last_seen_at = $3, severity = $4, version = version + 1 WHERE id = $1`, id, run, now, newSev); err != nil {
			return false, err
		}
		if runID != "" {
			return false, s.addEvent(ctx, tx, id, "SEEN_AGAIN", "", "", "", runID, "", "", "")
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	detail, _ := json.Marshal(d.Detail)
	id = ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO recon.exceptions (id, dedupe_key, generation, exception_type, severity, status, source_id, currency,
		first_run_id, last_run_id, item_id, subject_type, subject_id, settlement_batch_id, ledger_transaction_id, expected_amount_minor,
		actual_amount_minor, actual_currency_raw, detail, detected_at, last_seen_at)
		VALUES ($1, $2, (SELECT coalesce(max(generation), 0) + 1 FROM recon.exceptions WHERE dedupe_key = $2), $3, $4, 'OPEN', $5, $6, $7, $7,
		$8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $17)`,
		id, d.DedupeKey, d.Type, d.Severity, sourceID, currency, run, nullStr(d.ItemID), nullStr(d.SubjectType), nullStr(d.SubjectID),
		nullStr(d.BatchID), nullStr(d.LedgerTxID), d.Expected, d.Actual, nullStr(d.RawCurrency), detail, now)
	if err != nil {
		return false, err
	}
	if err := s.addEvent(ctx, tx, id, "OPENED", "", "OPEN", "", runID, "", "", ""); err != nil {
		return false, err
	}
	if err := audit.Record(ctx, tx, audit.Event{Action: "reconciliation.exception.opened", ActorType: "system", TargetType: "reconciliation_exception",
		TargetID: id, Metadata: map[string]any{"exception_type": d.Type, "severity": d.Severity, "run_id": runID, "environment": "TEST"}}); err != nil {
		return false, err
	}
	payload := map[string]any{"exception_id": id, "exception_type": d.Type, "severity": d.Severity}
	if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "reconciliation_exception", AggregateID: id, EventType: EventExceptionOpened,
		OccurredAt: now, Payload: payload}); err != nil {
		return false, err
	}
	if d.Type == "UNKNOWN_PAYMENT_OUTCOME" || d.Type == "UNKNOWN_PAYOUT_OUTCOME" {
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "reconciliation_exception", AggregateID: id, EventType: EventUnknownOutcome,
			OccurredAt: now, Payload: payload}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Service) addEvent(ctx context.Context, tx pgx.Tx, excID, typ, from, to, actorID, runID, resolutionID, note, evidence string) error {
	actorType := "SYSTEM"
	if actorID != "" {
		actorType = "STAFF"
	}
	_, err := tx.Exec(ctx, `INSERT INTO recon.exception_events (id, exception_id, event_type, from_status, to_status, actor_type, actor_id, run_id,
		resolution_id, note, evidence_ref, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		ids.New(), excID, typ, nullStr(from), nullStr(to), actorType, nullStr(actorID), nullStr(runID), nullStr(resolutionID), nullStr(note),
		nullStr(evidence), s.now())
	return err
}

// closeCleared closes a live case whose condition a later run found cleared (timing types only; never while an
// approval is pending — the database refuses anything else).
func (s *Service) closeCleared(ctx context.Context, tx pgx.Tx, excID, runID string) error {
	var status, typ string
	if err := tx.QueryRow(ctx, `SELECT status, exception_type FROM recon.exceptions WHERE id = $1 FOR UPDATE`, excID).Scan(&status, &typ); err != nil {
		return err
	}
	if !ClosableTypes[typ] || status == "PENDING_APPROVAL" || status == "RESOLVED" || status == "CLOSED" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET status = 'CLOSED', closed_at = $2, close_reason = 'CONDITION_CLEARED',
		last_run_id = coalesce($3, last_run_id), version = version + 1 WHERE id = $1`, excID, s.now(), nullStr(runID)); err != nil {
		return err
	}
	return s.addEvent(ctx, tx, excID, "CLOSED", status, "CLOSED", "", runID, "", "condition cleared by a later run", "")
}

// ---- reads ------------------------------------------------------------------------------------------------------------

const excCols = `e.id::text, e.exception_type, e.severity, e.status, s.code, s.source_kind, e.currency, e.occurrences, e.generation, e.item_id::text,
	e.subject_type, e.subject_id::text, e.settlement_batch_id::text, e.ledger_transaction_id::text, e.expected_amount_minor::text,
	e.actual_amount_minor::text, e.actual_currency_raw, e.detail, e.detected_at, e.last_seen_at, e.assigned_to::text, e.assigned_at, e.due_at,
	e.escalated_at, e.resolved_at, e.closed_at, e.close_reason, e.reopen_count, e.version`

func (s *Service) scanException(r pgx.Row) (Exception, error) {
	var x Exception
	var detail []byte
	err := r.Scan(&x.ID, &x.Type, &x.Severity, &x.Status, &x.SourceCode, &x.SourceKind, &x.Currency, &x.Occurrences, &x.Generation, &x.ItemID,
		&x.SubjectType, &x.SubjectID, &x.SettlementBatchID, &x.LedgerTransactionID, &x.ExpectedAmount, &x.ActualAmount, &x.ActualCurrencyRaw,
		&detail, &x.DetectedAt, &x.LastSeenAt, &x.AssignedTo, &x.AssignedAt, &x.DueAt, &x.EscalatedAt, &x.ResolvedAt, &x.ClosedAt, &x.CloseReason,
		&x.ReopenCount, &x.Version)
	if err != nil {
		return x, err
	}
	x.Detail = map[string]any{}
	_ = json.Unmarshal(detail, &x.Detail)
	x.AgeHours = int(s.now().Sub(x.DetectedAt).Hours())
	x.Label = "TEST_ONLY"
	return x, nil
}

// ExceptionFilter filters the exception queue.
type ExceptionFilter struct {
	Status, Type, Severity, Source, Currency, AssignedTo string
	OpenOnly                                             bool
	Limit                                                int
}

// Exceptions lists exceptions: most severe, then oldest first.
func (s *Service) Exceptions(ctx context.Context, f ExceptionFilter) ([]Exception, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+excCols+` FROM recon.exceptions e JOIN recon.sources s ON s.id = e.source_id
		WHERE ($1 = '' OR e.status = $1) AND ($2 = '' OR e.exception_type = $2) AND ($3 = '' OR e.severity = $3) AND ($4 = '' OR s.code = $4)
		  AND ($5 = '' OR e.currency = $5) AND ($6 = '' OR e.assigned_to::text = $6) AND (NOT $7 OR e.status NOT IN ('RESOLVED','CLOSED'))
		ORDER BY CASE e.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END, e.detected_at, e.id LIMIT $8`,
		f.Status, f.Type, f.Severity, f.Source, f.Currency, f.AssignedTo, f.OpenOnly, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Exception, error) { return s.scanException(r) })
}

// ExceptionByID returns one exception with its line, history and resolutions.
func (s *Service) ExceptionByID(ctx context.Context, id string) (Exception, error) {
	if !ids.Valid(id) {
		return Exception{}, ErrNotFound
	}
	x, err := s.scanException(s.Pool.QueryRow(ctx, `SELECT `+excCols+` FROM recon.exceptions e JOIN recon.sources s ON s.id = e.source_id
		WHERE e.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Exception{}, ErrNotFound
	}
	if err != nil {
		return x, err
	}
	if x.ItemID != nil {
		it, err := s.ItemByID(ctx, *x.ItemID)
		if err == nil {
			x.Item = &it
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, event_type, from_status, to_status, actor_type, actor_id::text, run_id::text,
		resolution_id::text, note, evidence_ref, created_at FROM recon.exception_events WHERE exception_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return x, err
	}
	x.Events, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExceptionEvent, error) {
		var e ExceptionEvent
		err := r.Scan(&e.ID, &e.Type, &e.From, &e.To, &e.ActorType, &e.ActorID, &e.RunID, &e.ResolutionID, &e.Note, &e.EvidenceRef, &e.At)
		return e, err
	})
	if err != nil {
		return x, err
	}
	x.Resolutions, err = s.resolutionsFor(ctx, id)
	return x, err
}

// ---- staff actions ------------------------------------------------------------------------------------------------------

// ActionInput is the body of an exception action.
type ActionInput struct {
	Version     int        `json:"version"`
	Note        string     `json:"note"`
	AssigneeID  string     `json:"assignee_id"`
	DueAt       *time.Time `json:"due_at"`
	EvidenceRef string     `json:"evidence_ref"`
}

var evidenceRefRE = regexp.MustCompile(`^(import|item|run|evidence|batch|payment|payout|ledger_transaction|document):[A-Za-z0-9_.:/-]{1,180}$`)

func cleanNote(note string, min, max int) (string, error) {
	n := strings.TrimSpace(note)
	if c := utf8.RuneCountInString(n); c < min || c > max {
		return "", httpx.Validation(errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	return n, nil
}

// Act performs a staff lifecycle action: assign, start, await_evidence, resume, comment, close, reopen.
func (s *Service) Act(ctx context.Context, staffID, id, action string, in ActionInput) (Exception, error) {
	if !ids.Valid(id) {
		return Exception{}, ErrNotFound
	}
	minNote := 5
	if action == "assign" {
		minNote = 0
	}
	note, err := cleanNote(in.Note, minNote, 2000)
	if err != nil {
		return Exception{}, err
	}
	if in.EvidenceRef != "" && !evidenceRefRE.MatchString(in.EvidenceRef) {
		return Exception{}, httpx.Validation(errs.Detail{Field: "evidence_ref", Code: "INVALID_FORMAT"})
	}
	if action == "assign" {
		if !ids.Valid(in.AssigneeID) {
			return Exception{}, httpx.Validation(errs.Detail{Field: "assignee_id", Code: "INVALID_FORMAT"})
		}
		if s.Directory != nil {
			list, err := s.Directory.StaffWithPermission(ctx, PermInvestigate)
			if err != nil {
				return Exception{}, err
			}
			if !contains(list, in.AssigneeID) {
				return Exception{}, httpx.Validation(errs.Detail{Field: "assignee_id", Code: "NOT_A_RECONCILIATION_INVESTIGATOR"})
			}
		}
		if in.DueAt != nil && !in.DueAt.After(s.now()) {
			return Exception{}, httpx.Validation(errs.Detail{Field: "due_at", Code: "IN_THE_PAST"})
		}
	}
	var assignedEvent bool
	var excType, sev string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var version int
		var assignee *string
		if err := tx.QueryRow(ctx, `SELECT status, version, assigned_to::text, exception_type, severity FROM recon.exceptions WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &version, &assignee, &excType, &sev); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if in.Version != 0 && in.Version != version {
			return ErrStale
		}
		move := func(to, extra string, args ...any) error {
			set := `status = $2, version = version + 1`
			if extra != "" {
				set += ", " + extra
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET `+set+` WHERE id = $1`, append([]any{id, to}, args...)...); err != nil {
				return err
			}
			return s.addEvent(ctx, tx, id, "STATUS_CHANGED", status, to, staffID, "", "", note, in.EvidenceRef)
		}
		switch action {
		case "assign":
			switch status {
			case "OPEN", "REOPENED":
				if err := move("ASSIGNED", `assigned_to = $3, assigned_by = $4, assigned_at = $5, due_at = $6`, in.AssigneeID, staffID, s.now(), in.DueAt); err != nil {
					return err
				}
			case "ASSIGNED", "INVESTIGATING", "AWAITING_EVIDENCE":
				if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET assigned_to = $2, assigned_by = $3, assigned_at = $4, due_at = coalesce($5, due_at),
					version = version + 1 WHERE id = $1`, id, in.AssigneeID, staffID, s.now(), in.DueAt); err != nil {
					return err
				}
			default:
				return ErrState
			}
			assignedEvent = true
			if err := s.addEvent(ctx, tx, id, "ASSIGNED", "", "", staffID, "", "", "assigned to "+in.AssigneeID, ""); err != nil {
				return err
			}
			if in.DueAt != nil {
				if err := s.addEvent(ctx, tx, id, "DUE_DATE_SET", "", "", staffID, "", "", in.DueAt.UTC().Format(time.RFC3339), ""); err != nil {
					return err
				}
			}
		case "start":
			if status != "ASSIGNED" {
				return ErrState
			}
			if assignee == nil || *assignee != staffID {
				return errs.New(errs.Forbidden, "NOT_ASSIGNEE", "Only the assigned investigator can start the investigation.")
			}
			if err := move("INVESTIGATING", ""); err != nil {
				return err
			}
		case "await_evidence":
			if status != "INVESTIGATING" {
				return ErrState
			}
			if err := move("AWAITING_EVIDENCE", ""); err != nil {
				return err
			}
		case "resume":
			if status != "AWAITING_EVIDENCE" {
				return ErrState
			}
			if err := move("INVESTIGATING", ""); err != nil {
				return err
			}
		case "comment":
			if status == "CLOSED" {
				return ErrState
			}
			typ := "COMMENT"
			if in.EvidenceRef != "" {
				typ = "EVIDENCE_ADDED"
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.exceptions SET version = version + 1 WHERE id = $1`, id); err != nil {
				return err
			}
			if err := s.addEvent(ctx, tx, id, typ, "", "", staffID, "", "", note, in.EvidenceRef); err != nil {
				return err
			}
		case "close":
			if status != "RESOLVED" {
				return ErrState
			}
			if err := move("CLOSED", `closed_at = $3, close_reason = 'RESOLVED'`, s.now()); err != nil {
				return err
			}
		case "reopen":
			if status != "RESOLVED" {
				return ErrState
			}
			if err := move("REOPENED", `reopen_count = reopen_count + 1`); err != nil {
				return err
			}
		default:
			return ErrNotFound
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.exception." + action, ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_exception", TargetID: id, Reason: note, Metadata: map[string]any{"from_status": status,
				"assignee_id": in.AssigneeID, "evidence_ref": in.EvidenceRef}})
	})
	if err != nil {
		return Exception{}, err
	}
	if assignedEvent {
		if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "reconciliation_exception", AggregateID: id, EventType: EventExceptionAssigned,
				OccurredAt: s.now(), Payload: map[string]any{"exception_id": id, "exception_type": excType, "severity": sev, "assignee_id": in.AssigneeID,
					"actor_id": staffID}})
			return err
		}); err != nil {
			s.Logger.WarnContext(ctx, "reconciliation: assignment notification not queued", "exception_id", id, "err", err)
		}
	}
	return s.ExceptionByID(ctx, id)
}
