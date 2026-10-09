package campaigns

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// campaignRow is the full campaigns row.
type campaignRow struct {
	ID, PublicCode, Slug, CreatedBy, Category, Title, Summary, Story, GoalCurrency, Status, Visibility, RiskTier, PolicyVersion string
	OwnerUserID, OwnerOrgID, FundraisingBasis, ApprovedVersionID, PendingVersionID, ReReviewReason, CompletionReason            *string
	GoalAmountMinor                                                                                                             int64
	ReReviewRequired                                                                                                            bool
	ResubmissionCount, Version                                                                                                  int
	SubmittedAt, ApprovedAt, PublishedAt, PausedAt, SuspendedAt, CompletedAt, CancelledAt, ArchivedAt                           *time.Time
	CreatedAt, UpdatedAt                                                                                                        time.Time
}

const campaignSelect = `SELECT id, public_code, slug, created_by, category_code, title, summary, story, goal_currency, status, visibility,
	risk_tier, policy_version, owner_user_id, owner_organisation_id, fundraising_basis, approved_version_id, pending_version_id, re_review_reason,
	completion_reason, goal_amount_minor, re_review_required, resubmission_count, version, submitted_at, approved_at, published_at, paused_at,
	suspended_at, completed_at, cancelled_at, archived_at, created_at, updated_at FROM app.campaigns`

func scanCampaign(row pgx.Row) (campaignRow, error) {
	var c campaignRow
	err := row.Scan(&c.ID, &c.PublicCode, &c.Slug, &c.CreatedBy, &c.Category, &c.Title, &c.Summary, &c.Story, &c.GoalCurrency, &c.Status,
		&c.Visibility, &c.RiskTier, &c.PolicyVersion, &c.OwnerUserID, &c.OwnerOrgID, &c.FundraisingBasis, &c.ApprovedVersionID,
		&c.PendingVersionID, &c.ReReviewReason, &c.CompletionReason, &c.GoalAmountMinor, &c.ReReviewRequired, &c.ResubmissionCount,
		&c.Version, &c.SubmittedAt, &c.ApprovedAt, &c.PublishedAt, &c.PausedAt, &c.SuspendedAt, &c.CompletedAt, &c.CancelledAt,
		&c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) loadRow(ctx context.Context, q querier, id string, lock bool) (campaignRow, error) {
	if !validID(id) {
		return campaignRow{}, ErrNotFound
	}
	sql := campaignSelect + ` WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanCampaign(q.QueryRow(ctx, sql, id))
}

func (c campaignRow) access() Access {
	return Access{CampaignID: c.ID, Status: c.Status, Category: c.Category, Visibility: c.Visibility, OwnerUserID: c.OwnerUserID,
		OwnerOrgID: c.OwnerOrgID, CreatedBy: c.CreatedBy, Public: Live(c.Status)}
}

// Money is the JSON money shape: amount_minor as a digit string plus the currency (MONEY.md).
type Money struct {
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// beneficiaryLink is the current campaign ↔ beneficiary link.
type beneficiaryLink struct {
	ID                 string `json:"id"`
	BeneficiaryID      string `json:"beneficiary_id"`
	Disclosure         string `json:"disclosure"`
	ConsentDeclared    bool   `json:"consent_declared"`
	DisplayName        string `json:"display_name,omitempty"`        // owner and staff views only
	BeneficiaryType    string `json:"beneficiary_type,omitempty"`    // owner and staff views only
	VerificationStatus string `json:"verification_status,omitempty"` // owner and staff views only
}

func (s *Service) currentLink(ctx context.Context, q querier, campaignID string) (*beneficiaryLink, error) {
	var l beneficiaryLink
	err := q.QueryRow(ctx, `SELECT id, beneficiary_id, disclosure, consent_declared FROM app.campaign_beneficiaries
		WHERE campaign_id = $1 AND unlinked_at IS NULL`, campaignID).Scan(&l.ID, &l.BeneficiaryID, &l.Disclosure, &l.ConsentDeclared)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &l, err
}
