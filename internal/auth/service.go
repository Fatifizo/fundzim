// Package auth implements authentication, sessions, CSRF protection, MFA, phone verification, staff
// administration and the request Authorizer (ADR-027, ADR-032, SECURITY §4–§7).
//
// Security-sensitive invariants are enforced in the database as well (sessions, tokens, challenges, role
// assignments); this package is the first line of defence and the source of audit events.
package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/auth/passwords"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
)

// OutboxEvent is a domain event written in the same transaction as the change that caused it.
type OutboxEvent struct {
	EventType   string
	AggregateID string
	Payload     map[string]any // IDs and minimal fields only; never secrets
}

// EventWriter writes outbox events inside tx (wired to outbox.Write).
type EventWriter func(ctx context.Context, tx pgx.Tx, e OutboxEvent) error

// Throttler applies a named rate-limit policy to a key and returns a RATE_LIMITED *errs.Error when the
// limit is exceeded (wired to the distributed limiter). Implementations must never fail open.
type Throttler interface {
	Allow(ctx context.Context, policy, key string) error
}

// SMSSender sends one SMS synchronously (wired to notifications.SMSSender; refuses inside a DB transaction).
type SMSSender interface {
	SendSMS(ctx context.Context, toE164, body string) error
}

// Mailer sends one email (wired to notifications.EmailSender; used by outbox consumers in the worker).
type Mailer interface {
	SendEmail(ctx context.Context, to, subject, text string) error
}

// Deps are the service dependencies.
type Deps struct {
	Pool     *pgxpool.Pool
	Cfg      config.Auth
	Clock    clock.Clock
	Hasher   *passwords.Hasher
	AEAD     *crypto.AEAD
	Keyed    *crypto.Keyed
	Events   EventWriter
	Throttle Throttler
	SMS      SMSSender
	Logger   *slog.Logger
}

// Service is the auth module.
type Service struct {
	Deps
	policy        passwords.Policy
	dummyHash     string
	stepUpPerms   map[string]bool
	permsLoadedAt time.Time
}

// New builds the service. It loads which permissions require step-up from the reference table.
func New(ctx context.Context, d Deps) (*Service, error) {
	s := &Service{Deps: d, policy: passwords.Policy{MinLength: d.Cfg.PasswordMinLength, MaxLength: d.Cfg.PasswordMaxLength}}
	s.dummyHash = d.Hasher.DummyHash(ctx)
	s.stepUpPerms = map[string]bool{}
	rows, err := d.Pool.Query(ctx, `SELECT code FROM app.permissions WHERE requires_step_up`)
	if err != nil {
		// The database may be down at startup; requiring step-up for every permission is the safe default.
		d.Logger.Warn("auth: could not load step-up permissions; requiring step-up for all permissions", slog.String("error_category", "dependency"))
		s.stepUpPerms = nil
		return s, nil
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		s.stepUpPerms[c] = true
	}
	return s, rows.Err()
}

func (s *Service) now() time.Time { return s.Clock.Now() }

// requiresStepUp reports whether a permission needs a fresh factor; unknown means yes (fail closed).
func (s *Service) requiresStepUp(perm string) bool {
	if s.stepUpPerms == nil {
		return true
	}
	return s.stepUpPerms[perm]
}
