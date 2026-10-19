package notifications

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Errors of the user and staff APIs (mapped to stable codes in http.go).
var (
	ErrNotFound        = errors.New("notifications: not found")
	ErrInvalid         = errors.New("notifications: invalid request")
	ErrMandatory       = errors.New("notifications: this notification cannot be turned off")
	ErrMarketingClosed = errors.New("notifications: marketing messages are not available")
	ErrState           = errors.New("notifications: not possible in the current state")
	ErrStale           = errors.New("notifications: the record changed")
	ErrSelfApproval    = errors.New("notifications: a different person must decide")
	ErrReplayBlocked   = errors.New("notifications: replay blocked")
)

// InboxItem is one in-app notification.
type InboxItem struct {
	ID          string     `json:"id"`
	Category    string     `json:"category"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Priority    string     `json:"priority"`
	DeepLink    *string    `json:"deep_link"`
	RelatedType *string    `json:"related_type"`
	RelatedID   *string    `json:"related_id"`
	Synthetic   bool       `json:"test_data"`
	ReadAt      *time.Time `json:"read_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// InboxFilter filters the inbox.
type InboxFilter struct {
	UnreadOnly bool
	Category   string
	Limit      int
	Cursor     string // "<created_at RFC3339Nano>|<id>"
}

// Inbox lists the caller's in-app notifications, newest first (only the caller's own: tenant isolation by query).
func (s *Service) Inbox(ctx context.Context, userID string, f InboxFilter) ([]InboxItem, string, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	if f.Category != "" && !contains(Categories, f.Category) {
		return nil, "", ErrInvalid
	}
	var curAt *time.Time
	var curID *string
	if f.Cursor != "" {
		at, id, ok := strings.Cut(f.Cursor, "|")
		t, err := time.Parse(time.RFC3339Nano, at)
		if !ok || err != nil || !ids.Valid(id) {
			return nil, "", ErrInvalid
		}
		curAt, curID = &t, &id
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, category, inapp_title, inapp_body, priority, deep_link, related_type, related_id::text, synthetic,
		read_at, created_at FROM app.notifications WHERE recipient_user_id = $1 AND in_app AND ($2 = false OR read_at IS NULL) AND ($3 = '' OR category = $3)
		AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid)) ORDER BY created_at DESC, id DESC LIMIT $6`,
		userID, f.UnreadOnly, f.Category, curAt, curID, f.Limit+1)
	if err != nil {
		return nil, "", err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxItem, error) {
		var it InboxItem
		err := r.Scan(&it.ID, &it.Category, &it.Title, &it.Body, &it.Priority, &it.DeepLink, &it.RelatedType, &it.RelatedID, &it.Synthetic, &it.ReadAt, &it.CreatedAt)
		return it, err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > f.Limit {
		items = items[:f.Limit]
		last := items[len(items)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return items, next, nil
}

// UnreadCount is the caller's unread in-app count (indexed).
func (s *Service) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.notifications WHERE recipient_user_id = $1 AND in_app AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkRead marks one of the caller's notifications read (another person's: not found).
func (s *Service) MarkRead(ctx context.Context, userID, id string) error {
	if !ids.Valid(id) {
		return ErrNotFound
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()
		var readAt *time.Time
		err := tx.QueryRow(ctx, `SELECT read_at FROM app.notifications WHERE id = $1 AND recipient_user_id = $2 AND in_app FOR UPDATE`, id, userID).Scan(&readAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || readAt != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.notifications SET read_at = $2 WHERE id = $1`, id, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'READ', read_at = $2, version = version + 1
			WHERE notification_id = $1 AND channel = 'IN_APP' AND status = 'DELIVERED'`, id, now)
		return err
	})
}

// MarkAllRead marks every unread in-app notification of the caller read; returns how many.
func (s *Service) MarkAllRead(ctx context.Context, userID string) (int, error) {
	n := 0
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()
		rows, err := tx.Query(ctx, `UPDATE app.notifications SET read_at = $2 WHERE recipient_user_id = $1 AND in_app AND read_at IS NULL RETURNING id`, userID, now)
		if err != nil {
			return err
		}
		idl, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		n = len(idl)
		if n == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'READ', read_at = $2, version = version + 1
			WHERE notification_id = ANY($1::uuid[]) AND channel = 'IN_APP' AND status = 'DELIVERED'`, idl, now)
		return err
	})
	return n, err
}

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- preferences ----------------------------------------------------------------------------------------------

// Preference is one (category, channel) setting as the user sees it.
type Preference struct {
	Category        string `json:"category"`
	Channel         string `json:"channel"`
	Enabled         bool   `json:"enabled"`
	Mandatory       bool   `json:"mandatory"`
	ConsentRequired bool   `json:"consent_required"`
	Available       bool   `json:"available"` // false: the channel cannot carry this category now (marketing; no provider in production)
}

// userCategories are the categories a user (not staff) receives.
var userCategories = []string{CatSecurity, CatCampaign, CatVerification, CatPayment, CatWithdrawal, CatPayout, CatSupport, CatMarketing}

// Preferences returns the caller's settings for every user category and channel.
func (s *Service) Preferences(ctx context.Context, userID string) ([]Preference, error) {
	rows, err := s.Pool.Query(ctx, `SELECT category, channel, enabled FROM app.notification_preferences WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	has := map[string]bool{}
	for rows.Next() {
		var c, ch string
		var on bool
		if err := rows.Scan(&c, &ch, &on); err != nil {
			rows.Close()
			return nil, err
		}
		set[c+"|"+ch], has[c+"|"+ch] = on, true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Preference{}
	for _, cat := range userCategories {
		p := Policies[cat]
		for _, ch := range p.Channels {
			on := contains(p.DefaultOn, ch)
			if has[cat+"|"+ch] {
				on = set[cat+"|"+ch]
			}
			mand := contains(p.Mandatory, ch)
			if mand {
				on = true
			}
			avail := cat != CatMarketing || MarketingEnabled
			if ch != ChInApp && s.Production && !LiveMessagingEnabled {
				avail = false
			}
			out = append(out, Preference{Category: cat, Channel: ch, Enabled: on, Mandatory: mand, ConsentRequired: ConsentRequired(cat, ch), Available: avail})
		}
	}
	return out, nil
}

// PreferenceChange is one requested setting.
type PreferenceChange struct {
	Category string `json:"category"`
	Channel  string `json:"channel"`
	Enabled  bool   `json:"enabled"`
}

// SetPreferences applies the caller's changes atomically (a mandatory channel cannot be turned off).
func (s *Service) SetPreferences(ctx context.Context, userID string, changes []PreferenceChange) error {
	if len(changes) == 0 || len(changes) > 100 {
		return ErrInvalid
	}
	for _, c := range changes {
		if !contains(userCategories, c.Category) || !contains(Policies[c.Category].Channels, c.Channel) {
			return ErrInvalid
		}
		if !PreferenceAllowed(c.Category, c.Channel, c.Enabled) {
			return ErrMandatory
		}
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()
		for _, c := range changes {
			if _, err := tx.Exec(ctx, `INSERT INTO app.notification_preferences (user_id, category, channel, enabled, updated_at) VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (user_id, category, channel) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`,
				userID, c.Category, c.Channel, c.Enabled, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.notification_preference_events (id, user_id, category, channel, enabled, changed_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				ids.New(), userID, c.Category, c.Channel, c.Enabled, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.preferences.updated", ActorType: "user", ActorID: userID, TargetType: "user",
			TargetID: userID, Metadata: map[string]any{"changes": len(changes)}})
	})
}

// ---- consent ----------------------------------------------------------------------------------------------------

// ConsentView is one (channel, purpose) consent as the user sees it.
type ConsentView struct {
	Channel       string     `json:"channel"`
	Purpose       string     `json:"purpose"`
	Status        string     `json:"status"` // NOT_REQUESTED when no record exists
	PolicyVersion *string    `json:"policy_version"`
	CurrentPolicy string     `json:"current_policy_version"`
	GrantedAt     *time.Time `json:"granted_at"`
	WithdrawnAt   *time.Time `json:"withdrawn_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Available     bool       `json:"available"` // marketing consent cannot be given while marketing is not approved
	Version       int        `json:"version"`
}

var consentChannels = []string{ChEmail, ChSMS, ChWhatsApp}

// Consents returns the caller's consent state for every channel and purpose (no record: NOT_REQUESTED).
func (s *Service) Consents(ctx context.Context, userID string) ([]ConsentView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT channel, purpose, status, policy_version, granted_at, withdrawn_at, expires_at, version
		FROM app.communication_consents WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	got := map[string]ConsentView{}
	for rows.Next() {
		var v ConsentView
		var pv string
		if err := rows.Scan(&v.Channel, &v.Purpose, &v.Status, &pv, &v.GrantedAt, &v.WithdrawnAt, &v.ExpiresAt, &v.Version); err != nil {
			rows.Close()
			return nil, err
		}
		v.PolicyVersion = &pv
		if v.Status == "GRANTED" && pv != ConsentPolicyVersion {
			v.Status = "REQUIRES_RECONFIRMATION" // shown as the router treats it
		}
		if v.Status == "GRANTED" && v.ExpiresAt != nil && !s.now().Before(*v.ExpiresAt) {
			v.Status = "EXPIRED"
		}
		got[v.Channel+"|"+v.Purpose] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []ConsentView{}
	for _, ch := range consentChannels {
		for _, purpose := range []string{PurposeService, PurposeMarketing} {
			v, ok := got[ch+"|"+purpose]
			if !ok {
				v = ConsentView{Channel: ch, Purpose: purpose, Status: "NOT_REQUESTED"}
			}
			v.CurrentPolicy = ConsentPolicyVersion
			v.Available = purpose != PurposeMarketing || MarketingEnabled
			out = append(out, v)
		}
	}
	return out, nil
}

// SetConsent records the caller's own grant or withdrawal (consent is never recorded on anyone's behalf). evidence is
// the request reference that captured the decision.
func (s *Service) SetConsent(ctx context.Context, userID, channel, purpose string, grant bool, evidence string) (ConsentView, error) {
	if !contains(consentChannels, channel) || (purpose != PurposeService && purpose != PurposeMarketing) || evidence == "" || len(evidence) > 200 {
		return ConsentView{}, ErrInvalid
	}
	if grant && purpose == PurposeMarketing && !MarketingEnabled {
		return ConsentView{}, ErrMarketingClosed
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()
		var id, status string
		err := tx.QueryRow(ctx, `SELECT id::text, status FROM app.communication_consents WHERE user_id = $1 AND channel = $2 AND purpose = $3 FOR UPDATE`,
			userID, channel, purpose).Scan(&id, &status)
		to := "WITHDRAWN"
		if grant {
			to = "GRANTED"
		}
		var from any
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// first decision on this channel and purpose (a withdrawal without a prior grant is recorded too)
			id = ids.New()
			var granted, withdrawn any
			if grant {
				granted = now
			} else {
				withdrawn = now
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.communication_consents (id, user_id, channel, purpose, status, source, policy_version, evidence_ref,
				granted_at, withdrawn_at, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,'USER_SETTINGS',$6,$7,$8,$9,$10,$10)`,
				id, userID, channel, purpose, to, ConsentPolicyVersion, evidence, granted, withdrawn, now); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			from = status
			if status == to && to != "GRANTED" {
				return nil // already withdrawn
			}
			if status == to {
				// a re-grant refreshes the policy version and evidence (reconfirmation)
				if _, err := tx.Exec(ctx, `UPDATE app.communication_consents SET policy_version = $2, evidence_ref = $3, granted_at = $4, source = 'USER_SETTINGS',
					version = version + 1 WHERE id = $1`, id, ConsentPolicyVersion, evidence, now); err != nil {
					return err
				}
			} else {
				set := `status = $2, source = 'USER_SETTINGS', policy_version = $3, evidence_ref = $4, version = version + 1`
				if grant {
					set += `, granted_at = $5, withdrawn_at = NULL`
				} else {
					set += `, withdrawn_at = $5`
				}
				if _, err := tx.Exec(ctx, `UPDATE app.communication_consents SET `+set+` WHERE id = $1`, id, to, ConsentPolicyVersion, evidence, now); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.communication_consent_events (id, consent_id, from_status, to_status, source, policy_version, evidence_ref, occurred_at)
			VALUES ($1,$2,$3,$4,'USER_SETTINGS',$5,$6,$7)`, ids.New(), id, from, to, ConsentPolicyVersion, evidence, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.consent.recorded", ActorType: "user", ActorID: userID, TargetType: "communication_consent",
			TargetID: id, Metadata: map[string]any{"channel": channel, "purpose": purpose, "status": to, "policy_version": ConsentPolicyVersion}})
	})
	if err != nil {
		return ConsentView{}, err
	}
	all, err := s.Consents(ctx, userID)
	if err != nil {
		return ConsentView{}, err
	}
	for _, v := range all {
		if v.Channel == channel && v.Purpose == purpose {
			return v, nil
		}
	}
	return ConsentView{}, ErrNotFound
}
