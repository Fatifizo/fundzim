// Package users owns accounts, profiles, email addresses and phone numbers (design-baseline §5.3). Other
// modules use this package's functions, always inside their own transaction (pgx.Tx), and never query these
// tables directly.
package users

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Account kinds and statuses (app.users CHECKs).
const (
	KindUser  = "USER"
	KindStaff = "STAFF"

	StatusActive    = "ACTIVE"
	StatusSuspended = "SUSPENDED"
)

// ErrNotFound means no matching row.
var ErrNotFound = errors.New("users: not found")

// ErrEmailTaken means another account already uses the sign-in address.
var ErrEmailTaken = errors.New("users: email already registered")

// ErrInvalidEmail is returned for addresses that cannot be normalised.
var ErrInvalidEmail = errors.New("users: invalid email address")

// NormalizeEmail trims and lower-cases an address and validates its basic form. It does not strip
// plus-tags or dots (providers differ; stripping would merge distinct mailboxes).
func NormalizeEmail(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " \t\r\n<>\"") {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 || !strings.Contains(s[at+1:], ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(s), nil
}

// NormalizeDisplayName trims a display name and checks its length (1–100 characters, no control characters).
func NormalizeDisplayName(raw string) (string, bool) {
	s := strings.Join(strings.Fields(raw), " ")
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 100 || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	return s, true
}

// Account is the identity summary other modules need.
type Account struct {
	ID            string
	Kind          string
	Status        string
	IsSystem      bool
	EmailID       string
	Email         string
	EmailVerified bool
	DisplayName   string
	CreatedAt     time.Time
}

// NewAccount describes an account to create.
type NewAccount struct {
	Kind        string
	Email       string // normalised
	DisplayName string
}

// Create inserts a user, profile and sign-in email (unverified). It returns ErrEmailTaken when the address
// is already a sign-in address (enforced by uq_user_emails_login, so concurrent registrations cannot both win).
func Create(ctx context.Context, tx pgx.Tx, a NewAccount) (userID, emailID string, err error) {
	userID, emailID = ids.New(), ids.New()
	if _, err = tx.Exec(ctx, `SAVEPOINT create_account`); err != nil {
		return "", "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO app.users (id, account_kind, status) VALUES ($1, $2, 'ACTIVE')`, userID, a.Kind)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO app.user_profiles (id, user_id, display_name) VALUES ($1, $2, $3)`, ids.New(), userID, a.DisplayName)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO app.user_emails (id, user_id, email, email_normalized, is_login)
			VALUES ($1, $2, $3, $3, true)`, emailID, userID, a.Email)
	}
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT create_account`)
		if db.IsUniqueViolation(err, "uq_user_emails_login") {
			return "", "", ErrEmailTaken
		}
		return "", "", err
	}
	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT create_account`)
	return userID, emailID, err
}

const accountSelect = `SELECT u.id, u.account_kind, u.status, u.is_system, e.id, e.email_normalized, e.verified_at IS NOT NULL,
	coalesce(p.display_name, ''), u.created_at
	FROM app.users u
	JOIN app.user_emails e ON e.user_id = u.id AND e.is_login AND e.deleted_at IS NULL
	LEFT JOIN app.user_profiles p ON p.user_id = u.id`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Kind, &a.Status, &a.IsSystem, &a.EmailID, &a.Email, &a.EmailVerified, &a.DisplayName, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Querier is satisfied by pgx.Tx, *pgx.Conn and *pgxpool.Pool.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ByLoginEmail finds the account whose sign-in address is email (normalised).
func ByLoginEmail(ctx context.Context, q Querier, email string) (Account, error) {
	return scanAccount(q.QueryRow(ctx, accountSelect+` WHERE e.email_normalized = $1`, email))
}

// ByID finds an account.
func ByID(ctx context.Context, q Querier, id string) (Account, error) {
	return scanAccount(q.QueryRow(ctx, accountSelect+` WHERE u.id = $1`, id))
}

// ByIDForUpdate locks the user row (serialises concurrent security changes on one account).
func ByIDForUpdate(ctx context.Context, tx pgx.Tx, id string) (Account, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM app.users WHERE id = $1 FOR UPDATE`, id); err != nil {
		return Account{}, err
	}
	return ByID(ctx, tx, id)
}

// MarkEmailVerified sets verified_at (idempotent) and makes it the primary address if the user has none.
func MarkEmailVerified(ctx context.Context, tx pgx.Tx, userID, emailID string, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE app.user_emails SET verified_at = coalesce(verified_at, $3)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, emailID, userID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE app.user_emails SET is_primary = true WHERE id = $1
		AND NOT EXISTS (SELECT 1 FROM app.user_emails o WHERE o.user_id = $2 AND o.is_primary AND o.deleted_at IS NULL)`, emailID, userID)
	return err
}

// AddPendingEmail adds a non-login address to be verified for an email change.
func AddPendingEmail(ctx context.Context, tx pgx.Tx, userID, email string) (string, error) {
	// remove any earlier pending (unverified, non-login) change address first
	if _, err := tx.Exec(ctx, `UPDATE app.user_emails SET deleted_at = now(), email = NULL, email_normalized = NULL
		WHERE user_id = $1 AND NOT is_login AND verified_at IS NULL AND deleted_at IS NULL`, userID); err != nil {
		return "", err
	}
	id := ids.New()
	_, err := tx.Exec(ctx, `INSERT INTO app.user_emails (id, user_id, email, email_normalized) VALUES ($1, $2, $3, $3)`, id, userID, email)
	return id, err
}

// EmailTakenByOther reports whether email is another account's sign-in address.
func EmailTakenByOther(ctx context.Context, q Querier, userID, email string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM app.user_emails WHERE email_normalized = $1 AND is_login AND deleted_at IS NULL AND user_id <> $2`,
		email, userID).Scan(&n)
	return n > 0, err
}

// SwapLoginEmail makes newEmailID (verified now) the sign-in and primary address, retiring the old one.
func SwapLoginEmail(ctx context.Context, tx pgx.Tx, userID, newEmailID string, at time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE app.user_emails SET is_login = false, is_primary = false, deleted_at = $2
		WHERE user_id = $1 AND is_login AND deleted_at IS NULL`, userID, at); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE app.user_emails SET is_login = true, is_primary = true, verified_at = coalesce(verified_at, $3)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, newEmailID, userID, at)
	if err != nil {
		if db.IsUniqueViolation(err, "") {
			return ErrEmailTaken
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EmailByID returns an address of the user (normalised) and whether it is verified.
func EmailByID(ctx context.Context, q Querier, userID, emailID string) (email string, verified bool, err error) {
	err = q.QueryRow(ctx, `SELECT email_normalized, verified_at IS NOT NULL FROM app.user_emails
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, emailID, userID).Scan(&email, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return email, verified, err
}

// UpdateDisplayName changes the profile display name.
func UpdateDisplayName(ctx context.Context, tx pgx.Tx, userID, name string) error {
	tag, err := tx.Exec(ctx, `UPDATE app.user_profiles SET display_name = $2 WHERE user_id = $1`, userID, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetStatus moves the account between ACTIVE and SUSPENDED (the transition guard rejects illegal moves).
func SetStatus(ctx context.Context, tx pgx.Tx, userID, status string, at time.Time) error {
	var suspended any
	if status == StatusSuspended {
		suspended = at
	}
	tag, err := tx.Exec(ctx, `UPDATE app.users SET status = $2, suspended_at = $3 WHERE id = $1 AND NOT is_system`, userID, status, suspended)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Phone is a verified phone number.
type Phone struct {
	E164       string
	VerifiedAt time.Time
}

// PrimaryPhone returns the user's verified primary phone, or ErrNotFound.
func PrimaryPhone(ctx context.Context, q Querier, userID string) (Phone, error) {
	var p Phone
	err := q.QueryRow(ctx, `SELECT e164, verified_at FROM app.user_phone_numbers
		WHERE user_id = $1 AND is_primary AND deleted_at IS NULL`, userID).Scan(&p.E164, &p.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ErrPhoneTaken means the number is already verified by another account.
var ErrPhoneTaken = errors.New("users: phone verified by another account")

// SetVerifiedPhone records e164 as the user's verified primary phone, retiring any previous number.
func SetVerifiedPhone(ctx context.Context, tx pgx.Tx, userID, e164 string, at time.Time) error {
	if _, err := tx.Exec(ctx, `SAVEPOINT set_phone`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE app.user_phone_numbers SET deleted_at = $2, is_primary = false
		WHERE user_id = $1 AND deleted_at IS NULL`, userID, at)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO app.user_phone_numbers (id, user_id, e164, is_primary, verified_at)
			VALUES ($1, $2, $3, true, $4)`, ids.New(), userID, e164, at)
	}
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT set_phone`)
		if db.IsUniqueViolation(err, "uq_user_phone_numbers_verified_e164") {
			return ErrPhoneTaken
		}
		return err
	}
	_, err = tx.Exec(ctx, `RELEASE SAVEPOINT set_phone`)
	return err
}

// MaskPhone shows only the country code prefix and last three digits (+26377****123).
func MaskPhone(e164 string) string {
	if len(e164) < 8 {
		return "****"
	}
	return e164[:6] + strings.Repeat("*", len(e164)-9) + e164[len(e164)-3:]
}

// DisplayNames returns the display names of the given users (missing IDs are absent from the map).
func DisplayNames(ctx context.Context, q Querier, userIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT user_id, display_name FROM app.user_profiles WHERE user_id = ANY($1::uuid[])`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// Legal documents a user can accept (app.user_terms_acceptances).
const (
	DocTermsOfUse = "TERMS_OF_USE"

	// CurrentTermsVersion identifies the terms text shown at registration. The text is a placeholder until
	// counsel drafts it (LEGAL_REVIEW_REQUIRED, LR-022); publishing real terms means a new version string and
	// a re-consent flow for existing users.
	CurrentTermsVersion = "placeholder-2026-10-08"
)

// RecordAcceptance stores that userID accepted version of document at `at` (append-only; accepting the same
// version twice is a no-op).
func RecordAcceptance(ctx context.Context, tx pgx.Tx, userID, document, version string, at time.Time, ip any) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.user_terms_acceptances (id, user_id, document, version, accepted_at, ip)
		VALUES ($1, $2, $3, $4, $5, $6::inet) ON CONFLICT (user_id, document, version) DO NOTHING`,
		ids.New(), userID, document, version, at, ip)
	return err
}

// PersonalAccount returns the personal (USER) account linked to a staff account ("" when none).
func PersonalAccount(ctx context.Context, q Querier, staffID string) (string, error) {
	var id *string
	err := q.QueryRow(ctx, `SELECT staff_personal_user_id FROM app.users WHERE id = $1`, staffID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) || id == nil {
		return "", nil
	}
	return *id, err
}

// SetKYCMirror updates the read-only verification mirror on app.users (written only from kyc.level_changed
// events; the kyc schema stays authoritative).
func SetKYCMirror(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, userID, level, status string, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE app.users SET kyc_level = $2, kyc_status = $3, kyc_mirror_updated_at = $4
		WHERE id = $1 AND (kyc_mirror_updated_at IS NULL OR kyc_mirror_updated_at <= $4)`, userID, level, status, at)
	return err
}
