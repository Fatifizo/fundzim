package operations

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// The exception center (brief §45–§47): unresolved exceptions reported by the owning modules, with the operations
// triage workflow around them (assignment, acknowledgement, notes, waiting on a third party). Triage never resolves the
// domain record: a payout exception is resolved by payouts, a ledger exception by the ledger, and so on. An operations
// RESOLVED means "triage finished" and is refused while the owning module still reports the exception as open
// (WAITING_EXTERNAL is the state for "waiting on the owning team or a third party").

// ExceptionEdges mirrors the 'ops_exception' machine (keep identical; TestOpsEdgesMatchDatabase).
var ExceptionEdges = map[string][]string{
	"":                 {"OPEN"},
	"OPEN":             {"ACKNOWLEDGED"},
	"REOPENED":         {"ACKNOWLEDGED"},
	"ACKNOWLEDGED":     {"INVESTIGATING", "RESOLVED"},
	"INVESTIGATING":    {"WAITING_EXTERNAL", "RESOLVED"},
	"WAITING_EXTERNAL": {"INVESTIGATING", "RESOLVED"},
	"RESOLVED":         {"CLOSED", "REOPENED"},
	"CLOSED":           {"REOPENED"},
}

// ExceptionView is a domain exception with its triage state.
type ExceptionView struct {
	opsview.Exception
	DomainOpen   bool       `json:"domain_open"` // the owning module still reports it
	TriageID     *string    `json:"triage_id"`
	TriageStatus string     `json:"triage_status"` // UNTRIAGED when no triage record exists
	AssignedTo   *string    `json:"assigned_to"`
	TriageNote   *string    `json:"triage_resolution"`
	UpdatedAt    *time.Time `json:"triage_updated_at"`
	Version      int        `json:"triage_version"`
}

// ExceptionFilter filters the exception center.
type ExceptionFilter struct {
	Module, Severity, Status string // Status: a triage status or UNTRIAGED
	AssignedToMe             bool
}

// Exceptions lists open domain exceptions the viewer may see, plus triage records whose domain exception has closed
// but whose triage is not finished.
func (s *Service) Exceptions(ctx context.Context, v Viewer, f ExceptionFilter) ([]ExceptionView, []SourceStatus, error) {
	c := s.collect(ctx)
	byID := map[string]*ExceptionView{}
	out := []*ExceptionView{}
	for _, snap := range c.snaps {
		for _, e := range snap.Exceptions {
			if !v.Can(e.Permission) {
				continue
			}
			x := &ExceptionView{Exception: e, DomainOpen: true, TriageStatus: "UNTRIAGED"}
			byID[e.SourceModule+"|"+e.ID] = x
			out = append(out, x)
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, source_module, source_ref, status, assigned_to::text, severity, category, summary, resolution, updated_at,
		version, created_at, view_permission FROM app.ops_exception_assignments WHERE status NOT IN ('CLOSED') OR updated_at > now() - interval '7 days'`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, module, ref, status, sev, cat, summary, viewPerm string
		var assignee, resolution *string
		var updated, created time.Time
		var version int
		if err := rows.Scan(&id, &module, &ref, &status, &assignee, &sev, &cat, &summary, &resolution, &updated, &version, &created, &viewPerm); err != nil {
			return nil, nil, err
		}
		x, ok := byID[module+"|"+ref]
		if !ok {
			if status == "CLOSED" {
				continue
			}
			// the domain no longer reports it (resolved there, or outside the snapshot window); the triage record stays visible
			x = &ExceptionView{Exception: opsview.Exception{ID: ref, SourceModule: module, Severity: sev, Category: cat, Summary: summary, CreatedAt: created,
				DomainStatus: "NOT_REPORTED", Permission: viewPerm}}
			if !v.Can(PermExceptionWork) || !v.Can(viewPerm) {
				continue
			}
			out = append(out, x)
		}
		tid, u := id, updated
		x.TriageID, x.TriageStatus, x.AssignedTo, x.TriageNote, x.UpdatedAt, x.Version = &tid, status, assignee, resolution, &u, version
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	res := []ExceptionView{}
	for _, x := range out {
		if (f.Module != "" && x.SourceModule != f.Module) || (f.Severity != "" && x.Severity != f.Severity) || (f.Status != "" && x.TriageStatus != f.Status) {
			continue
		}
		if f.AssignedToMe && (x.AssignedTo == nil || *x.AssignedTo != v.StaffID) {
			continue
		}
		res = append(res, *x)
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Severity != res[j].Severity {
			return res[i].Severity < res[j].Severity
		}
		return res[i].CreatedAt.Before(res[j].CreatedAt)
	})
	return res, c.statuses, nil
}

// findOpen returns the domain exception if its module currently reports it.
func (s *Service) findOpen(ctx context.Context, module, ref string) (*opsview.Exception, error) {
	for _, src := range s.Sources {
		if src.Module != module {
			continue
		}
		snap, err := src.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range snap.Exceptions {
			if e.ID == ref {
				x := e
				return &x, nil
			}
		}
	}
	return nil, nil
}

// TriageOp is a triage action.
type TriageOp struct {
	StaffID         string
	Module, Ref     string
	To              string // target status (ACKNOWLEDGED, INVESTIGATING, WAITING_EXTERNAL, RESOLVED, CLOSED, REOPENED) or "" to only (re)assign
	AssigneeID      string
	Note            string
	ExpectedVersion int
}

// Triage creates the triage record on first touch (the exception must be open in its module and visible to the
// viewer), then applies an assignment and/or a transition, atomically with its history and audit.
func (s *Service) Triage(ctx context.Context, v Viewer, op TriageOp) (ExceptionView, error) {
	if !v.Can(PermExceptionWork) {
		return ExceptionView{}, ErrAssigneeDenied
	}
	note := strings.TrimSpace(op.Note)
	if len([]rune(note)) > 4000 {
		return ExceptionView{}, validation("note", "INVALID_LENGTH")
	}
	if op.To == "RESOLVED" || op.To == "CLOSED" || op.To == "REOPENED" {
		if len([]rune(note)) < 5 {
			return ExceptionView{}, validation("note", "REQUIRED")
		}
	}
	var assigneePerms []string
	if op.AssigneeID != "" {
		if !ids.Valid(op.AssigneeID) {
			return ExceptionView{}, validation("assignee_id", "INVALID_FORMAT")
		}
		if s.Perms != nil {
			ps, err := s.Perms.EffectivePermissions(ctx, op.AssigneeID)
			if err != nil {
				return ExceptionView{}, err
			}
			if !contains(ps, PermExceptionWork) {
				return ExceptionView{}, ErrAssigneeDenied
			}
			assigneePerms = ps
		}
	}
	dom, err := s.findOpen(ctx, op.Module, op.Ref)
	if err != nil {
		return ExceptionView{}, err
	}
	if dom != nil && !v.Can(dom.Permission) {
		return ExceptionView{}, ErrNotFound
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id, status, viewPerm string
		var version int
		err := tx.QueryRow(ctx, `SELECT id::text, status, version, view_permission FROM app.ops_exception_assignments
			WHERE source_module = $1 AND source_ref = $2 FOR UPDATE`, op.Module, op.Ref).Scan(&id, &status, &version, &viewPerm)
		if errors.Is(err, pgx.ErrNoRows) {
			if dom == nil {
				return ErrExceptionGone // only an exception its module reports as open can enter triage
			}
			id, status, version = ids.New(), "OPEN", 1
			if _, err := tx.Exec(ctx, `INSERT INTO app.ops_exception_assignments (id, source_module, source_ref, status, severity, category, summary,
				view_permission) VALUES ($1, $2, $3, 'OPEN', $4, $5, $6, $7)`, id, op.Module, op.Ref, dom.Severity, truncate(dom.Category, 120),
				truncate(dom.Summary, 300), dom.Permission); err != nil {
				return mapDB(err)
			}
			if err := exceptionEvent(ctx, tx, id, "TRIAGE_OPENED", op.StaffID, "", "OPEN", "", now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !v.Can(viewPerm) {
			return ErrNotFound // the module's own visibility rule still applies after it stops reporting the exception
		} else if op.ExpectedVersion > 0 && version != op.ExpectedVersion {
			return ErrStale
		}
		if op.AssigneeID != "" {
			need := viewPerm
			if dom != nil {
				need = dom.Permission
			}
			if assigneePerms != nil && !contains(assigneePerms, need) {
				return ErrAssigneeDenied // the assignee must be able to open the exception in its module
			}
			if _, err := tx.Exec(ctx, `UPDATE app.ops_exception_assignments SET assigned_to = $2, version = version + 1 WHERE id = $1`, id, op.AssigneeID); err != nil {
				return mapDB(err)
			}
			if err := exceptionEvent(ctx, tx, id, "ASSIGNED", op.StaffID, "", "", "assigned to "+op.AssigneeID, now); err != nil {
				return err
			}
			if status == "OPEN" && op.To == "" {
				op.To = "ACKNOWLEDGED"
			}
		}
		if op.To != "" {
			if !allowed(ExceptionEdges, status, op.To) {
				return ErrState
			}
			if op.To == "RESOLVED" && dom != nil {
				return ErrDomainOpen // resolution authority stays with the owning module
			}
			var res any
			if op.To == "RESOLVED" || op.To == "CLOSED" {
				res = note
			}
			if _, err := tx.Exec(ctx, `UPDATE app.ops_exception_assignments SET status = $2, resolution = coalesce($3, resolution), version = version + 1
				WHERE id = $1`, id, op.To, res); err != nil {
				return mapDB(err)
			}
			if err := exceptionEvent(ctx, tx, id, "STATUS_"+op.To, op.StaffID, status, op.To, note, now); err != nil {
				return err
			}
		} else if note != "" {
			if err := exceptionEvent(ctx, tx, id, "NOTE_ADDED", op.StaffID, "", "", note, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.exception.triaged", TargetType: "ops_exception", TargetID: id,
			Metadata: map[string]any{"source_module": op.Module, "source_ref": op.Ref, "to": op.To, "assignee": op.AssigneeID}})
	})
	if err != nil {
		return ExceptionView{}, err
	}
	list, _, err := s.Exceptions(ctx, v, ExceptionFilter{Module: op.Module})
	if err != nil {
		return ExceptionView{}, err
	}
	for _, x := range list {
		if x.ID == op.Ref {
			return x, nil
		}
	}
	return ExceptionView{Exception: opsview.Exception{ID: op.Ref, SourceModule: op.Module}, TriageStatus: op.To}, nil
}

func exceptionEvent(ctx context.Context, tx pgx.Tx, id, typ, actor, from, to, note string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.ops_exception_events (id, assignment_id, event_type, actor_id, from_status, to_status, note, occurred_at)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8)`, ids.New(), id, typ, actor, from, to, note, at)
	return err
}

// ExceptionHistory returns a triage record's history.
func (s *Service) ExceptionHistory(ctx context.Context, v Viewer, module, ref string) ([]HistoryEntry, error) {
	var perm string
	err := s.Pool.QueryRow(ctx, `SELECT view_permission FROM app.ops_exception_assignments WHERE source_module = $1 AND source_ref = $2`, module, ref).Scan(&perm)
	if errors.Is(err, pgx.ErrNoRows) {
		return []HistoryEntry{}, nil // not triaged yet
	}
	if err != nil {
		return nil, err
	}
	if !v.Can(PermExceptionWork) || !v.Can(perm) {
		return nil, ErrNotFound
	}
	return s.history(ctx, `SELECT e.event_type, e.actor_id::text, coalesce(e.from_status, ''), coalesce(e.to_status, ''), coalesce(e.note, ''), e.occurred_at
		FROM app.ops_exception_events e JOIN app.ops_exception_assignments a ON a.id = e.assignment_id
		WHERE a.source_module = $1 AND a.source_ref = $2 ORDER BY e.occurred_at, e.id`, module, ref)
}

// HistoryEntry is one event of an operations record.
type HistoryEntry struct {
	EventType  string    `json:"event_type"`
	ActorID    string    `json:"actor_id"`
	FromStatus string    `json:"from_status,omitempty"`
	ToStatus   string    `json:"to_status,omitempty"`
	Note       string    `json:"note,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (s *Service) history(ctx context.Context, sql string, args ...any) ([]HistoryEntry, error) {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var actor *string
		if err := rows.Scan(&h.EventType, &actor, &h.FromStatus, &h.ToStatus, &h.Note, &h.OccurredAt); err != nil {
			return nil, err
		}
		if actor != nil {
			h.ActorID = *actor
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func allowed(edges map[string][]string, from, to string) bool {
	for _, t := range edges[from] {
		if t == to {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
