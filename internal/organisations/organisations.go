// Package organisations owns organisations, organisation roles, memberships and invitations
// (design-baseline §5.10). Stage 4 builds the membership foundation only: verification (KYB), logos,
// campaigns and payouts arrive in later stages.
//
// Isolation rule: every query that reads or writes organisation data is scoped by the organisation ID AND
// the caller's active membership. A non-member gets 404 (the organisation's existence is not revealed), a
// member without the needed organisation permission gets 403. Organisation roles are separate from staff
// roles: no organisation role grants any platform (staff) permission and vice versa.
package organisations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Organisation permissions (app.organisation_roles.permissions).
const (
	PermView         = "org.view"
	PermSettings     = "org.settings.manage"
	PermMemberManage = "org.member.manage"
	RoleAdmin        = "ORG_ADMIN"
	RoleMember       = "ORG_MEMBER"
	EvMemberInvited  = "organisations.member_invited"
	InvitationTTL    = 7 * 24 * time.Hour
	maxOrgsPerUser   = 20
	maxPendingPerOrg = 50
)

// OrgTypes are the accepted organisation types (ck_organisations_org_type).
var OrgTypes = map[string]bool{"COMPANY": true, "TRUST": true, "PVO": true, "FAITH_BASED": true, "SCHOOL": true,
	"HEALTH_INSTITUTION": true, "COMMUNITY_BASED": true, "SPORTS_CLUB": true, "OTHER": true}

// EventWriter writes an outbox event inside tx.
type EventWriter func(ctx context.Context, tx pgx.Tx, eventType, aggregateType, aggregateID string, payload map[string]any) error

// Mailer sends one email (outbox consumers).
type Mailer interface {
	SendEmail(ctx context.Context, to, subject, text string) error
}

// Service is the organisations module.
type Service struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Events    EventWriter
	Logger    *slog.Logger
	PublicURL string
}

func (s *Service) now() time.Time { return s.Clock.Now() }

func (s *Service) tx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

func errOrgNotFound() error {
	return errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
}

func errForbidden() error {
	return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action in this organisation.")
}

// Organisation is the member's view of an organisation.
type Organisation struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Slug        string    `json:"slug"`
	OrgType     string    `json:"org_type"`
	Status      string    `json:"status"`
	MyRole      string    `json:"my_role"`
	Permissions []string  `json:"my_permissions"`
	CreatedAt   time.Time `json:"created_at"`
}

// membership is the caller's active membership of an organisation.
type membership struct {
	MemberID, Role string
	Perms          map[string]bool
}

// member loads the caller's active membership in an ACTIVE organisation; errOrgNotFound when there is none.
func (s *Service) member(ctx context.Context, q users.Querier, orgID, userID string, lock bool) (membership, error) {
	var m membership
	if !ids.Valid(orgID) {
		return m, errOrgNotFound()
	}
	sql := `SELECT m.id, r.code, r.permissions FROM app.organisation_members m
		JOIN app.organisations o ON o.id = m.organisation_id
		JOIN app.organisation_roles r ON r.id = m.organisation_role_id
		WHERE m.organisation_id = $1 AND m.user_id = $2 AND m.status = 'ACTIVE' AND o.status = 'ACTIVE'`
	if lock {
		sql += ` FOR UPDATE OF m`
	}
	var perms []string
	err := q.QueryRow(ctx, sql, orgID, userID).Scan(&m.MemberID, &m.Role, &perms)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errOrgNotFound()
	}
	if err != nil {
		return m, err
	}
	m.Perms = map[string]bool{}
	for _, p := range perms {
		m.Perms[p] = true
	}
	return m, nil
}

func (s *Service) require(ctx context.Context, q users.Querier, orgID, userID, perm string) (membership, error) {
	m, err := s.member(ctx, q, orgID, userID, false)
	if err != nil {
		return m, err
	}
	if !m.Perms[perm] {
		return m, errForbidden()
	}
	return m, nil
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "org"
	}
	return s
}

func randSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type createReq struct {
	DisplayName string `json:"display_name"`
	OrgType     string `json:"org_type"`
}

// Create creates an organisation with the caller as its first ORG_ADMIN. A verified email is required.
func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if !p.EmailVerified {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "EMAIL_NOT_VERIFIED", "Verify your email address before creating an organisation."))
		return
	}
	var req createReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	name := strings.Join(strings.Fields(req.DisplayName), " ")
	var details []errs.Detail
	if n := len([]rune(name)); n < 2 || n > 150 {
		details = append(details, errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"})
	}
	if !OrgTypes[req.OrgType] {
		details = append(details, errs.Detail{Field: "org_type", Code: "INVALID_VALUE"})
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(details...))
		return
	}
	id := ids.New()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.organisations WHERE created_by_user_id = $1`, p.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= maxOrgsPerUser {
			return errs.New(errs.Unprocessable, "ORGANISATION_LIMIT_REACHED", "You have reached the number of organisations you can create.")
		}
		slug := slugify(name)
		for attempt := 0; ; attempt++ {
			if _, err := tx.Exec(ctx, `SAVEPOINT org_slug`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO app.organisations (id, display_name, slug, org_type, created_by_user_id)
				VALUES ($1, $2, $3, $4, $5)`, id, name, slug, req.OrgType, p.UserID)
			if err == nil {
				if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT org_slug`); err != nil {
					return err
				}
				break
			}
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT org_slug`)
			if !db.IsUniqueViolation(err, "uq_organisations_slug") || attempt >= 4 {
				return err
			}
			slug = slugify(name) + "-" + randSuffix()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
			VALUES ($1, $2, $3, md5('org_role:ORG_ADMIN')::uuid)`, ids.New(), id, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.created", TargetType: "organisation", TargetID: id,
			Metadata: map[string]any{"org_type": req.OrgType}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	o, err := s.load(ctx, id, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, o)
}

const orgSelect = `SELECT o.id, o.display_name, o.slug, o.org_type, o.status, r.code, r.permissions, o.created_at
	FROM app.organisations o
	JOIN app.organisation_members m ON m.organisation_id = o.id AND m.user_id = $1 AND m.status = 'ACTIVE'
	JOIN app.organisation_roles r ON r.id = m.organisation_role_id`

func scanOrg(row pgx.Row) (Organisation, error) {
	var o Organisation
	err := row.Scan(&o.ID, &o.DisplayName, &o.Slug, &o.OrgType, &o.Status, &o.MyRole, &o.Permissions, &o.CreatedAt)
	return o, err
}

func (s *Service) load(ctx context.Context, orgID, userID string) (Organisation, error) {
	if !ids.Valid(orgID) {
		return Organisation{}, errOrgNotFound()
	}
	o, err := scanOrg(s.Pool.QueryRow(ctx, orgSelect+` WHERE o.id = $2`, userID, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, errOrgNotFound()
	}
	return o, err
}

// ListMine lists the organisations the caller belongs to.
func (s *Service) ListMine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	rows, err := s.Pool.Query(ctx, orgSelect+` ORDER BY o.created_at`, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	defer rows.Close()
	out := []Organisation{}
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		out = append(out, o)
	}
	httpx.WriteData(w, r, http.StatusOK, out)
}

// Get returns one organisation to a member (404 for everyone else).
func (s *Service) Get(w http.ResponseWriter, r *http.Request) {
	o, err := s.load(r.Context(), r.PathValue("org_id"), authz.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, o)
}

// Member is one membership as other members see it.
type Member struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	JoinedAt    time.Time `json:"joined_at"`
}

// Members lists current members, ACTIVE and SUSPENDED (org.view); removed members are not listed.
func (s *Service) Members(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID := r.PathValue("org_id")
	if _, err := s.require(ctx, s.Pool, orgID, p.UserID, PermView); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.user_id, r.code, m.status, m.joined_at FROM app.organisation_members m
		JOIN app.organisation_roles r ON r.id = m.organisation_role_id
		WHERE m.organisation_id = $1 AND m.status <> 'REMOVED' ORDER BY m.joined_at`, orgID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	out := []Member{}
	var uids []string
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.Role, &m.Status, &m.JoinedAt); err != nil {
			rows.Close()
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		out = append(out, m)
		uids = append(uids, m.UserID)
	}
	rows.Close()
	names, err := users.DisplayNames(ctx, s.Pool, uids)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	for i := range out {
		out[i].DisplayName = names[out[i].UserID]
	}
	httpx.WriteData(w, r, http.StatusOK, out)
}

type inviteReq struct {
	Email    string `json:"email"`
	RoleCode string `json:"role_code"`
}

// Invitation is a pending invitation.
type Invitation struct {
	ID               string    `json:"id"`
	OrganisationID   string    `json:"organisation_id"`
	OrganisationName string    `json:"organisation_name,omitempty"`
	Email            string    `json:"email,omitempty"`
	Role             string    `json:"role"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

func roleID(code string) (string, bool) {
	switch code {
	case RoleAdmin, RoleMember:
		return code, true
	}
	return "", false
}

// Invite invites an email address to the organisation (org.member.manage). The invitee accepts while
// signed in with that (verified) address; no token is ever sent, so a forwarded email grants nothing.
func (s *Service) Invite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID := r.PathValue("org_id")
	var req inviteReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, err := users.NormalizeEmail(req.Email)
	var details []errs.Detail
	if err != nil {
		details = append(details, errs.Detail{Field: "email", Code: "INVALID_EMAIL"})
	}
	role, ok := roleID(req.RoleCode)
	if !ok {
		details = append(details, errs.Detail{Field: "role_code", Code: "INVALID_VALUE"})
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(details...))
		return
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.require(ctx, tx, orgID, p.UserID, PermMemberManage); err != nil {
			return err
		}
		var invitee string // the account that signs in with this address, if any (users module interface)
		if a, err := users.ByLoginEmail(ctx, tx, email); err == nil {
			invitee = a.ID
		} else if !errors.Is(err, users.ErrNotFound) {
			return err
		}
		var pending int
		var already bool
		if err := tx.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM app.organisation_invitations WHERE organisation_id = $1 AND status = 'PENDING' AND expires_at > $3),
				EXISTS (SELECT 1 FROM app.organisation_members m
				        WHERE m.organisation_id = $1 AND m.status <> 'REMOVED' AND m.user_id::text = $2)`,
			orgID, invitee, s.now()).Scan(&pending, &already); err != nil {
			return err
		}
		if already {
			return errs.New(errs.Conflict, "ALREADY_MEMBER", "That person is already a member.")
		}
		if pending >= maxPendingPerOrg {
			return errs.New(errs.Unprocessable, "INVITATION_LIMIT_REACHED", "Too many pending invitations. Revoke some first.")
		}
		// expire stale pending invitations for this address so the partial unique index admits a new one
		if _, err := tx.Exec(ctx, `UPDATE app.organisation_invitations SET status = 'EXPIRED'
			WHERE organisation_id = $1 AND invited_email_normalized = $2 AND status = 'PENDING' AND expires_at <= $3`, orgID, email, s.now()); err != nil {
			return err
		}
		tok := make([]byte, 32)
		_, _ = rand.Read(tok) // random, never sent: the column is required by the schema but acceptance is by signed-in identity
		now := s.now()
		if _, err := tx.Exec(ctx, `SAVEPOINT org_invite`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.organisation_invitations (id, organisation_id, organisation_role_id, invited_email_normalized,
			token_hash, invited_by_user_id, expires_at, created_at, updated_at)
			VALUES ($1, $2, md5('org_role:' || $3)::uuid, $4, $5, $6, $7, $8, $8)`, id, orgID, role, email, tok, p.UserID, now.Add(InvitationTTL), now)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT org_invite`)
			if db.IsUniqueViolation(err, "uq_organisation_invitations_pending_email") {
				return errs.New(errs.Conflict, "INVITATION_PENDING", "That address already has a pending invitation.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT org_invite`); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.member.invited", TargetType: "organisation_invitation",
			TargetID: id, Metadata: map[string]any{"organisation_id": orgID, "role": role}}); err != nil {
			return err
		}
		return s.Events(ctx, tx, EvMemberInvited, "organisation", orgID, map[string]any{"invitation_id": id, "organisation_id": orgID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusCreated, map[string]string{"id": id, "status": "PENDING"})
}

// Invitations lists the organisation's pending invitations (org.member.manage).
func (s *Service) Invitations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID := r.PathValue("org_id")
	if _, err := s.require(ctx, s.Pool, orgID, p.UserID, PermMemberManage); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.writeInvitations(w, r, `SELECT i.id, i.organisation_id, '', i.invited_email_normalized, r.code, i.status, i.expires_at, i.created_at
		FROM app.organisation_invitations i JOIN app.organisation_roles r ON r.id = i.organisation_role_id
		WHERE i.organisation_id = $1 AND i.status = 'PENDING' AND i.expires_at > $2 ORDER BY i.created_at`, orgID, s.now())
}

func (s *Service) writeInvitations(w http.ResponseWriter, r *http.Request, sql string, args ...any) {
	rows, err := s.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.OrganisationID, &i.OrganisationName, &i.Email, &i.Role, &i.Status, &i.ExpiresAt, &i.CreatedAt); err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		out = append(out, i)
	}
	httpx.WriteData(w, r, http.StatusOK, out)
}

// RevokeInvitation revokes a pending invitation of the organisation (org.member.manage).
func (s *Service) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID, invID := r.PathValue("org_id"), r.PathValue("invitation_id")
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.require(ctx, tx, orgID, p.UserID, PermMemberManage); err != nil {
			return err
		}
		if !ids.Valid(invID) {
			return errs.New(errs.NotFound, "INVITATION_NOT_FOUND", "No such invitation.")
		}
		tag, err := tx.Exec(ctx, `UPDATE app.organisation_invitations SET status = 'REVOKED', revoked_at = $3, revoked_by_user_id = $4
			WHERE id = $1 AND organisation_id = $2 AND status = 'PENDING'`, invID, orgID, s.now(), p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "INVITATION_NOT_FOUND", "No such invitation.")
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.invitation.revoked", TargetType: "organisation_invitation",
			TargetID: invID, Metadata: map[string]any{"organisation_id": orgID}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// verifiedLoginEmail returns the caller's verified sign-in address, or "" when it is not verified.
func (s *Service) verifiedLoginEmail(ctx context.Context, userID string) (string, error) {
	a, err := users.ByID(ctx, s.Pool, userID)
	if err != nil {
		return "", err
	}
	if !a.EmailVerified {
		return "", nil
	}
	return a.Email, nil
}

// MyInvitations lists pending invitations addressed to the caller's verified sign-in address.
func (s *Service) MyInvitations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email, err := s.verifiedLoginEmail(ctx, authz.PrincipalFrom(ctx).UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if email == "" {
		httpx.WriteData(w, r, http.StatusOK, []Invitation{})
		return
	}
	s.writeInvitations(w, r, `SELECT i.id, i.organisation_id, o.display_name, '', r.code, i.status, i.expires_at, i.created_at
		FROM app.organisation_invitations i JOIN app.organisation_roles r ON r.id = i.organisation_role_id
		JOIN app.organisations o ON o.id = i.organisation_id
		WHERE i.invited_email_normalized = $1 AND i.status = 'PENDING' AND i.expires_at > $2 AND o.status = 'ACTIVE'
		ORDER BY i.created_at`, email, s.now())
}

// AcceptInvitation joins the organisation. Only the signed-in owner of the invited (verified) address can
// accept; anyone else gets 404.
func (s *Service) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	s.answerInvitation(w, r, true)
}

// DeclineInvitation declines an invitation addressed to the caller.
func (s *Service) DeclineInvitation(w http.ResponseWriter, r *http.Request) {
	s.answerInvitation(w, r, false)
}

func (s *Service) answerInvitation(w http.ResponseWriter, r *http.Request, accept bool) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	invID := r.PathValue("invitation_id")
	notFound := errs.New(errs.NotFound, "INVITATION_NOT_FOUND", "No such invitation.")
	if !ids.Valid(invID) {
		httpx.WriteError(w, r, s.Logger, notFound)
		return
	}
	email, err := s.verifiedLoginEmail(ctx, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if email == "" {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "EMAIL_NOT_VERIFIED", "Verify your email address first."))
		return
	}
	var orgID string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var roleID string
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT i.organisation_id, i.organisation_role_id, i.expires_at FROM app.organisation_invitations i
			JOIN app.organisations o ON o.id = i.organisation_id
			WHERE i.id = $1 AND i.invited_email_normalized = $2 AND i.status = 'PENDING' AND o.status = 'ACTIVE' FOR UPDATE OF i`,
			invID, email).Scan(&orgID, &roleID, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		now := s.now()
		if !expires.After(now) {
			return notFound
		}
		if !accept {
			if _, err := tx.Exec(ctx, `UPDATE app.organisation_invitations SET status = 'DECLINED' WHERE id = $1`, invID); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.invitation.declined",
				TargetType: "organisation_invitation", TargetID: invID, Metadata: map[string]any{"organisation_id": orgID}})
		}
		if _, err := tx.Exec(ctx, `UPDATE app.organisation_invitations SET status = 'ACCEPTED', accepted_by_user_id = $2, accepted_at = $3
			WHERE id = $1`, invID, p.UserID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT org_join`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id, invitation_id)
			VALUES ($1, $2, $3, $4, $5)`, ids.New(), orgID, p.UserID, roleID, invID)
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT org_join`)
			if db.IsUniqueViolation(err, "uq_organisation_members_active") {
				return errs.New(errs.Conflict, "ALREADY_MEMBER", "You are already a member of this organisation.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT org_join`); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.member.joined",
			TargetType: "organisation", TargetID: orgID, Metadata: map[string]any{"invitation_id": invID}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if !accept {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	o, err := s.load(ctx, orgID, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, o)
}

type changeRoleReq struct {
	RoleCode string `json:"role_code"`
}

// lastAdmin maps the deferred at-least-one-ORG_ADMIN trigger to a stable error.
func lastAdmin(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23000" && strings.Contains(pg.Message, "at least one active ORG_ADMIN") {
		return errs.New(errs.Conflict, "LAST_ORG_ADMIN", "An organisation needs at least one administrator.")
	}
	return err
}

// ChangeMemberRole changes a member's organisation role (org.member.manage). The database keeps at least
// one ORG_ADMIN (deferred trigger, checked immediately here).
func (s *Service) ChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID, memberID := r.PathValue("org_id"), r.PathValue("member_id")
	var req changeRoleReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	role, ok := roleID(req.RoleCode)
	if !ok {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "role_code", Code: "INVALID_VALUE"}))
		return
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.require(ctx, tx, orgID, p.UserID, PermMemberManage); err != nil {
			return err
		}
		if !ids.Valid(memberID) {
			return errs.New(errs.NotFound, "MEMBER_NOT_FOUND", "No such member.")
		}
		tag, err := tx.Exec(ctx, `UPDATE app.organisation_members SET organisation_role_id = md5('org_role:' || $3)::uuid
			WHERE id = $1 AND organisation_id = $2 AND status = 'ACTIVE'`, memberID, orgID, role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "MEMBER_NOT_FOUND", "No such member.")
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return lastAdmin(err)
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.member.role_changed", TargetType: "organisation_member",
			TargetID: memberID, Metadata: map[string]any{"organisation_id": orgID, "role": role}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, lastAdmin(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveMember removes a member (org.member.manage), or lets a member leave (their own membership).
func (s *Service) RemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	orgID, memberID := r.PathValue("org_id"), r.PathValue("member_id")
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		me, err := s.member(ctx, tx, orgID, p.UserID, false)
		if err != nil {
			return err
		}
		if memberID != me.MemberID && !me.Perms[PermMemberManage] {
			return errForbidden()
		}
		if !ids.Valid(memberID) {
			return errs.New(errs.NotFound, "MEMBER_NOT_FOUND", "No such member.")
		}
		tag, err := tx.Exec(ctx, `UPDATE app.organisation_members SET status = 'REMOVED', removed_at = $3, removed_by_user_id = $4
			WHERE id = $1 AND organisation_id = $2 AND status <> 'REMOVED'`, memberID, orgID, s.now(), p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "MEMBER_NOT_FOUND", "No such member.")
		}
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return lastAdmin(err)
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "organisation.member.removed", TargetType: "organisation_member",
			TargetID: memberID, Metadata: map[string]any{"organisation_id": orgID, "self": memberID == me.MemberID}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, lastAdmin(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InvitationEmailConsumer returns the outbox handler that emails an invitee. The email carries no token:
// the invitee signs in (or registers) with the invited address and accepts from their dashboard.
func (s *Service) InvitationEmailConsumer(mail Mailer) func(ctx context.Context, payload json.RawMessage) error {
	return func(ctx context.Context, payload json.RawMessage) error {
		var p struct {
			InvitationID string `json:"invitation_id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil || !ids.Valid(p.InvitationID) {
			return fmt.Errorf("organisations consumer: malformed payload")
		}
		var email, orgName, status string
		err := s.Pool.QueryRow(ctx, `SELECT i.invited_email_normalized, o.display_name, i.status FROM app.organisation_invitations i
			JOIN app.organisations o ON o.id = i.organisation_id WHERE i.id = $1`, p.InvitationID).Scan(&email, &orgName, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "PENDING" {
			return nil
		}
		return mail.SendEmail(ctx, email, "You have been invited to join an organisation on FundZim",
			"You have been invited to join \""+orgName+"\" on FundZim.\n\nSign in (or create an account) with this email address at "+
				s.PublicURL+"/dashboard to accept or decline. The invitation expires in 7 days.\n\nIf you were not expecting this, you can ignore this email.")
	}
}
