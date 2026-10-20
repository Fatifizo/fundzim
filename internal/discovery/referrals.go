package discovery

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// Referral codes (docs/stage-16/referrals.md). A code says only "this campaign was shared through this channel": no
// user, device, address or cookie is linked to it, it grants nothing (no session, no discount, no money meaning) and it
// expires. Visits carrying a valid code are counted per channel in aggregate. A donation is never attributed to a code
// (attribution never reveals an individual donor's path; there are no real donations in any case).

// ReferralTTL is how long a code is counted (engineering default; LR-023 may change it).
const ReferralTTL = 90 * 24 * time.Hour

const codeAlphabet = "0123456789abcdefghjkmnpqrstvwxyz" // Crockford base32, lower case (no i, l, o, u)

func newCode() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b), nil
}

// ReferralInput is POST /discovery/referrals.
type ReferralInput struct {
	Slug    string `json:"slug"`
	Channel string `json:"channel"`
}

// Referral is an issued code.
type Referral struct {
	Code      string    `json:"code"`
	Channel   string    `json:"channel"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IssueReferral creates a code for a discoverable campaign and channel. Refused when referrals are disabled (always in
// production while LR-023 is open).
func (s *Service) IssueReferral(ctx context.Context, in ReferralInput) (Referral, error) {
	if !ShareChannels[in.Channel] {
		return Referral{}, httpx.Validation(errs.Detail{Field: "channel", Code: "INVALID_VALUE"})
	}
	if !slugRe.MatchString(in.Slug) || len(in.Slug) > 96 {
		return Referral{}, httpx.Validation(errs.Detail{Field: "slug", Code: "INVALID_FORMAT"})
	}
	on, err := s.referralsEnabled(ctx)
	if err != nil {
		return Referral{}, err
	}
	if !on {
		return Referral{}, ErrReferralsOff
	}
	if s.Limiter != nil {
		d, err := s.Limiter.Allow(ctx, ratelimit.DefaultPolicies().DiscoveryReferralIP, clientKey(ctx))
		if err != nil {
			return Referral{}, err
		}
		if !d.Allowed {
			if s.Recorder != nil {
				s.Recorder.Abuse(s.now(), "RATE_LIMITED", "", 1)
			}
			return Referral{}, rateLimited{after: d.RetryAfter}
		}
	}
	id, err := s.Campaigns.CampaignIDBySlug(ctx, in.Slug)
	if err != nil {
		if isNotFound(err) {
			return Referral{}, ErrCampaignMissing
		}
		return Referral{}, err
	}
	disc, err := s.Campaigns.Discoverable(ctx, []string{id}, false)
	if err != nil {
		return Referral{}, err
	}
	if !disc[id] {
		return Referral{}, ErrCampaignMissing
	}
	now := s.now()
	r := Referral{Channel: in.Channel, ExpiresAt: now.Add(ReferralTTL)}
	for attempt := 0; attempt < 5; attempt++ {
		if r.Code, err = newCode(); err != nil {
			return Referral{}, err
		}
		_, err = s.Pool.Exec(ctx, `INSERT INTO app.discovery_referral_tokens (id, code, campaign_id, channel, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), r.Code, id, in.Channel, now, r.ExpiresAt)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			continue // 60-bit collision: draw again
		}
		return r, err
	}
	return Referral{}, errors.New("discovery: could not allocate a referral code")
}

type refToken struct {
	campaignID, channel string
	expiresAt           time.Time
	revoked             bool
}

func lookupRefs(ctx context.Context, pool *pgxpool.Pool, codes []string) (map[string]refToken, error) {
	rows, err := pool.Query(ctx, `SELECT code, campaign_id::text, channel, expires_at, revoked_at IS NOT NULL FROM app.discovery_referral_tokens
		WHERE code = ANY($1)`, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]refToken{}
	for rows.Next() {
		var c string
		var t refToken
		if err := rows.Scan(&c, &t.campaignID, &t.channel, &t.expiresAt, &t.revoked); err != nil {
			return nil, err
		}
		out[c] = t
	}
	return out, rows.Err()
}

// RevokeReferral revokes a code (abuse). Audited.
func (s *Service) RevokeReferral(ctx context.Context, staffID, code, reason string) error {
	if !refCodeRe.MatchString(code) {
		return ErrNotFound
	}
	r, err := cleanReason(reason, 3, 500)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `UPDATE app.discovery_referral_tokens SET revoked_at = $2, revoked_by = $3, revoked_reason = $4
			WHERE code = $1 AND revoked_at IS NULL RETURNING id::text`, code, s.now(), staffID, r).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "discovery.referral_revoked", ActorType: "staff", ActorID: staffID,
			TargetType: "discovery_referral_token", TargetID: id, Reason: r})
	})
}

// ---- small helpers -----------------------------------------------------------------------------------------------

func itoa(n int) string { return strconv.Itoa(n) }

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
