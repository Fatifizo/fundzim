package campaigns

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the campaigns module's contribution to the operations portal (Stage 14, ADR-046): review backlog,
// second approvals waiting, open public reports and update moderation, from its own tables. Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "campaigns"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var queued, inReview, escalated, second, reports, updates int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.campaign_reviews WHERE status IN ('QUEUED','ASSIGNED')),
		(SELECT count(*) FROM app.campaign_reviews WHERE status = 'IN_REVIEW'),
		(SELECT count(*) FROM app.campaign_reviews WHERE status IN ('QUEUED','ASSIGNED','IN_REVIEW') AND escalated),
		(SELECT count(*) FROM app.campaign_reviews WHERE status IN ('QUEUED','ASSIGNED','IN_REVIEW') AND pending_outcome IS NOT NULL),
		(SELECT count(*) FROM app.campaign_reports WHERE status = 'OPEN'),
		(SELECT count(*) FROM app.campaign_updates WHERE status = 'PENDING_MODERATION')`).
		Scan(&queued, &inReview, &escalated, &second, &reports, &updates); err != nil {
		return snap, err
	}
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "campaigns.reviews.pending", "Campaign reviews waiting (queued or assigned)", queued, "campaign.review",
			"app.campaign_reviews QUEUED/ASSIGNED", "/admin/campaigns/review"),
		opsview.Count(m, "campaigns.reviews.in_review", "Campaign reviews in progress", inReview, "campaign.review", "app.campaign_reviews IN_REVIEW",
			"/admin/campaigns/review"),
		opsview.Count(m, "campaigns.reviews.escalated", "Escalated campaign reviews", escalated, "campaign.review", "open app.campaign_reviews with escalated",
			"/admin/campaigns/review"),
		opsview.Count(m, "campaigns.reviews.second_approval", "Campaign decisions awaiting a second approval", second, "campaign.decide.high",
			"open app.campaign_reviews with a pending outcome", "/admin/campaigns/review"),
		opsview.Count(m, "campaigns.reports.open", "Open public campaign reports", reports, "campaign.review", "app.campaign_reports OPEN",
			"/admin/campaigns/reports"),
		opsview.Count(m, "campaigns.updates.pending", "Campaign updates awaiting moderation", updates, "content.moderate",
			"app.campaign_updates PENDING_MODERATION", "/admin/campaigns/updates"),
	)
	rows, err := s.Pool.Query(ctx, `SELECT r.id::text, r.campaign_id::text, coalesce(c.title, ''), coalesce(r.pending_decided_by::text, ''),
		coalesce(r.pending_decided_at, r.updated_at), r.status, r.risk_tier, coalesce(r.pending_outcome, '')
		FROM app.campaign_reviews r JOIN app.campaigns c ON c.id = r.campaign_id
		WHERE r.status IN ('QUEUED','ASSIGNED','IN_REVIEW') AND r.pending_outcome IS NOT NULL ORDER BY r.pending_decided_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a opsview.Approval
		var campaign, title, outcome string
		if err := rows.Scan(&a.SourceRecordID, &campaign, &title, &a.RequestedBy, &a.RequestedAt, &a.Status, &a.RiskLevel, &outcome); err != nil {
			return snap, err
		}
		a.ID, a.SourceModule, a.Action = m+":review:"+a.SourceRecordID, m, "CAMPAIGN_SECOND_APPROVAL"
		a.Summary, a.RequiredPermission = "Second approval: "+outcome+" — "+title, "campaign.decide.high"
		a.Makers, a.Href = []string{a.RequestedBy}, "/admin/campaigns/review/"+campaign
		snap.Approvals = append(snap.Approvals, a)
	}
	return snap, rows.Err()
}

// OpsSearch finds campaigns by id, slug or title (staff with campaign.view).
func (s *Service) OpsSearch(ctx context.Context, q string) ([]opsview.SearchHit, error) {
	items, _, err := s.AdminList(ctx, "", q, "", 20)
	if err != nil {
		return nil, err
	}
	out := []opsview.SearchHit{}
	for _, c := range items {
		out = append(out, opsview.SearchHit{Kind: "CAMPAIGN", ID: c.ID, Label: c.Title, Status: c.Status, Permission: "campaign.view",
			Href: "/admin/campaigns/review/" + c.ID})
	}
	return out, nil
}
