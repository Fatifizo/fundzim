package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/users"
)

// ConsumerDispatch is the engine's outbox consumer (stable name: it is part of the inbox dedupe key).
const ConsumerDispatch = "notifications.dispatch"

// Directory resolves recipients the engine cannot read itself (wired in internal/app: organisations and auth).
type Directory interface {
	OrganisationAdmins(ctx context.Context, orgID string) ([]string, error)
	StaffWithPermission(ctx context.Context, permission string) ([]string, error)
}

// Settings are engineering defaults (documented in docs/stage-15/retries.md); none is a provider or legal limit.
type Settings struct {
	Batch        int           // deliveries claimed per sweep
	Lease        time.Duration // a PROCESSING claim older than this is UNKNOWN (the worker may have crashed mid-call)
	SendTimeout  time.Duration
	BackoffBase  time.Duration
	BackoffCap   time.Duration
	MaxAttempts  int
	UnknownGrace time.Duration // wait before looking an UNKNOWN outcome up
	UnknownMax   time.Duration // an outcome still unknown after this is dead-lettered for an operator
}

// DefaultSettings returns the engineering defaults.
func DefaultSettings() Settings {
	return Settings{Batch: 50, Lease: 2 * time.Minute, SendTimeout: 15 * time.Second, BackoffBase: 30 * time.Second, BackoffCap: 30 * time.Minute,
		MaxAttempts: 6, UnknownGrace: 2 * time.Minute, UnknownMax: 24 * time.Hour}
}

// Service is the notification engine.
type Service struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Logger     *slog.Logger
	Directory  Directory
	Production bool   // APP_ENV=production: external delivery is suppressed (LiveMessagingEnabled = false)
	PublicURL  string // links in messages are built from it
	WorkerID   string
	Settings   Settings
	// Active is the provider used for new deliveries, per channel (absent: PROVIDER_NOT_CONFIGURED).
	Active map[string]Provider
	// Providers are every adapter this process can send through, look up and receive callbacks for.
	Providers map[string]Provider
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) settings() Settings {
	if s.Settings.Batch == 0 {
		return DefaultSettings()
	}
	return s.Settings
}

// Intent is one notification to create.
type Intent struct {
	TemplateKey string
	Category    string
	Recipients  []string // user ids
	Staff       bool     // recipients are staff accounts (in-app only)
	Variables   map[string]string
	RelatedType string
	RelatedID   string
	DeepLink    string
	Synthetic   bool // TEST money or simulated source: in-app only, labelled TEST
	Priority    string
}

// EventRef identifies the source event of a set of intents (exactly-once creation).
type EventRef struct {
	EventID, EventType, AggregateType, AggregateID, CorrelationID string
	OccurredAt                                                    time.Time
}

// ErrTemplateUnavailable: no ACTIVE template version; the event is retried by the outbox until one is active.
var ErrTemplateUnavailable = errors.New("notifications: no active template version")

// Handle is the outbox handler: map the event, resolve recipients, create notifications once.
func (s *Service) Handle(ctx context.Context, d outbox.Delivery) error {
	intents, err := s.intentsFor(ctx, d)
	if err != nil {
		return err
	}
	return s.Create(ctx, EventRef{EventID: d.EventID, EventType: d.EventType, AggregateType: d.AggregateType, AggregateID: d.AggregateID,
		CorrelationID: d.CorrelationID, OccurredAt: d.OccurredAt}, intents, &d)
}

// Create records the event and its notifications in one transaction. A second call for the same event is a no-op.
func (s *Service) Create(ctx context.Context, ev EventRef, intents []Intent, d *outbox.Delivery) error {
	now := s.now()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	synthetic := false
	for _, in := range intents {
		synthetic = synthetic || in.Synthetic
	}
	outcome := "NOT_NOTIFIED" // the event maps to no notification (e.g. a TEST success without a donor)
	for _, in := range intents {
		if len(in.Recipients) > 0 {
			outcome = "CREATED"
		} else if outcome == "NOT_NOTIFIED" {
			outcome = "NO_RECIPIENTS"
		}
	}
	eventID := ids.New()
	var corr *string
	if ev.CorrelationID != "" {
		c := ev.CorrelationID
		corr = &c
	}
	tag, err := tx.Exec(ctx, `INSERT INTO app.notification_events (id, source_event_id, event_type, aggregate_type, aggregate_id, correlation_id,
		occurred_at, received_at, outcome, synthetic) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (source_event_id) DO NOTHING`,
		eventID, ev.EventID, ev.EventType, ev.AggregateType, ev.AggregateID, corr, ev.OccurredAt, now, outcome, synthetic)
	if err != nil {
		return fmt.Errorf("notifications: record event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil // already processed (at-least-once delivery of the outbox event)
	}
	for _, in := range intents {
		if _, err := s.createIntent(ctx, tx, &eventID, ev.EventID, in, now); err != nil {
			return err
		}
	}
	if d != nil {
		if _, err := outbox.Consume(ctx, tx, ConsumerDispatch, *d); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Notify creates notifications without a source event (operations alerts, tests); dedupeKey makes it idempotent.
func (s *Service) Notify(ctx context.Context, dedupeKey string, in Intent) (int, error) {
	if strings.TrimSpace(dedupeKey) == "" || len(dedupeKey) > 200 {
		return 0, errors.New("notifications: a dedupe key is required")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := s.createIntent(ctx, tx, nil, dedupeKey, in, s.now())
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

type activeVersion struct {
	TemplateID, VersionID string
	Allowed               []string
	WhatsAppStatus        string
	Content
}

func loadActive(ctx context.Context, q pgx.Tx, key string) (activeVersion, error) {
	var v activeVersion
	err := q.QueryRow(ctx, `SELECT t.id::text, v.id::text, t.allowed_variables, v.whatsapp_provider_status, v.email_subject, v.email_text, v.sms_text,
		v.whatsapp_template_name, v.whatsapp_language, v.inapp_title, v.inapp_body
		FROM app.notification_templates t JOIN app.notification_template_versions v ON v.template_id = t.id
		WHERE t.template_key = $1 AND v.locale = 'en' AND v.status = 'ACTIVE'`, key).
		Scan(&v.TemplateID, &v.VersionID, &v.Allowed, &v.WhatsAppStatus, &v.EmailSubject, &v.EmailText, &v.SMSText, &v.WhatsAppName,
			&v.WhatsAppLanguage, &v.InAppTitle, &v.InAppBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, fmt.Errorf("%w: %s", ErrTemplateUnavailable, key)
	}
	return v, err
}

// contact is a recipient's current contact state.
type contact struct {
	Kind, Status  string
	IsSystem      bool
	DisplayName   string
	Email         string
	EmailVerified bool
	Phone         string
	PhoneVerified bool
}

func loadContact(ctx context.Context, q users.Querier, userID string) (contact, error) {
	a, err := users.ByID(ctx, q, userID)
	if err != nil {
		return contact{}, err
	}
	c := contact{Kind: a.Kind, Status: a.Status, IsSystem: a.IsSystem, DisplayName: a.DisplayName, Email: a.Email, EmailVerified: a.EmailVerified}
	if p, err := users.PrimaryPhone(ctx, q, userID); err == nil {
		c.Phone, c.PhoneVerified = p.E164, !p.VerifiedAt.IsZero()
	} else if !errors.Is(err, users.ErrNotFound) {
		return contact{}, err
	}
	return c, nil
}

func (c contact) destination(channel string) (string, bool) {
	switch channel {
	case ChEmail:
		return c.Email, c.EmailVerified
	case ChSMS, ChWhatsApp:
		return c.Phone, c.PhoneVerified
	}
	return "", false
}

// simulatedChannel: the channel's active provider is a simulator (a simulator acceptance only counts there).
func (s *Service) simulatedChannel(channel string) bool {
	p := s.Active[channel]
	return p != nil && p.Simulated()
}

func destinationHash(channel, to string) []byte {
	h := sha256.Sum256([]byte(channel + ":" + strings.ToLower(strings.TrimSpace(to))))
	return h[:]
}

// routeFor gathers the facts for one channel of one notification and decides (also used before every send).
func (s *Service) routeFor(ctx context.Context, q users.Querier, userID, templateKey, category, channel string, staff, synthetic bool,
	c contact, v Content, whatsappStatus string) (string, string, error) {
	in := RouteInput{Category: category, Channel: channel, StaffRecipient: staff, Synthetic: synthetic, Production: s.Production,
		Active: (c.Status == "ACTIVE" || inactiveRecipientOK[templateKey]) && !c.IsSystem, TemplateHasChannel: v.HasChannel(channel), Now: s.now(),
		TemplateProviderApproved: whatsappStatus == "APPROVED" || (whatsappStatus == "SIMULATOR_ACCEPTED" && s.simulatedChannel(ChWhatsApp))}
	var pref bool
	err := q.QueryRow(ctx, `SELECT enabled FROM app.notification_preferences WHERE user_id = $1 AND category = $2 AND channel = $3`,
		userID, category, channel).Scan(&pref)
	switch {
	case err == nil:
		in.Preference = &pref
	case !errors.Is(err, pgx.ErrNoRows):
		return "", "", err
	}
	to := ""
	if channel != ChInApp {
		var verified bool
		to, verified = c.destination(channel)
		if channel == ChEmail && unverifiedSignInEmailOK[templateKey] {
			verified = true
		}
		in.ContactKnown, in.ContactVerified = to != "", verified
		if ConsentRequired(category, channel) {
			var cs Consent
			err := q.QueryRow(ctx, `SELECT status, policy_version, expires_at FROM app.communication_consents WHERE user_id = $1 AND channel = $2 AND purpose = $3`,
				userID, channel, PurposeFor(category)).Scan(&cs.Status, &cs.PolicyVersion, &cs.ExpiresAt)
			switch {
			case err == nil:
				in.Consent = &cs
			case !errors.Is(err, pgx.ErrNoRows):
				return "", "", err // a consent read failure never sends (fail closed)
			}
		}
		if to != "" {
			var n int
			if err := q.QueryRow(ctx, `SELECT count(*) FROM app.notification_suppressions WHERE channel = $1 AND destination_hash = $2 AND lifted_at IS NULL`,
				channel, destinationHash(channel, to)).Scan(&n); err != nil {
				return "", "", err
			}
			in.Suppressed = n > 0
		}
		_, in.ProviderReady = s.Active[channel]
	}
	return Route(in), to, nil
}

func (s *Service) createIntent(ctx context.Context, tx pgx.Tx, eventID *string, dedupeKey string, in Intent, now time.Time) (int, error) {
	if len(in.Recipients) == 0 {
		return 0, nil
	}
	if _, ok := Policies[in.Category]; !ok {
		return 0, fmt.Errorf("notifications: unknown category %q", in.Category)
	}
	v, err := loadActive(ctx, tx, in.TemplateKey)
	if err != nil {
		return 0, err
	}
	pol := Policies[in.Category]
	created := 0
	seen := map[string]bool{}
	for _, uid := range in.Recipients {
		if !ids.Valid(uid) || seen[uid] {
			continue
		}
		seen[uid] = true
		c, err := loadContact(ctx, tx, uid)
		if errors.Is(err, users.ErrNotFound) {
			continue
		}
		if err != nil {
			return created, err
		}
		staff := in.Staff || c.Kind == users.KindStaff
		vars := map[string]string{"display_name": c.DisplayName}
		for k, val := range in.Variables {
			vars[k] = val
		}
		if s.PublicURL != "" {
			vars["forgot_password_url"] = s.PublicURL + "/forgot-password"
		}
		inapp, err := Render(v.Content, vars, ChInApp, in.Synthetic)
		if err != nil {
			return created, err
		}
		channels := pol.Channels // staff restrictions are decided (and recorded) by Route
		type dec struct{ channel, reason, to string }
		var decisions []dec
		inAppShown := false
		for _, ch := range channels {
			reason, to, err := s.routeFor(ctx, tx, uid, in.TemplateKey, in.Category, ch, staff, in.Synthetic, c, v.Content, v.WhatsAppStatus)
			if err != nil {
				return created, err
			}
			if ch == ChInApp && reason == "" {
				inAppShown = true
			}
			decisions = append(decisions, dec{ch, reason, to})
		}
		kind := "USER"
		if staff {
			kind = "STAFF"
		}
		prio := in.Priority
		if prio == "" {
			prio = "NORMAL"
		}
		varsJSON, _ := json.Marshal(in.Variables)
		nid := ids.New()
		var related, deep any
		if ids.Valid(in.RelatedID) {
			related = in.RelatedID
		}
		if in.DeepLink != "" {
			deep = in.DeepLink
		}
		relType := any(nil)
		if in.RelatedType != "" && related != nil {
			relType = in.RelatedType
		}
		tag, err := tx.Exec(ctx, `INSERT INTO app.notifications (id, event_id, template_id, template_version_id, template_key, category, recipient_user_id,
			recipient_kind, synthetic, priority, related_type, related_id, deep_link, variables, in_app, inapp_title, inapp_body, dedupe_key, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
			ON CONFLICT (dedupe_key, recipient_user_id, template_key) DO NOTHING`,
			nid, eventID, v.TemplateID, v.VersionID, in.TemplateKey, in.Category, uid, kind, in.Synthetic, prio, relType, related, deep, varsJSON,
			inAppShown, inapp.Title, inapp.Body, dedupeKey, now)
		if err != nil {
			return created, fmt.Errorf("notifications: insert notification: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		created++
		for _, d := range decisions {
			if err := s.insertDelivery(ctx, tx, nid, d.channel, d.reason, d.to, now); err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

func (s *Service) insertDelivery(ctx context.Context, tx pgx.Tx, notificationID, channel, reason, to string, now time.Time) error {
	id := ids.New()
	var masked, hash any
	if to != "" {
		masked, hash = maskDestination(channel, to), destinationHash(channel, to)
	}
	switch {
	case channel == ChInApp && reason == "":
		_, err := tx.Exec(ctx, `INSERT INTO app.notification_deliveries (id, notification_id, channel, status, decision, delivered_at, created_at)
			VALUES ($1, $2, 'IN_APP', 'DELIVERED', 'ROUTED', $3, $3)`, id, notificationID, now)
		return err
	case reason != "":
		_, err := tx.Exec(ctx, `INSERT INTO app.notification_deliveries (id, notification_id, channel, status, decision, suppression_reason,
			destination_masked, destination_hash, created_at) VALUES ($1, $2, $3, 'SUPPRESSED', $4, $5, $6, $7, $8)`,
			id, notificationID, channel, "SUPPRESSED:"+reason, reason, masked, hash, now)
		return err
	}
	p := s.Active[channel]
	_, err := tx.Exec(ctx, `INSERT INTO app.notification_deliveries (id, notification_id, channel, status, provider_code, decision, destination_masked,
		destination_hash, max_attempts, next_attempt_at, created_at) VALUES ($1, $2, $3, 'QUEUED', $4, 'ROUTED', $5, $6, $7, $8, $8)`,
		id, notificationID, channel, p.Code(), masked, hash, s.settings().MaxAttempts, now)
	return err
}
