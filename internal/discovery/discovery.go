// Package discovery is the Stage 16 discovery subsystem (ADR-048): campaign search over its own projection, category and
// location browsing, governed featured placements, explainable trending and recommendations, privacy-preserving
// referral codes and aggregate engagement analytics, the sitemap source and the discovery operations views.
//
// Discovery is never an authority on what may be shown. It owns only app.discovery_* tables; campaign data reaches it
// through the campaigns service. Every card it serves is rendered by campaigns.PublicCards, which re-checks eligibility
// (live, PUBLIC, no compliance restriction; for promotion also ACTIVE and no blocking risk hold) at serve time. A stale
// or wrong projection row can therefore hide a campaign for a while, but can never expose one.
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// ProductionReferralsEnabled gates referral codes and referral-visit counting in production. Referral attribution and
// tracking consent is LR-023 (LEGAL_REVIEW_REQUIRED); until it is answered and an ADR changes this constant, a
// production process refuses referral issuance and ignores referral codes (the flag discovery.referrals.enabled is an
// additional kill switch in every environment).
const ProductionReferralsEnabled = false

// Consumer and job names (dedupe keys; never rename).
const ConsumerIndex = "discovery.index"

// Permissions.
const (
	PermView           = "discovery.view"
	PermFeatureRequest = "discovery.feature.request"
	PermFeatureApprove = "discovery.feature.approve"
	PermFeatureRemove  = "discovery.feature.remove"
	PermRankingRequest = "discovery.ranking.request"
	PermRankingApprove = "discovery.ranking.approve"
	PermOperate        = "discovery.operate"
)

// Campaigns is what discovery needs from the campaigns module (implemented by *campaigns.Service).
type Campaigns interface {
	PublicCards(ctx context.Context, ids []string, promotable bool) ([]campaigns.DiscoveryCard, error)
	Discoverable(ctx context.Context, ids []string, promotable bool) (map[string]bool, error)
	DiscoverySnapshot(ctx context.Context, campaignID string) (campaigns.DiscoveryDoc, error)
	CampaignIDsPage(ctx context.Context, after string, limit int) ([]string, error)
	CampaignIDBySlug(ctx context.Context, slug string) (string, error)
	IDsBySlugs(ctx context.Context, slugs []string) (map[string]string, error)
	CampaignsForSubject(ctx context.Context, subjectType, subjectID string) ([]string, error)
	OwnerAccess(ctx context.Context, userID, campaignID string, write bool) (campaigns.Access, error)
	SlugByID(ctx context.Context, campaignID string) (string, string, error)
	PublicCategories(ctx context.Context, slug string) ([]campaigns.PublicCategory, error)
	Locations(ctx context.Context) ([]campaigns.Location, error)
}

// Identities resolves linked accounts (kyc.Service.ActorIdentities) for maker-checker separation.
type Identities interface {
	ActorIdentities(ctx context.Context, actorID string) ([]string, error)
}

// Flags reads feature flags.
type Flags interface {
	Enabled(ctx context.Context, key string) (bool, error)
}

// Service is the discovery module.
type Service struct {
	Pool       *pgxpool.Pool // fundzim_app (API) or fundzim_worker (jobs)
	Campaigns  Campaigns
	Identities Identities
	Flags      Flags
	Limiter    ratelimit.Limiter
	Keyed      *crypto.Keyed // visitor keys (daily-rotating input)
	Clock      clock.Clock
	Logger     *slog.Logger
	Production bool
	Recorder   *Recorder // in-process engagement buffer (API); nil in the worker

	ref     refCache
	facetMu sync.Mutex
	facets  map[string]facetEntry
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Errors.
var (
	ErrNotFound        = errs.New(errs.NotFound, "NOT_FOUND", "Not found.")
	ErrCampaignMissing = errs.New(errs.NotFound, "CAMPAIGN_NOT_FOUND", "Campaign not found.")
	ErrState           = errs.New(errs.Conflict, "INVALID_STATE", "This record is not in a state that allows that action.")
	ErrSelf            = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot decide on a request you made (or one made by a linked account).")
	ErrNotPromotable   = errs.New(errs.Conflict, "CAMPAIGN_NOT_PROMOTABLE", "Only active, public campaigns without restrictions or holds can be featured.")
	ErrReferralsOff    = errs.New(errs.Unavailable, "REFERRALS_DISABLED", "Referral links are not available.")
	ErrSearchTimeout   = errs.New(errs.Unavailable, "SEARCH_TIMEOUT", "Search took too long. Try a simpler search.")
)

// referralsEnabled: the kill switch, and never in production while LR-023 is open.
func (s *Service) referralsEnabled(ctx context.Context) (bool, error) {
	if s.Production && !ProductionReferralsEnabled {
		return false, nil
	}
	if s.Flags == nil {
		return false, nil
	}
	return s.Flags.Enabled(ctx, "discovery.referrals.enabled")
}

// identities returns the actor and linked accounts (fails closed when the resolver errors).
func (s *Service) identities(ctx context.Context, actorID string) (map[string]bool, error) {
	out := map[string]bool{actorID: true}
	if s.Identities == nil {
		return out, nil
	}
	l, err := s.Identities.ActorIdentities(ctx, actorID)
	if err != nil {
		return nil, err
	}
	for _, id := range l {
		out[id] = true
	}
	return out, nil
}

func isNotFound(err error) bool { return errors.Is(err, campaigns.ErrNotFound) }
