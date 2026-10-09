package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// Actor is who performs an operation: a staff member, or a system job (consumers).
type Actor struct {
	Type string // STAFF | SYSTEM
	ID   string // staff user id (STAFF)
	Job  string // consumer / job name (SYSTEM), e.g. compliance.open_case
}

// Staff returns a staff actor.
func Staff(userID string) Actor { return Actor{Type: "STAFF", ID: userID} }

func (a Actor) auditType() string {
	if a.Type == "STAFF" {
		return "staff"
	}
	return "system"
}

// Link is one subject or related object of a case.
type Link struct {
	ID          string    `json:"id"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	Role        string    `json:"role"`
	LinkedAt    time.Time `json:"linked_at"`
}

// LinkInput is a link to create.
type LinkInput struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	Role        string `json:"role"`
}

// Resolution is the case's recorded resolution.
type Resolution struct {
	Decision         string     `json:"decision"`
	ReasonCode       string     `json:"reason_code"`
	Status           string     `json:"status"` // PROPOSED | APPROVED
	RequiresApproval bool       `json:"requires_approval"`
	DecidedBy        string     `json:"decided_by"`
	DecidedAt        time.Time  `json:"decided_at"`
	ApprovedBy       *string    `json:"approved_by"`
	ApprovedAt       *time.Time `json:"approved_at"`
}

// Closure records how the case was closed.
type Closure struct {
	ClosedBy   string    `json:"closed_by"`
	ClosedAt   time.Time `json:"closed_at"`
	ReasonCode string    `json:"reason_code"`
}

// OpenedBy describes the opener.
type OpenedBy struct {
	Type string  `json:"type"`
	ID   *string `json:"id"`
	Job  *string `json:"job"`
}

// Case is the staff view of a compliance case.
type Case struct {
	ID                string      `json:"id"`
	CaseNumber        string      `json:"case_number"`
	CaseType          string      `json:"case_type"`
	Severity          string      `json:"severity"`
	Status            string      `json:"status"`
	Confidentiality   string      `json:"confidentiality"`
	Source            string      `json:"source"`
	OpeningReasonCode string      `json:"opening_reason_code"`
	OpenedBy          OpenedBy    `json:"opened_by"`
	OpenedAt          time.Time   `json:"opened_at"`
	AssignedTo        *string     `json:"assigned_to"`
	AssignedAt        *time.Time  `json:"assigned_at"`
	Resolution        *Resolution `json:"resolution"`
	Closure           *Closure    `json:"closure"`
	Links             []Link      `json:"links"`
	Version           int         `json:"version"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// Event is one timeline entry (codes and ids only).
type Event struct {
	ID          string         `json:"id"`
	CaseVersion *int           `json:"case_version"`
	EventType   string         `json:"event_type"`
	FromStatus  *string        `json:"from_status"`
	ToStatus    *string        `json:"to_status"`
	ActorType   string         `json:"actor_type"`
	ActorID     *string        `json:"actor_id"`
	ActorJob    *string        `json:"actor_job"`
	ReasonCode  *string        `json:"reason_code"`
	Payload     map[string]any `json:"payload"`
	OccurredAt  time.Time      `json:"occurred_at"`
}

// Note is a decrypted staff note. Notes never leave the staff compliance endpoints.
type Note struct {
	ID         string    `json:"id"`
	AuthorID   string    `json:"author_id"`
	Visibility string    `json:"visibility"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

// CaseDetail is a case with its timeline and notes.
type CaseDetail struct {
	Case
	Events []Event `json:"events"`
	Notes  []Note  `json:"notes"`
}

// MaxNoteRunes bounds a note's length.
const MaxNoteRunes = 10000

const caseCols = `id, case_number, case_type, severity, status, confidentiality, source, opening_reason_code, opened_by_type, opened_by,
	opened_by_job, opened_at, assigned_to, assigned_at, decision, decision_reason_code, resolution_status, decided_by, decided_at,
	approved_by, approved_at, closed_by, closed_at, closure_reason_code, version, created_at, updated_at`

type caseRow struct {
	Case
	decision, decisionReason, resolutionStatus, decidedBy *string
	decidedAt                                             *time.Time
	approvedBy                                            *string
	approvedAt                                            *time.Time
	closedBy, closureReason                               *string
	closedAt                                              *time.Time
}

func scanCase(row pgx.Row) (caseRow, error) {
	var c caseRow
	err := row.Scan(&c.ID, &c.CaseNumber, &c.CaseType, &c.Severity, &c.Status, &c.Confidentiality, &c.Source, &c.OpeningReasonCode,
		&c.OpenedBy.Type, &c.OpenedBy.ID, &c.OpenedBy.Job, &c.OpenedAt, &c.AssignedTo, &c.AssignedAt, &c.decision, &c.decisionReason,
		&c.resolutionStatus, &c.decidedBy, &c.decidedAt, &c.approvedBy, &c.approvedAt, &c.closedBy, &c.closedAt, &c.closureReason,
		&c.Version, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if c.decision != nil {
		c.Resolution = &Resolution{Decision: *c.decision, ReasonCode: deref(c.decisionReason), Status: deref(c.resolutionStatus),
			RequiresApproval: CheckerRequired[*c.decision], DecidedBy: deref(c.decidedBy), ApprovedBy: c.approvedBy, ApprovedAt: c.approvedAt}
		if c.decidedAt != nil {
			c.Resolution.DecidedAt = *c.decidedAt
		}
	}
	if c.closedAt != nil {
		c.Closure = &Closure{ClosedBy: deref(c.closedBy), ClosedAt: *c.closedAt, ReasonCode: deref(c.closureReason)}
	}
	c.Links = []Link{}
	return c, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Service) lockCase(ctx context.Context, tx pgx.Tx, id string) (caseRow, error) {
	if !ids.Valid(id) {
		return caseRow{}, errNotFound()
	}
	c, err := scanCase(tx.QueryRow(ctx, `SELECT `+caseCols+` FROM compliance.compliance_cases WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errNotFound() // also when RLS hides a RESTRICTED_STR case
	}
	return c, err
}

func (s *Service) currentLinks(ctx context.Context, q pgx.Tx, caseID string) ([]Link, error) {
	rows, err := q.Query(ctx, `SELECT l.id, l.subject_type, l.subject_id, l.role, l.linked_at FROM compliance.compliance_case_links l
		WHERE l.case_id = $1 AND l.link_action = 'LINKED'
		  AND NOT EXISTS (SELECT 1 FROM compliance.compliance_case_links u WHERE u.unlinks_id = l.id)
		ORDER BY l.linked_at, l.id`, caseID)
	if err != nil {
		return nil, err
	}
	links, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Link, error) {
		var l Link
		err := r.Scan(&l.ID, &l.SubjectType, &l.SubjectID, &l.Role, &l.LinkedAt)
		return l, err
	})
	if links == nil {
		links = []Link{}
	}
	return links, err
}

// ---- validation ------------------------------------------------------------------------------------------

func validateLinks(links []LinkInput) []errs.Detail {
	var d []errs.Detail
	if len(links) == 0 || len(links) > 20 {
		return []errs.Detail{{Field: "subjects", Code: "INVALID_LENGTH"}}
	}
	primary := 0
	for i, l := range links {
		f := fmt.Sprintf("subjects[%d]", i)
		switch {
		case !ids.Valid(l.SubjectID):
			d = append(d, errs.Detail{Field: f + ".subject_id", Code: "INVALID_ID"})
		case PartyTypes[l.SubjectType] && (l.Role == "PRIMARY_SUBJECT" || l.Role == "RELATED_SUBJECT"):
		case ObjectTypes[l.SubjectType] && l.Role == "RELATED_OBJECT":
		default:
			d = append(d, errs.Detail{Field: f, Code: "INVALID_SUBJECT"})
		}
		if l.Role == "PRIMARY_SUBJECT" {
			primary++
		}
	}
	if primary > 1 {
		d = append(d, errs.Detail{Field: "subjects", Code: "MULTIPLE_PRIMARY_SUBJECTS"})
	}
	return d
}

func cleanNote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxNoteRunes
}

// ---- writes shared by all operations ---------------------------------------------------------------------

func (s *Service) audit(ctx context.Context, tx pgx.Tx, actor Actor, action, caseID string, md map[string]any) (string, error) {
	return audit.RecordGateway(ctx, tx, audit.Event{Action: action, ActorType: actor.auditType(), ActorID: actor.ID,
		TargetType: "compliance_case", TargetID: caseID, Metadata: md, OccurredAt: s.now()})
}

func (s *Service) event(ctx context.Context, tx pgx.Tx, caseID string, version *int, typ string, from, to *string, actor Actor,
	reason string, payload map[string]any, auditID string) error {
	if payload == nil {
		payload = map[string]any{}
	}
	pj, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var actorID, job any
	if actor.Type == "STAFF" {
		actorID = actor.ID
	} else {
		job = actor.Job
	}
	_, err = tx.Exec(ctx, `INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, from_status, to_status,
		actor_type, actor_id, actor_job, reason_code, payload, audit_event_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		ids.New(), caseID, version, typ, from, to, actor.Type, actorID, job, nullIfEmpty(reason), pj, nullIfEmpty(auditID), s.now())
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) addLink(ctx context.Context, tx pgx.Tx, caseID string, l LinkInput, actor Actor) error {
	var by any
	if actor.Type == "STAFF" {
		by = actor.ID
	}
	_, err := tx.Exec(ctx, `INSERT INTO compliance.compliance_case_links (id, case_id, subject_type, subject_id, role, linked_by_type,
		linked_by, linked_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, ids.New(), caseID, l.SubjectType, l.SubjectID, l.Role, actor.Type, by, s.now())
	return err
}

// insertNote encrypts and stores a note. The plaintext never reaches logs, audit metadata or events.
func (s *Service) insertNote(ctx context.Context, tx pgx.Tx, caseID, authorID, body string) (string, error) {
	id := ids.New()
	sealed, err := s.aead.Seal([]byte(body), []byte(id))
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO compliance.compliance_case_notes (id, case_id, author_id, visibility, body_ciphertext, key_id, created_at)
		VALUES ($1, $2, $3, 'STAFF_ONLY', $4, $5, $6)`, id, caseID, authorID, sealed, s.aead.KeyID(), s.now())
	return id, err
}

func linkPayload(links []Link) []map[string]string {
	out := make([]map[string]string, 0, len(links))
	for _, l := range links {
		out = append(out, map[string]string{"subject_type": l.SubjectType, "subject_id": l.SubjectID, "role": l.Role})
	}
	return out
}

func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ, caseID string, payload map[string]any) error {
	_, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "compliance_case", AggregateID: caseID, EventType: typ,
		Payload: payload, CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

// ---- conflict-of-interest check ----------------------------------------------------------------------------

// checkNotSubject refuses when the staff member is a linked USER party of the case (directly or through their
// linked personal account) or a member of a linked ORGANISATION party. The database repeats the direct and
// staff-alias checks (trg_compliance_cases_actor_not_subject).
func (s *Service) checkNotSubject(ctx context.Context, tx pgx.Tx, caseID, staffID string) error {
	links, err := s.currentLinks(ctx, tx, caseID)
	if err != nil {
		return err
	}
	var users, orgs []string
	for _, l := range links {
		switch l.SubjectType {
		case "USER":
			users = append(users, l.SubjectID)
		case "ORGANISATION":
			orgs = append(orgs, l.SubjectID)
		}
	}
	for _, u := range users {
		if u == staffID {
			return errSelfDecision()
		}
	}
	if len(users) == 0 && len(orgs) == 0 {
		return nil
	}
	if s.relations == nil {
		if len(orgs) > 0 {
			return errSubjectCheckUnavailable()
		}
		return nil // the staff-alias check for USER subjects is enforced by the database trigger
	}
	personal, err := s.relations.PersonalAccount(ctx, staffID)
	if err != nil {
		return errs.Wrap(err, errs.Unavailable, "SUBJECT_CHECK_UNAVAILABLE", "The conflict-of-interest check is unavailable. Please retry shortly.")
	}
	if personal == "" {
		return nil
	}
	for _, u := range users {
		if u == personal {
			return errSelfDecision()
		}
	}
	for _, o := range orgs {
		member, err := s.relations.IsOrganisationMember(ctx, o, personal)
		if err != nil {
			return errs.Wrap(err, errs.Unavailable, "SUBJECT_CHECK_UNAVAILABLE", "The conflict-of-interest check is unavailable. Please retry shortly.")
		}
		if member {
			return errSelfDecision()
		}
	}
	return nil
}

// ---- open ------------------------------------------------------------------------------------------------

// OpenInput opens a case.
type OpenInput struct {
	CaseType          string
	Severity          string
	Source            string
	OpeningReasonCode string
	Confidentiality   string // NORMAL (default) | RESTRICTED_STR
	Links             []LinkInput
	Note              string
}

// openTx creates a case, its links, its first event, audit event and compliance.case_opened in tx.
func (s *Service) openTx(ctx context.Context, tx pgx.Tx, id string, actor Actor, in OpenInput) error {
	now := s.now()
	var seq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('compliance.case_number_seq')`).Scan(&seq); err != nil {
		return err
	}
	number := fmt.Sprintf("CMP-%04d-%06d", now.Year(), seq)
	if in.Confidentiality == "" {
		in.Confidentiality = ConfNormal
	}
	var openedBy, job any
	if actor.Type == "STAFF" {
		openedBy = actor.ID
	} else {
		job = actor.Job
	}
	// no RETURNING: a RESTRICTED_STR row is not visible to its creator without the flag
	if _, err := tx.Exec(ctx, `INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, confidentiality, source,
		opening_reason_code, opened_by_type, opened_by, opened_by_job, opened_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'OPEN', $5, $6, $7, $8, $9, $10, $11, $11, $11)`,
		id, number, in.CaseType, in.Severity, in.Confidentiality, in.Source, in.OpeningReasonCode, actor.Type, openedBy, job, now); err != nil {
		return err
	}
	links := make([]Link, 0, len(in.Links))
	for _, l := range in.Links {
		if err := s.addLink(ctx, tx, id, l, actor); err != nil {
			return err
		}
		links = append(links, Link{SubjectType: l.SubjectType, SubjectID: l.SubjectID, Role: l.Role})
	}
	md := map[string]any{"case_number": number, "case_type": in.CaseType, "severity": in.Severity, "source": in.Source,
		"reason_code": in.OpeningReasonCode, "confidentiality": in.Confidentiality, "links": len(links)}
	auditID, err := s.audit(ctx, tx, actor, "compliance.case.opened", id, md)
	if err != nil {
		return err
	}
	v, to := 1, StatusOpen
	if err := s.event(ctx, tx, id, &v, "OPENED", nil, &to, actor, in.OpeningReasonCode,
		map[string]any{"case_type": in.CaseType, "severity": in.Severity, "source": in.Source}, auditID); err != nil {
		return err
	}
	if in.Note != "" && actor.Type == "STAFF" {
		noteID, err := s.insertNote(ctx, tx, id, actor.ID, in.Note)
		if err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, nil, "NOTE_ADDED", nil, nil, actor, "", map[string]any{"note_id": noteID}, ""); err != nil {
			return err
		}
	}
	return s.emit(ctx, tx, EvCaseOpened, id, map[string]any{"case_id": id, "case_type": in.CaseType, "severity": in.Severity,
		"source": in.Source, "links": linkPayload(links)})
}

// Open opens a case manually (staff, POST /admin/compliance/cases).
func (s *Service) Open(ctx context.Context, actor Actor, in OpenInput, strAccess bool) (Case, error) {
	var details []errs.Detail
	if !CaseTypes[in.CaseType] {
		details = append(details, errs.Detail{Field: "case_type", Code: "INVALID_VALUE"})
	}
	if _, ok := Severities[in.Severity]; !ok {
		details = append(details, errs.Detail{Field: "severity", Code: "INVALID_VALUE"})
	}
	if !ValidReasonCode(in.OpeningReasonCode) {
		details = append(details, errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	if in.Confidentiality != "" && in.Confidentiality != ConfNormal && in.Confidentiality != ConfRestrictedSTR {
		details = append(details, errs.Detail{Field: "confidentiality", Code: "INVALID_VALUE"})
	}
	note, ok := cleanNote(in.Note)
	if !ok {
		details = append(details, errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	details = append(details, validateLinks(in.Links)...)
	if len(details) > 0 {
		return Case{}, httpx.Validation(details...)
	}
	if in.Confidentiality == ConfRestrictedSTR && !strAccess {
		return Case{}, errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission to open a restricted case.")
	}
	in.Note, in.Source = note, "STAFF"
	id := ids.New()
	err := s.tx(ctx, strAccess, func(ctx context.Context, tx pgx.Tx) error { return s.openTx(ctx, tx, id, actor, in) })
	if err != nil {
		return Case{}, mapDBError(err)
	}
	return s.load(ctx, id, strAccess)
}

// ---- transitions ------------------------------------------------------------------------------------------

// Op carries the inputs of one case operation; each operation uses the fields it needs.
type Op struct {
	Actor      Actor
	CaseID     string
	IfVersion  int  // optional optimistic check (If-Match); 0 = none
	STRAccess  bool // caller holds str.prepare with a fresh step-up
	ReasonCode string
	Note       string
	Decision   string
	Severity   string
	AssigneeID string
}

type change struct {
	to      string         // new status ("" = unchanged)
	set     map[string]any // column -> value (column names are code constants)
	evType  string
	reason  string
	payload map[string]any
	action  string
	md      map[string]any
	after   func(ctx context.Context, tx pgx.Tx, c caseRow, newVersion int) error
}

// apply locks the case, runs decide (which validates the current state and returns the change), writes the
// update with version + 1, the versioned timeline event, the audit event, an optional note and side effects.
func (s *Service) apply(ctx context.Context, op Op, decide func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error)) (Case, error) {
	note, ok := cleanNote(op.Note)
	if !ok {
		return Case{}, httpx.Validation(errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	err := s.tx(ctx, op.STRAccess, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockCase(ctx, tx, op.CaseID)
		if err != nil {
			return err
		}
		if op.IfVersion > 0 && op.IfVersion != c.Version {
			return errStateChanged()
		}
		ch, err := decide(ctx, tx, c)
		if err != nil {
			return err
		}
		if ch.to != "" && ch.to != c.Status && !CanTransition(c.Status, ch.to) {
			return errStateChanged()
		}
		cols := []string{"version = version + 1"}
		args := []any{c.ID, c.Version}
		if ch.to != "" {
			args = append(args, ch.to)
			cols = append(cols, fmt.Sprintf("status = $%d", len(args)))
		}
		keys := make([]string, 0, len(ch.set))
		for k := range ch.set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, ch.set[k])
			cols = append(cols, fmt.Sprintf("%s = $%d", k, len(args)))
		}
		tag, err := tx.Exec(ctx, `UPDATE compliance.compliance_cases SET `+strings.Join(cols, ", ")+` WHERE id = $1 AND version = $2`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errStateChanged()
		}
		newV := c.Version + 1
		md := map[string]any{"case_number": c.CaseNumber, "from_status": c.Status, "to_status": c.Status}
		if ch.to != "" {
			md["to_status"] = ch.to
		}
		if ch.reason != "" {
			md["reason_code"] = ch.reason
		}
		for k, v := range ch.md {
			md[k] = v
		}
		var noteID string
		if note != "" {
			if noteID, err = s.insertNote(ctx, tx, c.ID, op.Actor.ID, note); err != nil {
				return err
			}
			md["note_id"] = noteID
		}
		auditID, err := s.audit(ctx, tx, op.Actor, ch.action, c.ID, md)
		if err != nil {
			return err
		}
		payload := ch.payload
		if payload == nil {
			payload = map[string]any{}
		}
		if noteID != "" {
			payload["note_id"] = noteID
		}
		from := c.Status
		to := c.Status
		if ch.to != "" {
			to = ch.to
		}
		if err := s.event(ctx, tx, c.ID, &newV, ch.evType, &from, &to, op.Actor, ch.reason, payload, auditID); err != nil {
			return err
		}
		if ch.after != nil {
			return ch.after(ctx, tx, c, newV)
		}
		return nil
	})
	if err != nil {
		return Case{}, mapDBError(err)
	}
	return s.load(ctx, op.CaseID, op.STRAccess)
}

func requireAssignee(c caseRow, actor Actor) error {
	if c.AssignedTo == nil || *c.AssignedTo != actor.ID {
		return errNotAssigned()
	}
	return nil
}

// Assign assigns the case (default: to the caller). OPEN -> ASSIGNED; later non-final states are reassigned
// without a status change.
func (s *Service) Assign(ctx context.Context, op Op) (Case, error) {
	assignee := op.AssigneeID
	if assignee == "" {
		assignee = op.Actor.ID
	}
	if !ids.Valid(assignee) {
		return Case{}, httpx.Validation(errs.Detail{Field: "assignee_id", Code: "INVALID_ID"})
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		switch c.Status {
		case StatusOpen, StatusAssigned, StatusInReview, StatusAwaitingInformation, StatusEscalated:
		default:
			return change{}, errStateChanged()
		}
		if c.AssignedTo != nil && *c.AssignedTo == assignee {
			return change{}, errs.New(errs.Conflict, "ALREADY_ASSIGNED", "The case is already assigned to this person.")
		}
		if assignee != op.Actor.ID {
			if s.relations == nil {
				return change{}, errSubjectCheckUnavailable()
			}
			ok, err := s.relations.StaffHasPermission(ctx, assignee, PermManage)
			if err != nil {
				return change{}, errs.Wrap(err, errs.Unavailable, "SUBJECT_CHECK_UNAVAILABLE", "The assignee check is unavailable. Please retry shortly.")
			}
			if !ok {
				return change{}, errs.New(errs.Unprocessable, "ASSIGNEE_NOT_ELIGIBLE", "The assignee cannot manage compliance cases.")
			}
		}
		if err := s.checkNotSubject(ctx, tx, c.ID, assignee); err != nil {
			return change{}, err
		}
		ch := change{set: map[string]any{"assigned_to": assignee, "assigned_at": s.now()}, evType: "ASSIGNED",
			payload: map[string]any{"assignee_id": assignee}, action: "compliance.case.assigned", md: map[string]any{"assignee_id": assignee}}
		if c.Status == StatusOpen {
			ch.to = StatusAssigned
		}
		return ch, nil
	})
}

// Start begins (or resumes) the review: ASSIGNED | AWAITING_INFORMATION | ESCALATED -> IN_REVIEW. Assignee only.
func (s *Service) Start(ctx context.Context, op Op) (Case, error) {
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusAssigned && c.Status != StatusAwaitingInformation && c.Status != StatusEscalated {
			return change{}, errStateChanged()
		}
		if err := requireAssignee(c, op.Actor); err != nil {
			return change{}, err
		}
		return change{to: StatusInReview, evType: "STATUS_CHANGED", action: "compliance.case.review_started"}, nil
	})
}

func needReason(code string) error {
	if !ValidReasonCode(code) {
		return httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	return nil
}

// RequestInformation pauses the review for information: IN_REVIEW -> AWAITING_INFORMATION. Assignee only.
func (s *Service) RequestInformation(ctx context.Context, op Op) (Case, error) {
	if err := needReason(op.ReasonCode); err != nil {
		return Case{}, err
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusInReview {
			return change{}, errStateChanged()
		}
		if err := requireAssignee(c, op.Actor); err != nil {
			return change{}, err
		}
		return change{to: StatusAwaitingInformation, evType: "INFO_REQUESTED", reason: op.ReasonCode,
			action: "compliance.case.information_requested"}, nil
	})
}

// Escalate escalates the review: IN_REVIEW -> ESCALATED, optionally raising the severity. Assignee only.
func (s *Service) Escalate(ctx context.Context, op Op) (Case, error) {
	if err := needReason(op.ReasonCode); err != nil {
		return Case{}, err
	}
	if _, ok := Severities[op.Severity]; op.Severity != "" && !ok {
		return Case{}, httpx.Validation(errs.Detail{Field: "severity", Code: "INVALID_VALUE"})
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusInReview {
			return change{}, errStateChanged()
		}
		if err := requireAssignee(c, op.Actor); err != nil {
			return change{}, err
		}
		ch := change{to: StatusEscalated, evType: "ESCALATED", reason: op.ReasonCode, action: "compliance.case.escalated"}
		if op.Severity != "" && op.Severity != c.Severity {
			if Severities[op.Severity] < Severities[c.Severity] {
				return change{}, httpx.Validation(errs.Detail{Field: "severity", Code: "SEVERITY_CANNOT_DECREASE"})
			}
			ch.set = map[string]any{"severity": op.Severity}
			ch.payload = map[string]any{"severity_from": c.Severity, "severity_to": op.Severity}
			ch.md = map[string]any{"severity": op.Severity}
		}
		return ch, nil
	})
}

// Resolve records the resolution: IN_REVIEW | ESCALATED -> RESOLVED. Assignee only, never a subject. A
// decision in CheckerRequired is PROPOSED and needs ApproveResolution by someone else; any other decision is
// APPROVED at once and compliance.case_resolved is emitted.
func (s *Service) Resolve(ctx context.Context, op Op) (Case, error) {
	var details []errs.Detail
	if !Decisions[op.Decision] {
		details = append(details, errs.Detail{Field: "decision", Code: "INVALID_VALUE"})
	}
	if !ValidReasonCode(op.ReasonCode) {
		details = append(details, errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	if strings.TrimSpace(op.Note) == "" {
		details = append(details, errs.Detail{Field: "note", Code: "REQUIRED"})
	}
	if len(details) > 0 {
		return Case{}, httpx.Validation(details...)
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusInReview && c.Status != StatusEscalated {
			return change{}, errStateChanged()
		}
		if err := requireAssignee(c, op.Actor); err != nil {
			return change{}, err
		}
		if err := s.checkNotSubject(ctx, tx, c.ID, op.Actor.ID); err != nil {
			return change{}, err
		}
		status := ResolutionApproved
		evType, action := "RESOLVED", "compliance.case.resolved"
		if CheckerRequired[op.Decision] {
			status, evType, action = ResolutionProposed, "RESOLUTION_PROPOSED", "compliance.case.resolution_proposed"
		}
		ch := change{to: StatusResolved, evType: evType, reason: op.ReasonCode, action: action,
			set: map[string]any{"decision": op.Decision, "decision_reason_code": op.ReasonCode, "resolution_status": status,
				"decided_by": op.Actor.ID, "decided_at": s.now()},
			payload: map[string]any{"decision": op.Decision, "resolution_status": status},
			md:      map[string]any{"decision": op.Decision, "resolution_status": status}}
		if status == ResolutionApproved {
			ch.after = s.emitResolved(op.Actor, op.Decision)
		}
		return ch, nil
	})
}

// emitResolved runs when a resolution becomes APPROVED: it maintains the case's restrictions (ADR-037 §3) and
// emits compliance.case_resolved, in the approving transaction.
func (s *Service) emitResolved(actor Actor, decision string) func(ctx context.Context, tx pgx.Tx, c caseRow, v int) error {
	return func(ctx context.Context, tx pgx.Tx, c caseRow, v int) error {
		if err := s.syncRestrictions(ctx, tx, actor, c.ID, decision, v); err != nil {
			return err
		}
		links, err := s.currentLinks(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		return s.emit(ctx, tx, EvCaseResolved, c.ID, map[string]any{"case_id": c.ID, "decision": decision, "links": linkPayload(links)})
	}
}

// ApproveResolution is the checker's approval of a PROPOSED resolution. The approver is never the decider
// nor a subject (also enforced by the database).
func (s *Service) ApproveResolution(ctx context.Context, op Op) (Case, error) {
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusResolved || deref(c.resolutionStatus) != ResolutionProposed {
			return change{}, errStateChanged()
		}
		if deref(c.decidedBy) == op.Actor.ID {
			return change{}, errSelfApproval()
		}
		if err := s.checkNotSubject(ctx, tx, c.ID, op.Actor.ID); err != nil {
			return change{}, err
		}
		decision := deref(c.decision)
		return change{evType: "RESOLUTION_APPROVED", reason: deref(c.decisionReason), action: "compliance.case.resolution_approved",
			set:     map[string]any{"resolution_status": ResolutionApproved, "approved_by": op.Actor.ID, "approved_at": s.now()},
			payload: map[string]any{"decision": decision, "decided_by": deref(c.decidedBy)},
			md:      map[string]any{"decision": decision}, after: s.emitResolved(op.Actor, decision)}, nil
	})
}

// Close closes a RESOLVED case whose resolution is APPROVED.
func (s *Service) Close(ctx context.Context, op Op) (Case, error) {
	if err := needReason(op.ReasonCode); err != nil {
		return Case{}, err
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusResolved {
			return change{}, errStateChanged()
		}
		if deref(c.resolutionStatus) != ResolutionApproved {
			return change{}, errs.New(errs.Conflict, "RESOLUTION_NOT_APPROVED", "The resolution needs a second approval before the case can close.")
		}
		return change{to: StatusClosed, evType: "CLOSED", reason: op.ReasonCode, action: "compliance.case.closed",
			set: map[string]any{"closed_by": op.Actor.ID, "closed_at": s.now(), "closure_reason_code": op.ReasonCode}}, nil
	})
}

// Reopen returns a RESOLVED or CLOSED case to IN_REVIEW, clearing the resolution and closure (the timeline
// and audit trail keep them). A note explaining why is required.
func (s *Service) Reopen(ctx context.Context, op Op) (Case, error) {
	if err := needReason(op.ReasonCode); err != nil {
		return Case{}, err
	}
	if strings.TrimSpace(op.Note) == "" {
		return Case{}, httpx.Validation(errs.Detail{Field: "note", Code: "REQUIRED"})
	}
	return s.apply(ctx, op, func(ctx context.Context, tx pgx.Tx, c caseRow) (change, error) {
		if c.Status != StatusResolved && c.Status != StatusClosed {
			return change{}, errStateChanged()
		}
		prev := map[string]any{"previous_decision": deref(c.decision), "previous_resolution_status": deref(c.resolutionStatus)}
		return change{to: StatusInReview, evType: "REOPENED", reason: op.ReasonCode, action: "compliance.case.reopened",
			set: map[string]any{"decision": nil, "decision_reason_code": nil, "resolution_status": nil, "decided_by": nil, "decided_at": nil,
				"approved_by": nil, "approved_at": nil, "closed_by": nil, "closed_at": nil, "closure_reason_code": nil},
			payload: prev, md: prev}, nil
	})
}

// AddNote appends an encrypted staff note (any status). The note text never appears in audit metadata,
// events or logs.
func (s *Service) AddNote(ctx context.Context, op Op) (Note, error) {
	body, ok := cleanNote(op.Note)
	if !ok || body == "" {
		return Note{}, httpx.Validation(errs.Detail{Field: "body", Code: "INVALID_LENGTH"})
	}
	var n Note
	err := s.tx(ctx, op.STRAccess, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if !ids.Valid(op.CaseID) {
			return errNotFound()
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM compliance.compliance_cases WHERE id = $1)`, op.CaseID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errNotFound()
		}
		id, err := s.insertNote(ctx, tx, op.CaseID, op.Actor.ID, body)
		if err != nil {
			return err
		}
		auditID, err := s.audit(ctx, tx, op.Actor, "compliance.case.note_added", op.CaseID, map[string]any{"note_id": id})
		if err != nil {
			return err
		}
		n = Note{ID: id, AuthorID: op.Actor.ID, Visibility: "STAFF_ONLY", Body: body, CreatedAt: s.now()}
		return s.event(ctx, tx, op.CaseID, nil, "NOTE_ADDED", nil, nil, op.Actor, "", map[string]any{"note_id": id}, auditID)
	})
	return n, mapDBError(err)
}

// ---- reads -------------------------------------------------------------------------------------------------

func (s *Service) load(ctx context.Context, id string, strAccess bool) (Case, error) {
	var c Case
	err := s.tx(ctx, strAccess, func(ctx context.Context, tx pgx.Tx) error {
		row, err := scanCase(tx.QueryRow(ctx, `SELECT `+caseCols+` FROM compliance.compliance_cases WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound()
		}
		if err != nil {
			return err
		}
		c = row.Case
		c.Links, err = s.currentLinks(ctx, tx, id)
		return err
	})
	return c, err
}

// Get returns a case with its timeline and decrypted notes, and audits the access (notes are C3).
func (s *Service) Get(ctx context.Context, actor Actor, id string, strAccess bool) (CaseDetail, error) {
	var d CaseDetail
	if !ids.Valid(id) {
		return d, errNotFound()
	}
	err := s.tx(ctx, strAccess, func(ctx context.Context, tx pgx.Tx) error {
		row, err := scanCase(tx.QueryRow(ctx, `SELECT `+caseCols+` FROM compliance.compliance_cases WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound()
		}
		if err != nil {
			return err
		}
		d.Case = row.Case
		if d.Links, err = s.currentLinks(ctx, tx, id); err != nil {
			return err
		}
		if d.Events, err = s.events(ctx, tx, id); err != nil {
			return err
		}
		if d.Notes, err = s.notes(ctx, tx, id); err != nil {
			return err
		}
		_, err = s.audit(ctx, tx, actor, "compliance.case.viewed", id, map[string]any{"case_number": d.CaseNumber, "notes": len(d.Notes)})
		return err
	})
	return d, err
}

func (s *Service) events(ctx context.Context, tx pgx.Tx, caseID string) ([]Event, error) {
	rows, err := tx.Query(ctx, `SELECT id, case_version, event_type, from_status, to_status, actor_type, actor_id, actor_job, reason_code,
		payload, occurred_at FROM compliance.compliance_case_events WHERE case_id = $1 ORDER BY occurred_at, id`, caseID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) {
		var e Event
		var pj []byte
		if err := r.Scan(&e.ID, &e.CaseVersion, &e.EventType, &e.FromStatus, &e.ToStatus, &e.ActorType, &e.ActorID, &e.ActorJob,
			&e.ReasonCode, &pj, &e.OccurredAt); err != nil {
			return e, err
		}
		return e, json.Unmarshal(pj, &e.Payload)
	})
	if out == nil {
		out = []Event{}
	}
	return out, err
}

func (s *Service) notes(ctx context.Context, tx pgx.Tx, caseID string) ([]Note, error) {
	rows, err := tx.Query(ctx, `SELECT id, author_id, visibility, body_ciphertext, created_at FROM compliance.compliance_case_notes
		WHERE case_id = $1 ORDER BY created_at, id`, caseID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Note, error) {
		var n Note
		var sealed []byte
		if err := r.Scan(&n.ID, &n.AuthorID, &n.Visibility, &sealed, &n.CreatedAt); err != nil {
			return n, err
		}
		plain, err := s.aead.Open(sealed, []byte(n.ID))
		if err != nil {
			return n, fmt.Errorf("compliance: note %s cannot be decrypted", n.ID) // never the content
		}
		n.Body = string(plain)
		return n, nil
	})
	if out == nil {
		out = []Note{}
	}
	return out, err
}

// ListFilter filters the case queue.
type ListFilter struct {
	Status, Severity string
	Assigned         string // me | unassigned | any (default)
	Me               string // caller's id for assigned=me
	Limit            int
	Cursor           string // id of the last case of the previous page
}

// List returns cases newest first (UUIDv7 ids sort by creation) and the next cursor ("" at the end).
func (s *Service) List(ctx context.Context, f ListFilter, strAccess bool) ([]Case, string, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Severity != "" {
		add("severity = $%d", f.Severity)
	}
	switch f.Assigned {
	case "me":
		add("assigned_to = $%d", f.Me)
	case "unassigned":
		where = append(where, "assigned_to IS NULL")
	}
	if f.Cursor != "" {
		add("id < $%d", f.Cursor)
	}
	q := `SELECT ` + caseCols + ` FROM compliance.compliance_cases`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit+1)
	q += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, len(args))
	var out []Case
	err := s.tx(ctx, strAccess, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (caseRow, error) { return scanCase(r) })
		if err != nil {
			return err
		}
		for _, r := range rs {
			out = append(out, r.Case)
		}
		for i := range out {
			if out[i].Links, err = s.currentLinks(ctx, tx, out[i].ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	if out == nil {
		out = []Case{}
	}
	return out, next, nil
}
