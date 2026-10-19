// Package operations is the Stage 14 operations portal backend (ADR-046): the command center, the approval and
// exception centers, support cases, operational incidents, access reviews, internal SLA targets, platform health,
// operational reports, global search and in-app task indicators.
//
// It orchestrates; it does not decide. Every domain figure comes from a module's own snapshot (opsview.Snapshot built
// from that module's tables), every decision is taken by the owning module's existing endpoint and service, and the
// module owns only its operational records (app.ops_*). It imports platform and audit only.
package operations

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Permissions (migration 20261017090100).
const (
	PermDashboard      = "ops.dashboard.view"
	PermSupportView    = "ops.support.view"
	PermSupportWork    = "ops.support.work"
	PermExceptionWork  = "ops.exception.work"
	PermIncidentView   = "ops.incident.view"
	PermIncidentManage = "ops.incident.manage"
	PermStaffView      = "staff.view"
	PermReviewRun      = "access.review.run"
	PermReviewCertify  = "access.review.certify"
	PermAuditRead      = "audit.read"
	PermAuditExport    = "audit.export"
	PermSecurityAudit  = "security_audit.read"
	PermSLARequest     = "ops.sla.request"
	PermSLAApprove     = "ops.sla.approve"
)

// Source is one module's contribution (implemented over each module's OpsSnapshot in internal/app).
type Source struct {
	Module   string
	Snapshot func(ctx context.Context) (opsview.Snapshot, error)
	// Search is optional; Permission is what a viewer needs to see its hits.
	Search           func(ctx context.Context, q string) ([]opsview.SearchHit, error)
	SearchPermission string
}

// Identities resolves linked accounts (kyc.Service.ActorIdentities).
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// Permissions returns a staff member's effective permissions (auth.Service.EffectivePermissions).
type Permissions interface {
	EffectivePermissions(ctx context.Context, userID string) ([]string, error)
}

// RoleRevoker files a dual-controlled role REVOKE request (auth; access review recommendations).
type RoleRevoker interface {
	RequestRoleRevocation(ctx context.Context, maker, target, roleCode, justification string) (string, error)
}

// StaffRoles lists active (staff, role) pairs for access reviews.
type StaffRoles interface {
	ActiveRoleAssignments(ctx context.Context, roleCode string) ([][2]string, error)
}

// HealthProbe reports one infrastructure dependency (database, cache, storage, …).
type HealthProbe struct {
	Name  string
	Check func(ctx context.Context) error
}

// Service is the operations module.
type Service struct {
	Pool        *pgxpool.Pool
	Clock       clock.Clock
	Logger      *slog.Logger
	Sources     []Source
	Identities  Identities
	Perms       Permissions
	Revoker     RoleRevoker
	StaffRoles  StaffRoles
	Probes      []HealthProbe
	Environment string // APP_ENV
	// SourceTimeout bounds each source (default 3s): a slow module degrades its panel, not the page.
	SourceTimeout time.Duration
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Errors (stable codes).
var (
	ErrNotFound       = errs.New(errs.NotFound, "OPS_NOT_FOUND", "No such operations record.")
	ErrState          = errs.New(errs.Conflict, "OPS_STATE", "The record is not in a state that allows this action.")
	ErrStale          = errs.New(errs.Conflict, "OPS_RECORD_CHANGED", "The record changed since you loaded it. Reload and try again.")
	ErrSelf           = errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "You cannot decide something you proposed, or something made by an account linked to yours.")
	ErrConflict       = errs.New(errs.Forbidden, "OPS_CONFLICT_OF_INTEREST", "You cannot review your own access or that of an account linked to yours.")
	ErrStepUp         = errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	ErrExceptionGone  = errs.New(errs.NotFound, "OPS_EXCEPTION_NOT_OPEN", "The owning module no longer reports this exception as open.")
	ErrNotReviewer    = errs.New(errs.Forbidden, "OPS_NOT_REVIEWER", "Only the reviewer named on this access review can decide its items.")
	ErrDomainOpen     = errs.New(errs.Conflict, "OPS_EXCEPTION_DOMAIN_OPEN", "The owning module still reports this exception as open: resolve it there first.")
	ErrAssigneeDenied = errs.New(errs.Unprocessable, errs.CodeValidationFailed, "The assignee does not hold the permission this work needs.")
)

func validation(field, code string) error {
	e := errs.New(errs.Unprocessable, errs.CodeValidationFailed, "Some fields are invalid.")
	e.Details = []errs.Detail{{Field: field, Code: code}}
	return e
}

func text(field, v string, minLen, maxLen int) (string, error) {
	v = strings.TrimSpace(v)
	if n := utf8.RuneCountInString(v); n < minLen || n > maxLen {
		return "", validation(field, "INVALID_LENGTH")
	}
	return v, nil
}

func mapDB(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch {
	case pg.Code == "23514" && strings.Contains(pg.Message, "maker-checker"):
		return ErrSelf
	case pg.Code == "23514" && strings.Contains(pg.Message, "conflict of interest"):
		return ErrConflict
	case pg.Code == "23514" && strings.Contains(pg.Message, "illegal "):
		return ErrState
	case pg.Code == "23505" && strings.Contains(pg.ConstraintName, "uq_ops_sla_targets"):
		return errs.New(errs.Conflict, "OPS_SLA_TARGET_PENDING", "A target for this metric is already awaiting approval.")
	}
	return err
}

// ---- viewer ----------------------------------------------------------------------------------------------------------

// Viewer is the staff member a read model is built for.
type Viewer struct {
	StaffID     string
	Permissions map[string]bool
	Identities  map[string]bool // the viewer and linked accounts
}

// ViewerFor loads a viewer's permissions and linked identities.
func (s *Service) ViewerFor(ctx context.Context, staffID string) (Viewer, error) {
	v := Viewer{StaffID: staffID, Permissions: map[string]bool{}, Identities: map[string]bool{staffID: true}}
	if s.Perms != nil {
		ps, err := s.Perms.EffectivePermissions(ctx, staffID)
		if err != nil {
			return v, err
		}
		for _, p := range ps {
			v.Permissions[p] = true
		}
	}
	if s.Identities != nil {
		idl, err := s.Identities.ActorIdentities(ctx, staffID)
		if err != nil {
			return v, err
		}
		for _, id := range idl {
			v.Identities[id] = true
		}
	}
	return v, nil
}

// Can reports whether the viewer holds a permission.
func (v Viewer) Can(p string) bool { return p == "" || v.Permissions[p] }

// ---- aggregation ------------------------------------------------------------------------------------------------------

// SourceStatus says whether a module's snapshot could be read.
type SourceStatus struct {
	Module string `json:"module"`
	Status string `json:"status"` // HEALTHY (read) | UNAVAILABLE (error or timeout)
	Error  string `json:"error,omitempty"`
}

type collected struct {
	snaps    []opsview.Snapshot
	statuses []SourceStatus
}

// collect reads every source concurrently, each bounded; a failed source is reported, never invented.
func (s *Service) collect(ctx context.Context) collected {
	to := s.SourceTimeout
	if to <= 0 {
		to = 3 * time.Second
	}
	out := collected{snaps: make([]opsview.Snapshot, len(s.Sources)), statuses: make([]SourceStatus, len(s.Sources))}
	var wg sync.WaitGroup
	for i, src := range s.Sources {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					out.statuses[i] = SourceStatus{Module: src.Module, Status: opsview.Unavailable, Error: "SOURCE_PANIC"}
				}
			}()
			c, cancel := context.WithTimeout(ctx, to)
			defer cancel()
			snap, err := src.Snapshot(c)
			if err != nil {
				if s.Logger != nil {
					s.Logger.Warn("operations: source unavailable", "module", src.Module, "error", err.Error())
				}
				out.statuses[i] = SourceStatus{Module: src.Module, Status: opsview.Unavailable, Error: "SOURCE_UNAVAILABLE"}
				out.snaps[i] = opsview.Snapshot{Module: src.Module}
				return
			}
			out.snaps[i], out.statuses[i] = snap, SourceStatus{Module: src.Module, Status: opsview.Healthy}
		}(i, src)
	}
	wg.Wait()
	return out
}

// ApprovalView is an approval as a viewer sees it.
type ApprovalView struct {
	opsview.Approval
	// CanDecide: the viewer holds the permission and is not a maker (the owning module re-checks everything,
	// including linked accounts of the subject, current state and step-up).
	CanDecide bool   `json:"can_decide"`
	Blocked   string `json:"blocked_reason,omitempty"` // MAKER | PERMISSION
	AgeSecs   int64  `json:"age_seconds"`
}

// ApprovalFilter filters the approval center.
type ApprovalFilter struct {
	Module, Action string
	MineOnly       bool // only items the viewer can decide
}

// Approvals is the unified approval queue: pending maker-checker decisions the viewer may see, oldest first.
func (s *Service) Approvals(ctx context.Context, v Viewer, f ApprovalFilter) ([]ApprovalView, []SourceStatus, error) {
	c := s.collect(ctx)
	now := s.now()
	out := []ApprovalView{}
	for _, snap := range c.snaps {
		for _, a := range snap.Approvals {
			if (f.Module != "" && a.SourceModule != f.Module) || (f.Action != "" && a.Action != f.Action) {
				continue
			}
			// visible to anyone who could decide it or who holds the module's view; here: who holds the decision permission
			// or made it (a maker can follow their own request)
			maker := false
			for _, m := range a.Makers {
				if m != "" && v.Identities[m] {
					maker = true
				}
			}
			if !v.Can(a.RequiredPermission) && !maker {
				continue
			}
			av := ApprovalView{Approval: a, AgeSecs: int64(now.Sub(a.RequestedAt).Seconds())}
			switch {
			case maker:
				av.Blocked = "MAKER"
			case !v.Can(a.RequiredPermission):
				av.Blocked = "PERMISSION"
			default:
				av.CanDecide = true
			}
			if f.MineOnly && !av.CanDecide {
				continue
			}
			out = append(out, av)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RequestedAt.Before(out[j].RequestedAt) })
	return out, c.statuses, nil
}

// Outbox events written by the operations module (Stage 15, ADR-047: the notification engine consumes them). Payloads
// carry ids and numbers only — never subjects, notes or resolutions.
const (
	EvSupportCaseOpened        = "operations.support_case_opened"
	EvSupportCaseAssigned      = "operations.support_case_assigned"
	EvSupportCaseStatusChanged = "operations.support_case_status_changed"
	EvIncidentDeclared         = "operations.incident_declared"
)

func emit(ctx context.Context, tx pgx.Tx, aggType, aggID, eventType string, payload map[string]any, now time.Time) error {
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: aggType, AggregateID: aggID, EventType: eventType, Payload: payload, OccurredAt: now})
	return err
}
