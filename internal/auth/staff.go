package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Staff directory, role inventory, effective permissions and session revocation (Stage 14, ADR-046 §6). The staff
// directory shows what operations needs to govern access — display name, roles, status, MFA and last activity — and
// no email, phone or personal data. Granting and revoking roles stays the dual-controlled role request flow.

// StaffMember is a staff directory row.
type StaffMember struct {
	users.StaffAccount
	Roles          []string   `json:"roles"`
	MFAConfirmed   bool       `json:"mfa_confirmed"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	ActiveSessions int        `json:"active_sessions"`
}

// StaffDirectory lists staff accounts with their active roles.
func (s *Service) StaffDirectory(ctx context.Context, status, query string) ([]StaffMember, error) {
	accts, err := users.ListStaff(ctx, s.Pool, status, query, 300)
	if err != nil {
		return nil, err
	}
	out := make([]StaffMember, 0, len(accts))
	idx := map[string]int{}
	idList := make([]string, 0, len(accts))
	for i, a := range accts {
		out = append(out, StaffMember{StaffAccount: a, Roles: []string{}})
		idx[a.ID] = i
		idList = append(idList, a.ID)
	}
	if len(idList) == 0 {
		return out, nil
	}
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT ra.user_id::text, r.code FROM app.role_assignments ra JOIN app.roles r ON r.id = ra.role_id
		WHERE ra.user_id = ANY($1::uuid[]) AND ra.revoked_at IS NULL AND (ra.expires_at IS NULL OR ra.expires_at > $2) ORDER BY r.code`, idList, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, code string
		if err := rows.Scan(&id, &code); err != nil {
			rows.Close()
			return nil, err
		}
		out[idx[id]].Roles = append(out[idx[id]].Roles, code)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT u.id::text,
		EXISTS (SELECT 1 FROM app.mfa_methods m WHERE m.user_id = u.id AND m.confirmed_at IS NOT NULL AND m.disabled_at IS NULL),
		(SELECT max(last_seen_at) FROM app.sessions x WHERE x.user_id = u.id),
		(SELECT count(*) FROM app.sessions x WHERE x.user_id = u.id AND x.revoked_at IS NULL AND x.absolute_expires_at > $2 AND x.idle_expires_at > $2)
		FROM unnest($1::uuid[]) AS u(id)`, idList, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var mfa bool
		var last *time.Time
		var n int
		if err := rows.Scan(&id, &mfa, &last, &n); err != nil {
			return nil, err
		}
		m := &out[idx[id]]
		m.MFAConfirmed, m.LastActivityAt, m.ActiveSessions = mfa, last, n
	}
	return out, rows.Err()
}

// RoleHistoryItem is one role assignment (active or revoked) of a staff member.
type RoleHistoryItem struct {
	Role         string     `json:"role"`
	GrantedBy    *string    `json:"granted_by"`
	ApprovedBy   *string    `json:"approved_by"`
	ValidFrom    time.Time  `json:"valid_from"`
	ExpiresAt    *time.Time `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
	RevokedBy    *string    `json:"revoked_by"`
	RevokeReason *string    `json:"revoke_reason"`
}

// StaffDetail is one staff member with role history, pending role requests and recent security events (types only).
type StaffDetail struct {
	Member         StaffMember       `json:"member"`
	Permissions    []string          `json:"permissions"`
	RoleHistory    []RoleHistoryItem `json:"role_history"`
	PendingChanges []RoleRequest     `json:"pending_role_requests"`
	SecurityEvents []SecurityEvent   `json:"recent_security_events"`
}

// SecurityEvent is a security telemetry event of a staff account (no IP or user agent in this view).
type SecurityEvent struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
}

// StaffDetailOf returns one staff member (ErrNotFound-like 404 for unknown or non-staff ids).
func (s *Service) StaffDetailOf(ctx context.Context, id string) (StaffDetail, error) {
	if !ids.Valid(id) {
		return StaffDetail{}, errNotFound("STAFF_NOT_FOUND", "No such staff account.")
	}
	list, err := s.StaffDirectory(ctx, "", id)
	if err != nil {
		return StaffDetail{}, err
	}
	if len(list) != 1 || list[0].ID != id {
		return StaffDetail{}, errNotFound("STAFF_NOT_FOUND", "No such staff account.")
	}
	d := StaffDetail{Member: list[0], RoleHistory: []RoleHistoryItem{}, PendingChanges: []RoleRequest{}, SecurityEvents: []SecurityEvent{}}
	if d.Permissions, err = s.EffectivePermissions(ctx, id); err != nil {
		return StaffDetail{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT r.code, ra.granted_by::text, ra.approved_by::text, ra.valid_from, ra.expires_at, ra.revoked_at, ra.revoked_by::text,
		ra.revoke_reason FROM app.role_assignments ra JOIN app.roles r ON r.id = ra.role_id WHERE ra.user_id = $1 ORDER BY ra.valid_from DESC LIMIT 100`, id)
	if err != nil {
		return StaffDetail{}, err
	}
	for rows.Next() {
		var h RoleHistoryItem
		if err := rows.Scan(&h.Role, &h.GrantedBy, &h.ApprovedBy, &h.ValidFrom, &h.ExpiresAt, &h.RevokedAt, &h.RevokedBy, &h.RevokeReason); err != nil {
			rows.Close()
			return StaffDetail{}, err
		}
		d.RoleHistory = append(d.RoleHistory, h)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT id::text FROM app.role_assignment_requests WHERE target_user_id = $1 AND status = 'PENDING' AND expires_at > $2
		ORDER BY created_at`, id, s.now())
	if err != nil {
		return StaffDetail{}, err
	}
	var reqIDs []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			rows.Close()
			return StaffDetail{}, err
		}
		reqIDs = append(reqIDs, x)
	}
	rows.Close()
	for _, x := range reqIDs {
		rr, err := s.loadRoleRequest(ctx, s.Pool, x)
		if err != nil {
			return StaffDetail{}, err
		}
		d.PendingChanges = append(d.PendingChanges, rr)
	}
	rows, err = s.Pool.Query(ctx, `SELECT event_type, occurred_at FROM app.security_events WHERE user_id = $1 ORDER BY occurred_at DESC LIMIT 20`, id)
	if err != nil {
		return StaffDetail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e SecurityEvent
		if err := rows.Scan(&e.Type, &e.OccurredAt); err != nil {
			return StaffDetail{}, err
		}
		d.SecurityEvents = append(d.SecurityEvents, e)
	}
	return d, rows.Err()
}

// EffectivePermissions returns the permission codes of an ACTIVE staff account's active roles (sorted, unique; empty for
// anyone else).
func (s *Service) EffectivePermissions(ctx context.Context, userID string) ([]string, error) {
	if !ids.Valid(userID) {
		return []string{}, nil
	}
	acct, err := users.ByID(ctx, s.Pool, userID)
	if errors.Is(err, users.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if acct.Kind != users.KindStaff || acct.Status != users.StatusActive {
		return []string{}, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT p.code FROM app.role_assignments ra JOIN app.role_permissions rp ON rp.role_id = ra.role_id
		JOIN app.permissions p ON p.id = rp.permission_id
		WHERE ra.user_id = $1 AND ra.revoked_at IS NULL AND (ra.expires_at IS NULL OR ra.expires_at > $2) ORDER BY p.code`, userID, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RoleInventoryItem is a role with its permissions and how many staff hold it.
type RoleInventoryItem struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	Holders     int      `json:"holders"`
}

// RoleInventory lists every role, its permissions and active holder count.
func (s *Service) RoleInventory(ctx context.Context) ([]RoleInventoryItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.code, r.name, coalesce(array_agg(DISTINCT p.code) FILTER (WHERE p.code IS NOT NULL), '{}'),
		(SELECT count(*) FROM app.role_assignments ra WHERE ra.role_id = r.id AND ra.revoked_at IS NULL AND (ra.expires_at IS NULL OR ra.expires_at > $1))
		FROM app.roles r LEFT JOIN app.role_permissions rp ON rp.role_id = r.id LEFT JOIN app.permissions p ON p.id = rp.permission_id
		GROUP BY r.id, r.code, r.name ORDER BY r.code`, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoleInventoryItem{}
	for rows.Next() {
		var x RoleInventoryItem
		if err := rows.Scan(&x.Code, &x.Name, &x.Permissions, &x.Holders); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeStaffSessionsByStaff ends every session of a staff account (session.revoke; step-up by route policy; reason;
// security audit). Nobody revokes their own sessions here (the account page does that).
func (s *Service) RevokeStaffSessionsByStaff(ctx context.Context, actorID, target, reason string) (int, error) {
	r, ok := justification(reason)
	if !ok {
		return 0, httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"})
	}
	if !ids.Valid(target) {
		return 0, errNotFound("STAFF_NOT_FOUND", "No such staff account.")
	}
	if target == actorID {
		return 0, errs.New(errs.Forbidden, "SELF_ACTION_FORBIDDEN", "Use your account's sessions page to end your own sessions.")
	}
	n := 0
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		acct, err := users.ByIDForUpdate(ctx, tx, target)
		if errors.Is(err, users.ErrNotFound) || err == nil && (acct.IsSystem || acct.Kind != users.KindStaff) {
			return errNotFound("STAFF_NOT_FOUND", "No such staff account.")
		}
		if err != nil {
			return err
		}
		if n, err = s.revokeSessions(ctx, tx, target, "", RevokeStaff); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "session.revoked_by_staff", TargetType: "user", TargetID: target,
			Reason: r, Metadata: map[string]any{"revoked_count": n}})
	})
	return n, err
}

// ---- HTTP ------------------------------------------------------------------------------------------------------------

// ListStaff is GET /api/v1/admin/staff (staff.view).
func (s *Service) ListStaff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.StaffDirectory(r.Context(), q.Get("status"), q.Get("q"))
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": list, "next_cursor": nil})
}

// GetStaff is GET /api/v1/admin/staff/{user_id} (staff.view). Each view is audited.
func (s *Service) GetStaff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := s.StaffDetailOf(ctx, r.PathValue("user_id"))
	if err == nil {
		err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "staff.viewed", TargetType: "user", TargetID: d.Member.ID})
		})
	}
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, d)
}

// ListRoles is GET /api/v1/admin/staff/roles (staff.view): the role and permission inventory.
func (s *Service) ListRoles(w http.ResponseWriter, r *http.Request) {
	list, err := s.RoleInventory(r.Context())
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": list, "next_cursor": nil})
}

// RevokeStaffSessions is POST /api/v1/admin/staff/{user_id}/sessions/revoke (session.revoke + step-up).
func (s *Service) RevokeStaffSessions(w http.ResponseWriter, r *http.Request) {
	var req statusChangeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	n, err := s.RevokeStaffSessionsByStaff(r.Context(), authz.PrincipalFrom(r.Context()).UserID, r.PathValue("user_id"), req.Reason)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"revoked_sessions": n})
}

// OpsSnapshot is the identity module's contribution to the operations portal: role changes awaiting a checker and
// staff account counts (Stage 14). Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "identity"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	now := s.now()
	counts, err := users.StaffCounts(ctx, s.Pool)
	if err != nil {
		return snap, err
	}
	active, suspended := counts[users.StatusActive], counts[users.StatusSuspended]
	activeIDs, err := users.ActiveStaffIDs(ctx, s.Pool)
	if err != nil {
		return snap, err
	}
	var noMFA, requests int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM unnest($1::uuid[]) AS u(id) WHERE NOT EXISTS (
		   SELECT 1 FROM app.mfa_methods m WHERE m.user_id = u.id AND m.confirmed_at IS NOT NULL AND m.disabled_at IS NULL)),
		(SELECT count(*) FROM app.role_assignment_requests WHERE status = 'PENDING' AND expires_at > $2)`, activeIDs, now).Scan(&noMFA, &requests); err != nil {
		return snap, err
	}
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "staff.active", "Active staff accounts", active, "staff.view", "app.users STAFF ACTIVE", "/admin/staff"),
		opsview.Count(m, "staff.suspended", "Suspended staff accounts", suspended, "staff.view", "app.users STAFF SUSPENDED", "/admin/staff"),
		opsview.Count(m, "staff.without_mfa", "Active staff without confirmed MFA (cannot sign in)", noMFA, "staff.view",
			"active STAFF without a confirmed MFA method", "/admin/staff"),
		opsview.Count(m, "staff.role_requests.pending", "Role changes awaiting a checker", requests, "role.assign.approve",
			"app.role_assignment_requests PENDING, not expired", "/admin/staff"),
	)
	rows, err := s.Pool.Query(ctx, `SELECT q.id::text, q.action, r.code, q.target_user_id::text, q.requested_by::text, q.created_at, q.status
		FROM app.role_assignment_requests q JOIN app.roles r ON r.id = q.role_id WHERE q.status = 'PENDING' AND q.expires_at > $1
		ORDER BY q.created_at LIMIT 200`, now)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a opsview.Approval
		var action, role, target string
		if err := rows.Scan(&a.SourceRecordID, &action, &role, &target, &a.RequestedBy, &a.RequestedAt, &a.Status); err != nil {
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":role_request:"+a.SourceRecordID, m, "ROLE_"+action, "role.assign.approve"
		a.Summary, a.Makers, a.RiskLevel = "Role "+action+" "+role, []string{a.RequestedBy, target}, "PRIVILEGED"
		a.Href = "/admin/staff/" + target
		snap.Approvals = append(snap.Approvals, a)
	}
	return snap, rows.Err()
}

// RequestRoleRevocation files a dual-controlled role REVOKE request on behalf of an access reviewer (the maker; another
// holder of role.assign.approve must decide it). Nobody files one about themselves.
func (s *Service) RequestRoleRevocation(ctx context.Context, maker, target, roleCode, justification string) (string, error) {
	if maker == target {
		return "", errs.New(errs.Forbidden, "SELF_REQUEST_FORBIDDEN", "You cannot request a role change for yourself.")
	}
	just, ok := justificationText(justification)
	if !ok {
		return "", httpx.Validation(errs.Detail{Field: "justification", Code: "INVALID_LENGTH"})
	}
	return s.createRoleRequest(ctx, maker, target, roleCode, "REVOKE", just)
}

// ActiveRoleAssignments lists active (staff id, role code) pairs, optionally for one role (access review inventory).
func (s *Service) ActiveRoleAssignments(ctx context.Context, roleCode string) ([][2]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT ra.user_id::text, r.code FROM app.role_assignments ra JOIN app.roles r ON r.id = ra.role_id
		WHERE ra.revoked_at IS NULL AND (ra.expires_at IS NULL OR ra.expires_at > $2) AND ra.user_account_kind = 'STAFF'
		AND ($1 = '' OR r.code = $1) ORDER BY r.code, ra.user_id`, roleCode, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][2]string{}
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func justificationText(s string) (string, bool) {
	if rs := []rune(s); len(rs) > 1000 {
		s = string(rs[:1000])
	}
	return justification(s)
}

// StaffWithPermission returns the ACTIVE staff accounts that currently hold a permission through an active role
// assignment (notification recipients, Stage 15). Break-glass grants are not used for routing notifications.
func (s *Service) StaffWithPermission(ctx context.Context, permission string) ([]string, error) {
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ra.user_id::text FROM app.role_assignments ra
		JOIN app.role_permissions rp ON rp.role_id = ra.role_id JOIN app.permissions p ON p.id = rp.permission_id
		WHERE p.code = $1 AND ra.revoked_at IS NULL AND ra.valid_from <= $2 AND (ra.expires_at IS NULL OR ra.expires_at > $2)`, permission, now)
	if err != nil {
		return nil, err
	}
	holders, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(holders) == 0 {
		return holders, err
	}
	active, err := users.ActiveStaffIDs(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	ok := map[string]bool{}
	for _, id := range active {
		ok[id] = true
	}
	out := holders[:0]
	for _, id := range holders {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
