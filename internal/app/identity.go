package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/auth/passwords"
	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// Local key IDs. KMS-managed keys (and their IDs) arrive in Stage 18; production refuses to start until then.
const (
	fieldKeyID      = "local-field-1"
	blindIndexKeyID = "local-blind-1"
)

// IdentityDeps are what the identity modules need from the process (API or worker).
type IdentityDeps struct {
	Pool    *pgxpool.Pool
	Config  config.Config
	Clock   clock.Clock
	Logger  *slog.Logger
	Limiter ratelimit.Limiter       // nil: no throttling (worker only; the API always has one)
	SMS     notifications.SMSSender // nil in the worker
}

// NewAuthService builds the auth service and the organisations service.
func NewAuthService(ctx context.Context, d IdentityDeps) (*auth.Service, *organisations.Service, error) {
	cfg := d.Config
	aead, err := crypto.NewAEAD(fieldKeyID, cfg.Security.FieldEncryptionKey.Reveal())
	if err != nil {
		return nil, nil, fmt.Errorf("field encryption key: %w", err)
	}
	keyed, err := crypto.NewKeyed(blindIndexKeyID, cfg.Security.BlindIndexKey.Reveal())
	if err != nil {
		return nil, nil, fmt.Errorf("blind index key: %w", err)
	}
	hasher := passwords.NewHasher(passwords.Params{MemoryKiB: cfg.Auth.HashMemoryKiB, Iterations: cfg.Auth.HashIterations,
		Parallelism: cfg.Auth.HashParallelism}, cfg.Auth.HashMaxConcurrent)
	events := func(ctx context.Context, tx pgx.Tx, e auth.OutboxEvent) error {
		_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "user", AggregateID: e.AggregateID, EventType: e.EventType,
			Payload: e.Payload, CorrelationID: httpx.RequestID(ctx), OccurredAt: d.Clock.Now()})
		return err
	}
	deps := auth.Deps{Pool: d.Pool, Cfg: cfg.Auth, Clock: d.Clock, Hasher: hasher, AEAD: aead, Keyed: keyed, Events: events, Logger: d.Logger}
	if d.Limiter != nil {
		deps.Throttle = newThrottler(d.Limiter)
	}
	if d.SMS != nil {
		deps.SMS = smsAdapter{d.SMS}
	}
	svc, err := auth.New(ctx, deps)
	if err != nil {
		return nil, nil, err
	}
	orgs := &organisations.Service{Pool: d.Pool, Clock: d.Clock, Logger: d.Logger, PublicURL: cfg.Auth.PublicURL,
		Events: func(ctx context.Context, tx pgx.Tx, typ, aggType, aggID string, payload map[string]any) error {
			_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: aggType, AggregateID: aggID, EventType: typ, Payload: payload,
				CorrelationID: httpx.RequestID(ctx), OccurredAt: d.Clock.Now()})
			return err
		}}
	return svc, orgs, nil
}

// throttler adapts the distributed limiter to auth.Throttler by policy name.
type throttler struct {
	l        ratelimit.Limiter
	policies map[string]ratelimit.Policy
}

func newThrottler(l ratelimit.Limiter) *throttler {
	t := &throttler{l: l, policies: map[string]ratelimit.Policy{}}
	for _, p := range ratelimit.DefaultPolicies().All() {
		t.policies[p.Name] = p
	}
	return t
}

func (t *throttler) Allow(ctx context.Context, policy, key string) error {
	p, ok := t.policies[policy]
	if !ok {
		// a programming error: never fail open
		return errs.New(errs.Internal, errs.CodeInternal, "An unexpected error occurred.")
	}
	d, err := t.l.Allow(ctx, p, key)
	if err != nil {
		return errs.Wrap(err, errs.Internal, errs.CodeInternal, "An unexpected error occurred.")
	}
	if !d.Allowed {
		return errs.New(errs.RateLimited, errs.CodeRateLimited, fmt.Sprintf("Too many attempts. Try again in %d seconds.",
			ratelimit.RetryAfterSeconds(d.RetryAfter)))
	}
	return nil
}

type smsAdapter struct{ s notifications.SMSSender }

func (a smsAdapter) SendSMS(ctx context.Context, to, body string) error {
	return a.s.Send(ctx, notifications.SMS{To: to, Body: body})
}

type mailAdapter struct{ s notifications.EmailSender }

func (a mailAdapter) SendEmail(ctx context.Context, to, subject, text string) error {
	return a.s.Send(ctx, notifications.Email{To: to, Subject: subject, Text: text})
}

// registerIdentityRoutes registers the Stage 4 identity, organisation and admin routes. Every route
// declares its policy; ownership and organisation membership are checked in the handlers.
func registerIdentityRoutes(r *httpx.Router, a *auth.Service, o *organisations.Service, idem *idempotency.Store, logger *slog.Logger) {
	pub, authn, user := httpx.Public(), httpx.Authenticated(), httpx.User()
	perm := httpx.Permission
	idemOpt := func(scope string) httpx.RouteOption {
		return httpx.With(idempotency.Middleware(idem, idempotency.Optional, scope, logger))
	}

	// authentication (public; CSRF origin checks apply to every unsafe request)
	r.HandleFunc("POST /api/v1/auth/register", pub, a.Register)
	r.HandleFunc("POST /api/v1/auth/verify-email", pub, a.VerifyEmail)
	r.HandleFunc("POST /api/v1/auth/resend-verification", pub, a.ResendVerification)
	r.HandleFunc("POST /api/v1/auth/login", pub, a.Login)
	r.HandleFunc("POST /api/v1/auth/mfa/verify", pub, a.MFAVerify)
	r.HandleFunc("POST /api/v1/auth/mfa/recovery", pub, a.MFARecovery)
	r.HandleFunc("POST /api/v1/auth/forgot-password", pub, a.ForgotPassword)
	r.HandleFunc("POST /api/v1/auth/reset-password", pub, a.ResetPassword)
	r.HandleFunc("POST /api/v1/auth/staff-invitation/start", pub, a.StaffInvitationStart)
	r.HandleFunc("POST /api/v1/auth/staff-invitation/finish", pub, a.StaffInvitationFinish)
	r.HandleFunc("GET /api/v1/auth/session", pub, a.SessionInfo)

	// session management
	r.HandleFunc("POST /api/v1/auth/logout", authn, a.Logout)
	r.HandleFunc("POST /api/v1/auth/logout-all", authn, a.LogoutAll)
	r.HandleFunc("POST /api/v1/auth/step-up/verify", authn, a.StepUp)

	// self service
	r.HandleFunc("GET /api/v1/me", authn, a.GetMe)
	r.HandleFunc("PATCH /api/v1/me", authn, a.PatchMe, idemOpt("me.patch"))
	r.HandleFunc("POST /api/v1/me/password", authn, a.ChangePassword)
	r.HandleFunc("POST /api/v1/me/email-change", user, a.RequestEmailChange)
	r.HandleFunc("GET /api/v1/me/security", authn, a.Security)
	r.HandleFunc("GET /api/v1/me/sessions", authn, a.Sessions)
	r.HandleFunc("DELETE /api/v1/me/sessions/{session_id}", authn, a.RevokeSession)
	r.HandleFunc("POST /api/v1/me/phone/verify-request", user, a.PhoneVerifyRequest)
	r.HandleFunc("POST /api/v1/me/phone/verify-confirm", user, a.PhoneVerifyConfirm)
	r.HandleFunc("POST /api/v1/me/mfa/enroll", authn, a.MFAEnroll)
	r.HandleFunc("POST /api/v1/me/mfa/confirm", authn, a.MFAConfirm)
	r.HandleFunc("POST /api/v1/me/mfa/disable", authn, a.MFADisable)
	r.HandleFunc("POST /api/v1/me/mfa/recovery-codes", authn, a.MFARegenerateRecoveryCodes)

	// organisations (personal accounts only)
	r.HandleFunc("POST /api/v1/organisations", user, o.Create, idemOpt("organisations.create"))
	r.HandleFunc("GET /api/v1/me/organisations", user, o.ListMine)
	r.HandleFunc("GET /api/v1/organisations/{org_id}", user, o.Get)
	r.HandleFunc("GET /api/v1/organisations/{org_id}/members", user, o.Members)
	r.HandleFunc("PATCH /api/v1/organisations/{org_id}/members/{member_id}", user, o.ChangeMemberRole)
	r.HandleFunc("DELETE /api/v1/organisations/{org_id}/members/{member_id}", user, o.RemoveMember)
	r.HandleFunc("POST /api/v1/organisations/{org_id}/invitations", user, o.Invite, idemOpt("organisations.invite"))
	r.HandleFunc("GET /api/v1/organisations/{org_id}/invitations", user, o.Invitations)
	r.HandleFunc("DELETE /api/v1/organisations/{org_id}/invitations/{invitation_id}", user, o.RevokeInvitation)
	r.HandleFunc("GET /api/v1/me/organisation-invitations", user, o.MyInvitations)
	r.HandleFunc("POST /api/v1/me/organisation-invitations/{invitation_id}/accept", user, o.AcceptInvitation)
	r.HandleFunc("POST /api/v1/me/organisation-invitations/{invitation_id}/decline", user, o.DeclineInvitation)

	// staff administration (404 for non-staff; permission + step-up per policy)
	r.HandleFunc("POST /api/v1/admin/staff", perm("staff.invite"), a.InviteStaff, idemOpt("admin.staff_invite"))
	r.HandleFunc("GET /api/v1/admin/role-assignment-requests", perm("role.assign.approve"), a.ListRoleRequests)
	r.HandleFunc("POST /api/v1/admin/role-assignment-requests", perm("role.assign.request"), a.CreateRoleRequest, idemOpt("admin.role_request"))
	r.HandleFunc("POST /api/v1/admin/role-assignment-requests/{role_request_id}/approve", perm("role.assign.approve"), a.ApproveRoleRequest)
	r.HandleFunc("POST /api/v1/admin/role-assignment-requests/{role_request_id}/reject", perm("role.assign.approve"), a.RejectRoleRequest)
	r.HandleFunc("GET /api/v1/admin/users/{user_id}", perm("user.view"), a.GetAdminUser)
	r.HandleFunc("POST /api/v1/admin/users/{user_id}/suspend", perm("account.suspend"), a.SuspendUser)
	r.HandleFunc("POST /api/v1/admin/users/{user_id}/reactivate", perm("account.reactivate"), a.ReactivateUser)
	r.HandleFunc("POST /api/v1/admin/staff/{user_id}/suspend", perm("staff.suspend"), a.SuspendStaff)
}

// registerIdentityConsumers subscribes the identity email consumers (worker).
func registerIdentityConsumers(reg *outbox.Registry, a *auth.Service, o *organisations.Service, mail auth.Mailer) {
	for _, c := range a.Consumers(mail) {
		h := c.Handle
		reg.Subscribe(c.Name, c.EventType, func(ctx context.Context, del outbox.Delivery) error { return h(ctx, del.Payload) })
	}
	h := o.InvitationEmailConsumer(mail)
	reg.Subscribe("organisations.send_invitation_email", organisations.EvMemberInvited,
		func(ctx context.Context, del outbox.Delivery) error { return h(ctx, del.Payload) })
}
