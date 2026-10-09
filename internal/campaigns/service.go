// Package campaigns is the campaign engine (Stage 6, ADR-036): ownership, content, beneficiary association,
// eligibility, review and moderation, controlled publication and public read. It holds no financial state:
// there are no raised, total or balance values anywhere in this module (CLAUDE.md financial rules 5 and 11);
// the goal is an exact amount_minor + currency. Media (package media) and owner updates (package updates)
// build on the access API here; the core reaches them only through the interfaces below.
package campaigns

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/beneficiaries"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Statuses (ADR-036 §1). Brief names: PENDING_REVIEW = SUBMITTED, PUBLISHED = ACTIVE.
const (
	StatusDraft            = "DRAFT"
	StatusSubmitted        = "SUBMITTED"
	StatusUnderReview      = "UNDER_REVIEW"
	StatusChangesRequested = "CHANGES_REQUESTED"
	StatusApproved         = "APPROVED"
	StatusActive           = "ACTIVE"
	StatusPaused           = "PAUSED"
	StatusSuspended        = "SUSPENDED"
	StatusRejected         = "REJECTED"
	StatusCompleted        = "COMPLETED"
	StatusCancelled        = "CANCELLED"
	StatusArchived         = "ARCHIVED"
)

// Restriction levels (ADR-037 §3), highest wins.
const (
	RestrictionNone       = ""
	RestrictionRestricted = "RESTRICTED"
	RestrictionSuspended  = "SUSPENDED"
	RestrictionOffboarded = "OFFBOARDED"
)

var (
	ErrNotFound      = errs.New(errs.NotFound, "CAMPAIGN_NOT_FOUND", "No such campaign.")
	ErrInvalidStatus = errs.New(errs.Conflict, "INVALID_STATUS", "This action is not allowed in the campaign's current status.")
	ErrStateChanged  = errs.New(errs.Conflict, "CAMPAIGN_STATE_CHANGED", "The campaign changed in the meantime. Reload and try again.")
	ErrSelfDecision  = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot review a campaign that concerns you or your organisation.")
	ErrNotAssigned   = errs.New(errs.Conflict, "NOT_ASSIGNED", "Assign the review to yourself first.")
)

// Live reports whether a status is publicly exposed (ADR-036 §1).
func Live(status string) bool {
	return status == StatusActive || status == StatusPaused || status == StatusCompleted
}

// Access is the authorisation view of a campaign used by the media and updates packages.
type Access struct {
	CampaignID  string
	Status      string
	Category    string
	Visibility  string
	OwnerUserID *string
	OwnerOrgID  *string
	CreatedBy   string
	Public      bool // exposed publicly (live status); only PUBLIC visibility is listed or searchable
}

// Subject identifies a party for restriction checks.
type Subject struct{ Type, ID string }

// Restrictions reads compliance restrictions (implemented over compliance.Service in internal/app).
type Restrictions interface {
	Levels(ctx context.Context, subjects []Subject) (map[Subject]string, error)
}

// AgeChecker reads the age-attestation state (implemented over kyc in internal/app).
type AgeChecker interface {
	// Attested reports whether the user's current self-attestation meets the policy age, and whether a verified
	// date of birth contradicts it.
	Attested(ctx context.Context, userID string) (attested, contradicted bool, err error)
}

// Flags reads feature flags (platform featureflags).
type Flags interface {
	Enabled(ctx context.Context, key string) (bool, error)
}

// MediaReadiness summarises a campaign's media for gating.
type MediaReadiness struct {
	CoverApproved bool `json:"cover_approved"`
	Pending       int  `json:"pending"`
	Rejected      int  `json:"rejected"`
}

// MediaInfo is implemented by package media and injected (nil = no media: never ready).
type MediaInfo interface {
	Readiness(ctx context.Context, campaignID string) (MediaReadiness, error)
	ApprovedIDs(ctx context.Context, campaignID string) ([]string, error)
}

// Deps are the collaborators of the campaigns module.
type Deps struct {
	Pool          *pgxpool.Pool // fundzim_app
	KYC           *kyc.Service
	Beneficiaries *beneficiaries.Service
	Orgs          *organisations.Service
	Restrictions  Restrictions
	Age           AgeChecker
	Flags         Flags
	// StaffCan reports whether an active staff account holds a permission (assignee eligibility); nil refuses
	// assigning anyone but the caller.
	StaffCan func(ctx context.Context, staffID, permission string) (bool, error)
	Clock    clock.Clock
	Logger   *slog.Logger
}

// Service is the campaigns module.
type Service struct {
	Deps
	media MediaInfo
}

// New builds the service.
func New(d Deps) *Service { return &Service{Deps: d} }

// SetMedia injects the media package (composition root).
func (s *Service) SetMedia(m MediaInfo) { s.media = m }

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

const accessSelect = `SELECT id, status, category_code, visibility, owner_user_id, owner_organisation_id, created_by FROM app.campaigns`

func scanAccess(row pgx.Row) (Access, error) {
	var a Access
	err := row.Scan(&a.CampaignID, &a.Status, &a.Category, &a.Visibility, &a.OwnerUserID, &a.OwnerOrgID, &a.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Public = Live(a.Status)
	return a, err
}

// Load returns any campaign (staff paths; the caller authorises).
func (s *Service) Load(ctx context.Context, campaignID string) (Access, error) {
	if !validID(campaignID) {
		return Access{}, ErrNotFound
	}
	return scanAccess(s.Pool.QueryRow(ctx, accessSelect+` WHERE id = $1`, campaignID))
}

// OwnerAccess authorises a personal user: read = the owner or any member of the owning organisation; write =
// the owner or an ORG_ADMIN. Everyone else gets ErrNotFound (existence is not revealed).
func (s *Service) OwnerAccess(ctx context.Context, userID, campaignID string, write bool) (Access, error) {
	a, err := s.Load(ctx, campaignID)
	if err != nil {
		return a, err
	}
	ok, err := s.canAccess(ctx, userID, a, write)
	if err != nil {
		return Access{}, err
	}
	if !ok {
		return Access{}, ErrNotFound
	}
	return a, nil
}

func (s *Service) canAccess(ctx context.Context, userID string, a Access, write bool) (bool, error) {
	if a.OwnerUserID != nil {
		return *a.OwnerUserID == userID, nil
	}
	role, err := s.Orgs.MemberRole(ctx, *a.OwnerOrgID, userID)
	if err != nil {
		return false, err
	}
	if write {
		return role == organisations.RoleAdmin, nil
	}
	return role != "", nil
}

// PublicBySlug returns a publicly exposed campaign; anything else is ErrNotFound.
func (s *Service) PublicBySlug(ctx context.Context, slug string) (Access, error) {
	if !validSlug(slug) {
		return Access{}, ErrNotFound
	}
	a, err := scanAccess(s.Pool.QueryRow(ctx, accessSelect+` WHERE slug = $1`, slug))
	if err != nil {
		return Access{}, err
	}
	if !a.Public {
		return Access{}, ErrNotFound
	}
	return a, nil
}

// Restricted returns the highest compliance restriction on the campaign, its owner, owning organisation and
// current beneficiary. Fails closed: an error means the caller must refuse the action.
func (s *Service) Restricted(ctx context.Context, a Access) (string, error) {
	if s.Restrictions == nil {
		return "", errors.New("campaigns: restrictions reader not configured")
	}
	subjects := []Subject{{Type: "CAMPAIGN", ID: a.CampaignID}}
	if a.OwnerUserID != nil {
		subjects = append(subjects, Subject{Type: "USER", ID: *a.OwnerUserID})
	}
	if a.OwnerOrgID != nil {
		subjects = append(subjects, Subject{Type: "ORGANISATION", ID: *a.OwnerOrgID})
	}
	var benef *string
	if err := s.Pool.QueryRow(ctx, `SELECT beneficiary_id FROM app.campaign_beneficiaries WHERE campaign_id = $1 AND unlinked_at IS NULL`,
		a.CampaignID).Scan(&benef); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if benef != nil {
		subjects = append(subjects, Subject{Type: "BENEFICIARY", ID: *benef})
	}
	levels, err := s.Restrictions.Levels(ctx, subjects)
	if err != nil {
		return "", err
	}
	return highest(levels), nil
}

func highest(levels map[Subject]string) string {
	rank := map[string]int{RestrictionNone: 0, RestrictionRestricted: 1, RestrictionSuspended: 2, RestrictionOffboarded: 3}
	best := RestrictionNone
	for _, l := range levels {
		if rank[l] > rank[best] {
			best = l
		}
	}
	return best
}
