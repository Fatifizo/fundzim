package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Staff administration (SECURITY §5, operational-controls §3):
//   - staff accounts exist only through invitation (no public staff registration); the invitee sets a
//     password and enrols TOTP in one step, so a staff account never exists without MFA;
//   - roles are granted and revoked only through maker-checker requests (the requester can never approve,
//     nobody can request or approve for themselves; enforced in the database too), approval needs a fresh
//     step-up, and the target's sessions are revoked so new privileges apply from the next sign-in;
//   - suspension revokes every session at once.

// RoleRequestTTL is how long a role request may wait for a checker (operational-controls §3: 24 h).
const RoleRequestTTL = 24 * time.Hour

// SystemActorID is the seeded non-login STAFF account used as the maker of bootstrap role requests.
const SystemActorID = "00000000-0000-0000-0000-000000000001"

func errNotFound(code, msg string) error { return errs.New(errs.NotFound, code, msg) }

func justification(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, len(s) >= 10 && len(s) <= 1000
}

// ---- staff invitations ----------------------------------------------------------------------------------

type staffInviteReq struct {
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	Justification string `json:"justification"`
}

// InviteStaff creates a STAFF account with no credentials and queues the invitation email. Roles are
// requested separately (maker-checker).
func (s *Service) InviteStaff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req staffInviteReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var details []errs.Detail
	email, err := users.NormalizeEmail(req.Email)
	if err != nil {
		details = append(details, errs.Detail{Field: "email", Code: "INVALID_EMAIL"})
	}
	name, ok := users.NormalizeDisplayName(req.DisplayName)
	if !ok {
		details = append(details, errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"})
	}
	just, ok := justification(req.Justification)
	if !ok {
		details = append(details, errs.Detail{Field: "justification", Code: "INVALID_LENGTH"})
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(details...))
		return
	}
	userID, err := s.inviteStaff(ctx, email, name, just, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, map[string]string{"user_id": userID, "status": "invited"})
}

func (s *Service) inviteStaff(ctx context.Context, email, name, just, invitedBy string) (string, error) {
	var userID string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var emailID string
		var err error
		userID, emailID, err = users.Create(ctx, tx, users.NewAccount{Kind: users.KindStaff, Email: email, DisplayName: name})
		if errors.Is(err, users.ErrEmailTaken) {
			return errs.New(errs.Conflict, "EMAIL_IN_USE", "This address is already used by an account. Staff accounts need their own work address.")
		}
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "staff.invited", TargetType: "user", TargetID: userID,
			Justification: just}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvStaffInvited, userID, map[string]any{"user_id": userID, "email_id": emailID, "invited_by": invitedBy})
	})
	return userID, err
}

type tokenOnlyReq struct {
	Token string `json:"token"`
}

// liveStaffInvitation returns the invited user for a live STAFF_INVITATION token without consuming it.
func (s *Service) liveStaffInvitation(ctx context.Context, q queryRower, raw string) (users.Account, error) {
	if !crypto.ValidTokenFormat(raw) {
		return users.Account{}, errTokenInvalid()
	}
	var userID string
	err := q.QueryRow(ctx, `SELECT t.user_id FROM app.auth_tokens t
		WHERE t.token_hash = $1 AND t.purpose = 'STAFF_INVITATION' AND t.consumed_at IS NULL AND t.invalidated_at IS NULL
		  AND t.expires_at > $2
		  AND NOT EXISTS (SELECT 1 FROM app.password_credentials c WHERE c.user_id = t.user_id AND c.superseded_at IS NULL)`,
		crypto.HashToken(raw), s.now()).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return users.Account{}, errTokenInvalid()
	}
	if err != nil {
		return users.Account{}, err
	}
	acct, err := users.ByID(ctx, s.Pool, userID)
	if errors.Is(err, users.ErrNotFound) || err == nil && (acct.Kind != users.KindStaff || acct.Status != users.StatusActive || acct.IsSystem) {
		return users.Account{}, errTokenInvalid()
	}
	return acct, err
}

// StaffInvitationStart validates the invitation link and returns a TOTP enrolment (secret shown once).
// Calling it again replaces the pending enrolment.
func (s *Service) StaffInvitationStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req tokenOnlyReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	acct, err := s.liveStaffInvitation(ctx, s.Pool, req.Token)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "FundZim Staff", AccountName: acct.Email, Period: totpPeriod,
		Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1, SecretSize: 20})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	id := ids.New()
	sealed, err := s.AEAD.Seal([]byte(key.Secret()), aad(id))
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET disabled_at = $2 WHERE user_id = $1 AND confirmed_at IS NULL AND disabled_at IS NULL`,
			acct.ID, s.now()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.mfa_methods (id, user_id, method_type, label, totp_secret_ciphertext, totp_secret_key_id)
			VALUES ($1, $2, 'TOTP', 'Authenticator app', $3, $4)`, id, acct.ID, sealed, s.AEAD.KeyID())
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"email": acct.Email, "display_name": acct.DisplayName,
		"enrollment_id": id, "secret": key.Secret(), "otpauth_uri": key.URL()})
}

type staffInvitationFinishReq struct {
	Token        string `json:"token"`
	EnrollmentID string `json:"enrollment_id"`
	Code         string `json:"code"`
	Password     string `json:"password"`
}

// StaffInvitationFinish consumes the invitation, confirms TOTP, sets the password and verifies the email
// in one transaction, and returns recovery codes once. The staff member then signs in normally
// (password + TOTP); no session is created here.
func (s *Service) StaffInvitationFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req staffInvitationFinishReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	acct, err := s.liveStaffInvitation(ctx, s.Pool, req.Token)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if v := s.policy.Check(req.Password, append(emailContext(acct.Email), acct.DisplayName)...); len(v) > 0 {
		httpx.WriteError(w, r, s.Logger, passwordViolation(v))
		return
	}
	if !ids.Valid(req.EnrollmentID) {
		httpx.WriteError(w, r, s.Logger, errNotFound("MFA_ENROLLMENT_NOT_FOUND", "Start MFA setup again."))
		return
	}
	hash, err := s.Hasher.Hash(ctx, req.Password)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, errs.Wrap(err, errs.Unavailable, errs.CodeServiceUnavailable, "Please try again shortly."))
		return
	}
	var codes []string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT totp_secret_ciphertext FROM app.mfa_methods WHERE id = $1 AND user_id = $2
			AND method_type = 'TOTP' AND confirmed_at IS NULL AND disabled_at IS NULL FOR UPDATE`, req.EnrollmentID, acct.ID).Scan(&sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound("MFA_ENROLLMENT_NOT_FOUND", "Start MFA setup again.")
		}
		if err != nil {
			return err
		}
		ok, err := s.checkTOTP(ctx, tx, req.EnrollmentID, sealed, req.Code)
		if err != nil {
			return err
		}
		if !ok {
			return errMFAInvalid()
		}
		_, userID, _, _, err := s.consumeToken(ctx, tx, req.Token, "STAFF_INVITATION")
		if err != nil {
			return err
		}
		if userID != acct.ID {
			return errTokenInvalid()
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET confirmed_at = $2 WHERE id = $1`, req.EnrollmentID, now); err != nil {
			return err
		}
		if err := s.replacePassword(ctx, tx, acct.ID, hash); err != nil {
			return err
		}
		if !acct.EmailVerified {
			if err := users.MarkEmailVerified(ctx, tx, acct.ID, acct.EmailID, now); err != nil {
				return err
			}
		}
		if codes, err = s.newRecoveryCodes(ctx, tx, acct.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "staff.invitation.accepted", ActorType: "staff",
			ActorID: acct.ID, TargetType: "user", TargetID: acct.ID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, acct.ID, "MFA_ENROLLED", map[string]any{"via": "staff_invitation"})
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"status": "activated", "recovery_codes": codes})
}

// ---- role requests (maker-checker) ----------------------------------------------------------------------

type roleRequestReq struct {
	UserID        string `json:"user_id"`
	RoleCode      string `json:"role_code"`
	Action        string `json:"action"`
	Justification string `json:"justification"`
}

// RoleRequest is a pending or decided role change.
type RoleRequest struct {
	ID             string     `json:"id"`
	Action         string     `json:"action"`
	TargetUserID   string     `json:"target_user_id"`
	RoleCode       string     `json:"role_code"`
	RequestedBy    string     `json:"requested_by"`
	Justification  string     `json:"justification"`
	Status         string     `json:"status"`
	DecidedBy      *string    `json:"decided_by"`
	DecidedAt      *time.Time `json:"decided_at"`
	DecisionReason *string    `json:"decision_reason"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CreateRoleRequest records a maker's request to grant or revoke a staff role.
func (s *Service) CreateRoleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req roleRequestReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var details []errs.Detail
	if !ids.Valid(req.UserID) {
		details = append(details, errs.Detail{Field: "user_id", Code: "INVALID_ID"})
	}
	if req.Action != "GRANT" && req.Action != "REVOKE" {
		details = append(details, errs.Detail{Field: "action", Code: "INVALID_VALUE"})
	}
	just, ok := justification(req.Justification)
	if !ok {
		details = append(details, errs.Detail{Field: "justification", Code: "INVALID_LENGTH"})
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(details...))
		return
	}
	if req.UserID == p.UserID {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "SELF_REQUEST_FORBIDDEN", "You cannot request a role change for yourself."))
		return
	}
	id, err := s.createRoleRequest(ctx, p.UserID, req.UserID, req.RoleCode, req.Action, just)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	rr, err := s.loadRoleRequest(ctx, s.Pool, id)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, rr)
}

func (s *Service) createRoleRequest(ctx context.Context, maker, target, roleCode, action, just string) (string, error) {
	id := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		acct, err := users.ByID(ctx, tx, target)
		if errors.Is(err, users.ErrNotFound) || err == nil && (acct.Kind != users.KindStaff || acct.IsSystem) {
			return errNotFound("STAFF_NOT_FOUND", "No such staff account.")
		}
		if err != nil {
			return err
		}
		var roleID string
		err = tx.QueryRow(ctx, `SELECT id FROM app.roles WHERE code = $1`, roleCode).Scan(&roleID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Validation(errs.Detail{Field: "role_code", Code: "UNKNOWN_ROLE"})
		}
		if err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.role_assignments WHERE user_id = $1 AND role_id = $2 AND revoked_at IS NULL)`,
			target, roleID).Scan(&active); err != nil {
			return err
		}
		if action == "GRANT" && active {
			return errs.New(errs.Conflict, "ROLE_ALREADY_ASSIGNED", "The staff member already has this role.")
		}
		if action == "REVOKE" && !active {
			return errs.New(errs.Conflict, "ROLE_NOT_ASSIGNED", "The staff member does not have this role.")
		}
		now := s.now()
		_, err = tx.Exec(ctx, `INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification,
			expires_at, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			id, action, target, roleID, maker, just, now.Add(RoleRequestTTL), now)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.ConstraintName == "uq_role_assignment_requests_pending" {
				return errs.New(errs.Conflict, "ROLE_REQUEST_PENDING", "An identical request is already pending.")
			}
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "rbac.role_request.created", TargetType: "role_assignment_request",
			TargetID: id, Justification: just, Metadata: map[string]any{"action": action, "role": roleCode, "target_user_id": target}})
	})
	return id, err
}

func (s *Service) loadRoleRequest(ctx context.Context, q queryRower, id string) (RoleRequest, error) {
	var rr RoleRequest
	err := q.QueryRow(ctx, `SELECT q.id, q.action, q.target_user_id, r.code, q.requested_by, q.justification, q.status, q.decided_by,
			q.decided_at, q.decision_reason, q.expires_at, q.created_at
		FROM app.role_assignment_requests q JOIN app.roles r ON r.id = q.role_id WHERE q.id = $1`, id).
		Scan(&rr.ID, &rr.Action, &rr.TargetUserID, &rr.RoleCode, &rr.RequestedBy, &rr.Justification, &rr.Status, &rr.DecidedBy,
			&rr.DecidedAt, &rr.DecisionReason, &rr.ExpiresAt, &rr.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rr, errNotFound("ROLE_REQUEST_NOT_FOUND", "No such role request.")
	}
	return rr, err
}

// ListRoleRequests lists pending role requests (oldest first).
func (s *Service) ListRoleRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.role_assignment_requests WHERE status = 'PENDING' AND expires_at > $1
		ORDER BY created_at LIMIT 200`, s.now())
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var idList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		idList = append(idList, id)
	}
	rows.Close()
	out := []RoleRequest{}
	for _, id := range idList {
		rr, err := s.loadRoleRequest(ctx, s.Pool, id)
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		out = append(out, rr)
	}
	httpx.WriteData(w, r, http.StatusOK, out)
}

type decisionReq struct {
	Reason string `json:"reason"`
}

// ApproveRoleRequest is the checker step. The route policy already required role.assign.approve and a
// fresh step-up; the database rejects self-approval and role conflicts as a second line of defence.
func (s *Service) ApproveRoleRequest(w http.ResponseWriter, r *http.Request) {
	s.decideRoleRequest(w, r, true)
}

// RejectRoleRequest rejects a pending request (any holder of role.assign.approve except the requester).
func (s *Service) RejectRoleRequest(w http.ResponseWriter, r *http.Request) {
	s.decideRoleRequest(w, r, false)
}

func (s *Service) decideRoleRequest(w http.ResponseWriter, r *http.Request, approve bool) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	id := r.PathValue("role_request_id")
	var req decisionReq
	if r.ContentLength != 0 { // the body (an optional reason for approvals) may be omitted
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	}
	if !ids.Valid(id) {
		httpx.WriteError(w, r, s.Logger, errNotFound("ROLE_REQUEST_NOT_FOUND", "No such role request."))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if !approve && len(reason) < 3 || len(reason) > 1000 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"}))
		return
	}
	err := s.decide(ctx, id, p.UserID, p.StepUpAt, approve, reason)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	rr, err := s.loadRoleRequest(ctx, s.Pool, id)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, rr)
}

// errExpired carries the "mark EXPIRED and commit" outcome out of the transaction.
var errRoleRequestExpired = errs.New(errs.Conflict, "ROLE_REQUEST_EXPIRED", "This request has expired. Create a new one.")

func (s *Service) decide(ctx context.Context, id, checker string, stepUpAt time.Time, approve bool, reason string) error {
	expired := false
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var action, target, roleID, maker, status string
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT action, target_user_id, role_id, requested_by, status, expires_at
			FROM app.role_assignment_requests WHERE id = $1 FOR UPDATE`, id).Scan(&action, &target, &roleID, &maker, &status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound("ROLE_REQUEST_NOT_FOUND", "No such role request.")
		}
		if err != nil {
			return err
		}
		if status != "PENDING" {
			return errs.New(errs.Conflict, "ROLE_REQUEST_DECIDED", "This request has already been decided.")
		}
		now := s.now()
		if !expires.After(now) {
			if _, err := tx.Exec(ctx, `UPDATE app.role_assignment_requests SET status = 'EXPIRED' WHERE id = $1`, id); err != nil {
				return err
			}
			expired = true
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "rbac.role_request.expired", TargetType: "role_assignment_request", TargetID: id})
		}
		if checker == maker || checker == target {
			return errs.New(errs.Forbidden, "SELF_APPROVAL_FORBIDDEN", "You cannot decide a request you made or that concerns you.")
		}
		newStatus := "REJECTED"
		var stepUp any
		if approve {
			newStatus, stepUp = "APPROVED", stepUpAt
		}
		if _, err := tx.Exec(ctx, `UPDATE app.role_assignment_requests SET status = $2, decided_by = $3, decided_at = $4,
			decision_reason = $5, checker_step_up_at = $6 WHERE id = $1`, id, newStatus, checker, now, nullable(reason), stepUp); err != nil {
			return err
		}
		if approve {
			if err := s.applyRoleChange(ctx, tx, id, action, target, roleID, maker, checker, reason); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "rbac.role_request." + strings.ToLower(newStatus),
			TargetType: "role_assignment_request", TargetID: id, Reason: reason, Metadata: map[string]any{"action": action, "target_user_id": target}})
	})
	if err == nil && expired {
		return errRoleRequestExpired
	}
	return err
}

func (s *Service) applyRoleChange(ctx context.Context, tx pgx.Tx, requestID, action, target, roleID, maker, checker, reason string) error {
	now := s.now()
	if action == "GRANT" {
		if _, err := tx.Exec(ctx, `SAVEPOINT grant_role`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by, valid_from)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), target, roleID, requestID, maker, checker, now)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT grant_role`)
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23000" && strings.Contains(pg.Message, "separation of duties") {
				return errs.New(errs.Conflict, "ROLE_CONFLICT", "This role conflicts with a role the staff member already holds.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT grant_role`); err != nil {
			return err
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE app.role_assignments SET revoked_at = $3, revoked_by = $4, revoke_request_id = $5, revoke_reason = $6
			WHERE user_id = $1 AND role_id = $2 AND revoked_at IS NULL`, target, roleID, now, checker, requestID, nullable(reason))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.Conflict, "ROLE_NOT_ASSIGNED", "The staff member no longer has this role.")
		}
	}
	// privileges changed: the target signs in again so the new role set and a fresh MFA apply
	_, err := s.revokeSessions(ctx, tx, target, "", RevokePrivilege)
	return err
}

// ---- account suspension -----------------------------------------------------------------------------------

type statusChangeReq struct {
	Reason string `json:"reason"`
}

// SuspendUser suspends a personal account (account.suspend) and revokes all of its sessions.
func (s *Service) SuspendUser(w http.ResponseWriter, r *http.Request) {
	s.changeStatus(w, r, users.KindUser, true)
}

// ReactivateUser reactivates a suspended personal account (account.reactivate).
func (s *Service) ReactivateUser(w http.ResponseWriter, r *http.Request) {
	s.changeStatus(w, r, users.KindUser, false)
}

// SuspendStaff suspends a staff account (staff.suspend) and revokes all of its sessions. Staff
// reactivation is deferred to the admin console stage (it should be a maker-checker decision).
func (s *Service) SuspendStaff(w http.ResponseWriter, r *http.Request) {
	s.changeStatus(w, r, users.KindStaff, true)
}

// changeStatus suspends or reactivates an account of the given kind. The route policy has already
// required the matching permission and a fresh step-up. Nobody can change their own status, and the
// system actor is never visible. A target of the other kind is reported as not found.
func (s *Service) changeStatus(w http.ResponseWriter, r *http.Request, kind string, suspend bool) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	target := r.PathValue("user_id")
	var req statusChangeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	reason, ok := justification(req.Reason)
	if !ok {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"}))
		return
	}
	if !ids.Valid(target) {
		httpx.WriteError(w, r, s.Logger, errNotFound("USER_NOT_FOUND", "No such account."))
		return
	}
	if target == p.UserID {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "SELF_ACTION_FORBIDDEN", "You cannot change your own account status."))
		return
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		acct, err := users.ByIDForUpdate(ctx, tx, target)
		if errors.Is(err, users.ErrNotFound) || err == nil && (acct.IsSystem || acct.Kind != kind) {
			return errNotFound("USER_NOT_FOUND", "No such account.")
		}
		if err != nil {
			return err
		}
		want, from := users.StatusSuspended, users.StatusActive
		if !suspend {
			want, from = users.StatusActive, users.StatusSuspended
		}
		if acct.Status != from {
			return errs.New(errs.Conflict, "INVALID_STATUS", "The account is not in a state that allows this change.")
		}
		if err := users.SetStatus(ctx, tx, target, want, s.now()); err != nil {
			return err
		}
		if suspend {
			if _, err := s.revokeSessions(ctx, tx, target, "", RevokeSuspended); err != nil {
				return err
			}
		}
		action := "account.suspended"
		if !suspend {
			action = "account.reactivated"
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: action, TargetType: "user", TargetID: target,
			Reason: reason, Metadata: map[string]any{"account_kind": acct.Kind}}); err != nil {
			return err
		}
		if suspend {
			return s.emit(ctx, tx, EvAccountSuspended, target, map[string]any{"user_id": target})
		}
		return s.emit(ctx, tx, EvAccountReactivated, target, map[string]any{"user_id": target})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminUser is the staff view of an account: contact data masked, no credentials.
type AdminUser struct {
	ID            string    `json:"id"`
	AccountKind   string    `json:"account_kind"`
	Status        string    `json:"status"`
	EmailMasked   string    `json:"email_masked"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name"`
	PhoneMasked   *string   `json:"phone_masked"`
	MFAEnabled    bool      `json:"mfa_enabled"`
	Roles         []string  `json:"roles,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// MaskEmail keeps the first character of the local part and the domain (j***@example.org).
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 1 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}

// GetAdminUser returns the masked staff view of an account (user.view). Each view is audited.
func (s *Service) GetAdminUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("user_id")
	if !ids.Valid(id) {
		httpx.WriteError(w, r, s.Logger, errNotFound("USER_NOT_FOUND", "No such account."))
		return
	}
	acct, err := users.ByID(ctx, s.Pool, id)
	if errors.Is(err, users.ErrNotFound) || err == nil && acct.IsSystem {
		httpx.WriteError(w, r, s.Logger, errNotFound("USER_NOT_FOUND", "No such account."))
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	me, err := s.loadMe(ctx, id)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "admin.user.viewed", TargetType: "user", TargetID: id})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, http.StatusOK, AdminUser{ID: acct.ID, AccountKind: acct.Kind, Status: acct.Status, EmailMasked: MaskEmail(acct.Email),
		EmailVerified: acct.EmailVerified, DisplayName: acct.DisplayName, PhoneMasked: me.PhoneMasked, MFAEnabled: me.MFAEnabled,
		Roles: me.Roles, CreatedAt: acct.CreatedAt})
}

// ---- super-admin bootstrap ceremony ---------------------------------------------------------------------

// BootstrapAdmin is one of the two initial super administrators.
type BootstrapAdmin struct {
	Email       string
	DisplayName string
}

// ErrAlreadyBootstrapped means an active SUPER_ADMIN already exists; the ceremony never runs twice.
var ErrAlreadyBootstrapped = errors.New("bootstrap refused: an active SUPER_ADMIN assignment already exists")

// BootstrapSuperAdmins is the controlled, audited ceremony that creates the first two SUPER_ADMINs (run by
// an operator with `fundzimctl bootstrap-admins`). There is no hard-coded administrator and no public path:
//   - it refuses when any active SUPER_ADMIN assignment exists (serialised by an advisory lock);
//   - it invites two distinct staff accounts (they set a password and enrol TOTP before they can sign in);
//   - each grant goes through a role_assignment_request with maker = the seeded system actor and checker =
//     the OTHER new admin, so no account approves its own role and both grants stay in the maker-checker
//     tables. The checker's step-up time is the ceremony time: the operator-run ceremony is the control,
//     and that is recorded explicitly in the audit justification.
func (s *Service) BootstrapSuperAdmins(ctx context.Context, a, b BootstrapAdmin, ceremonyJustification string) ([2]string, error) {
	var out [2]string
	just, ok := justification(ceremonyJustification)
	if !ok {
		return out, errors.New("bootstrap: justification must be 10-1000 characters")
	}
	ea, errA := users.NormalizeEmail(a.Email)
	eb, errB := users.NormalizeEmail(b.Email)
	if errA != nil || errB != nil || ea == eb {
		return out, errors.New("bootstrap: two distinct valid email addresses are required")
	}
	na, okA := users.NormalizeDisplayName(a.DisplayName)
	nb, okB := users.NormalizeDisplayName(b.DisplayName)
	if !okA || !okB {
		return out, errors.New("bootstrap: display names must be 1-100 characters")
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('fundzim.bootstrap_super_admins'))`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.role_assignments ra JOIN app.roles r ON r.id = ra.role_id
			WHERE r.code = 'SUPER_ADMIN' AND ra.revoked_at IS NULL)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrAlreadyBootstrapped
		}
		var roleID string
		if err := tx.QueryRow(ctx, `SELECT id FROM app.roles WHERE code = 'SUPER_ADMIN'`).Scan(&roleID); err != nil {
			return err
		}
		admins := []struct{ email, name string }{{ea, na}, {eb, nb}}
		for i, ad := range admins {
			uid, emailID, err := users.Create(ctx, tx, users.NewAccount{Kind: users.KindStaff, Email: ad.email, DisplayName: ad.name})
			if errors.Is(err, users.ErrEmailTaken) {
				return errors.New("bootstrap: an account already uses " + MaskEmail(ad.email))
			}
			if err != nil {
				return err
			}
			out[i] = uid
			if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "staff.invited", ActorType: "system", ActorID: SystemActorID,
				TargetType: "user", TargetID: uid, Justification: just, Metadata: map[string]any{"ceremony": "bootstrap_super_admins"}}); err != nil {
				return err
			}
			if err := s.emit(ctx, tx, EvStaffInvited, uid, map[string]any{"user_id": uid, "email_id": emailID, "invited_by": SystemActorID}); err != nil {
				return err
			}
		}
		now := s.now()
		for i := range out {
			target, checker := out[i], out[1-i]
			reqID := ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification,
				expires_at, created_at, updated_at) VALUES ($1, 'GRANT', $2, $3, $4, $5, $6, $7, $7)`,
				reqID, target, roleID, SystemActorID, just, now.Add(RoleRequestTTL), now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = $2, decided_at = $3,
				decision_reason = 'bootstrap ceremony', checker_step_up_at = $3 WHERE id = $1`, reqID, checker, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by, valid_from)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), target, roleID, reqID, SystemActorID, checker, now); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "rbac.bootstrap.super_admin_granted", ActorType: "system",
				ActorID: SystemActorID, TargetType: "user", TargetID: target, Justification: just,
				Metadata: map[string]any{"request_id": reqID, "checker_user_id": checker, "checker_step_up": "ceremony_time"}}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
