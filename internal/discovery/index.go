package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Index maintenance (docs/stage-16/search-indexing.md).
//
//   - Event-driven: the outbox consumer discovery.index reindexes a campaign on every campaign lifecycle event that can
//     change what is public (publication, approval of a revision, pause/resume, suspension, reactivation, completion,
//     cancellation, archiving, visibility and beneficiary changes) and on compliance restriction changes for every
//     campaign of the subject.
//   - Reconciliation: a periodic sweep compares the projection with campaigns' eligibility for every published campaign
//     and reindexes drift, and retries rows left dirty by failures.
//   - Serve time: whatever the projection says, every served card is re-checked by campaigns.PublicCards.
//
// Reindex is idempotent and ordered by snapshot time (see Reindex): an older snapshot never overwrites a newer one.

// CampaignEvents are the outbox event types the indexer follows.
var CampaignEvents = []string{"campaigns.published", "campaigns.approved", "campaigns.paused", "campaigns.resumed", "campaigns.suspended",
	"campaigns.reactivated", "campaigns.completed", "campaigns.cancelled", "campaigns.archived", "campaigns.visibility_changed",
	"campaigns.beneficiary_changed"}

// RestrictionEvents are the compliance events that change discoverability of every campaign of a subject.
var RestrictionEvents = []string{"compliance.restriction_applied", "compliance.restriction_lifted"}

// IndexState is the outcome of one reindex.
type IndexState string

// Index outcomes.
const (
	IndexedDiscoverable IndexState = "DISCOVERABLE"
	IndexedRemoved      IndexState = "REMOVED"
)

// Reindex refreshes one campaign's projection row from campaigns. On failure the row is left dirty with an error code
// (and retried by the sweep); a failure never leaves a row discoverable that campaigns did not confirm.
//
// Ordering without locks: the database clock is read before the campaign snapshot, and a write applies only when its
// snapshot time is newer than the stored one. Every change that can affect discoverability emits an event whose reindex
// starts after that change committed, so the newest snapshot — the one that wins — always reflects the latest state.
// (No connection is held while campaigns reads its tables, so a burst of reindexes cannot exhaust the pool.)
func (s *Service) Reindex(ctx context.Context, campaignID string) (IndexState, error) {
	if !ids.Valid(campaignID) {
		return "", ErrCampaignMissing
	}
	var at time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return "", err
	}
	doc, err := s.Campaigns.DiscoverySnapshot(ctx, campaignID)
	if errors.Is(err, campaigns.ErrNotFound) {
		return IndexedRemoved, nil // no such campaign: nothing to index (no row is created for an unknown id)
	}
	if err != nil {
		s.markDirty(ctx, campaignID, errorCode(err))
		return "", err
	}
	const newer = ` WHERE app.discovery_campaign_index.snapshot_at IS NULL OR app.discovery_campaign_index.snapshot_at < EXCLUDED.snapshot_at`
	if !doc.Discoverable {
		_, err = s.Pool.Exec(ctx, `INSERT INTO app.discovery_campaign_index (campaign_id, discoverable, indexed_at, snapshot_at) VALUES ($1, false, $2, $3)
			ON CONFLICT (campaign_id) DO UPDATE SET discoverable = false, slug = '', title = '', summary = '', category_code = NULL,
			  location_code = NULL, province_code = NULL, organiser_type = NULL, organiser_name = '', status = NULL, published_at = NULL,
			  source_version_id = NULL, search_doc = ''::tsvector, title_norm = '', dirty_since = NULL, last_error = NULL, attempts = 0,
			  indexed_at = EXCLUDED.indexed_at, snapshot_at = EXCLUDED.snapshot_at`+newer, campaignID, s.now(), at)
		if err != nil {
			s.markDirty(ctx, campaignID, errorCode(err))
			return "", err
		}
		return IndexedRemoved, nil
	}
	place := strings.TrimSpace(doc.LocationName + " " + doc.ProvinceName)
	if doc.LocationName == doc.ProvinceName {
		place = doc.ProvinceName
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO app.discovery_campaign_index (campaign_id, slug, title, summary, category_code, location_code, province_code,
		  organiser_type, organiser_name, status, published_at, source_version_id, discoverable, search_doc, title_norm, indexed_at, snapshot_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, true,
		  setweight(to_tsvector('simple', $3), 'A') || setweight(to_tsvector('simple', $4), 'B') ||
		  setweight(to_tsvector('simple', $13 || ' ' || $14), 'C') || setweight(to_tsvector('simple', $9), 'D'),
		  lower($3), $15, $16)
		ON CONFLICT (campaign_id) DO UPDATE SET slug = EXCLUDED.slug, title = EXCLUDED.title, summary = EXCLUDED.summary,
		  category_code = EXCLUDED.category_code, location_code = EXCLUDED.location_code, province_code = EXCLUDED.province_code,
		  organiser_type = EXCLUDED.organiser_type, organiser_name = EXCLUDED.organiser_name, status = EXCLUDED.status,
		  published_at = EXCLUDED.published_at, source_version_id = EXCLUDED.source_version_id, discoverable = true,
		  search_doc = EXCLUDED.search_doc, title_norm = EXCLUDED.title_norm, dirty_since = NULL, last_error = NULL, attempts = 0,
		  indexed_at = EXCLUDED.indexed_at, snapshot_at = EXCLUDED.snapshot_at`+newer,
		campaignID, doc.Slug, doc.Title, doc.Summary, doc.Category, doc.LocationCode, doc.ProvinceCode, doc.OrganiserType, doc.OrganiserName,
		doc.Status, doc.PublishedAt, doc.VersionID, doc.CategoryName, place, s.now(), at)
	if err != nil {
		s.markDirty(ctx, campaignID, errorCode(err))
		return "", err
	}
	return IndexedDiscoverable, nil
}

// markDirty records a failed reindex (best effort: the outbox retries the event and the sweep retries dirty rows). An
// existing discoverable row stays as it was — serve-time checks still apply to it. (An unknown id fails the foreign key
// and is only logged.)
func (s *Service) markDirty(ctx context.Context, campaignID, code string) {
	_, err := s.Pool.Exec(ctx, `INSERT INTO app.discovery_campaign_index (campaign_id, discoverable, dirty_since, last_error, attempts)
		VALUES ($1, false, $2, $3, 1)
		ON CONFLICT (campaign_id) DO UPDATE SET dirty_since = coalesce(app.discovery_campaign_index.dirty_since, EXCLUDED.dirty_since),
		  last_error = EXCLUDED.last_error, attempts = app.discovery_campaign_index.attempts + 1`, campaignID, s.now(), code)
	if err != nil {
		s.Logger.WarnContext(ctx, "discovery: could not mark index row dirty", "error_category", "dependency", "error", err.Error())
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, campaigns.ErrEligibilityUnavailable):
		return "ELIGIBILITY_UNAVAILABLE"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "TIMEOUT"
	}
	return "INDEX_FAILED"
}

type indexEvent struct {
	CampaignID string `json:"campaign_id"`
	SubjectT   string `json:"subject_type"`
	SubjectID  string `json:"subject_id"`
}

// HandleCampaignEvent is the outbox handler for campaign events.
func (s *Service) HandleCampaignEvent(ctx context.Context, d outbox.Delivery) error {
	var p indexEvent
	if err := json.Unmarshal(d.Payload, &p); err != nil || !ids.Valid(p.CampaignID) {
		return fmt.Errorf("%s: malformed payload", d.EventType)
	}
	_, err := s.Reindex(ctx, p.CampaignID)
	return err
}

// HandleRestrictionEvent reindexes every campaign of the restricted (or released) subject.
func (s *Service) HandleRestrictionEvent(ctx context.Context, d outbox.Delivery) error {
	var p indexEvent
	if err := json.Unmarshal(d.Payload, &p); err != nil {
		return fmt.Errorf("%s: malformed payload", d.EventType)
	}
	if !ids.Valid(p.SubjectID) {
		return nil
	}
	list, err := s.Campaigns.CampaignsForSubject(ctx, p.SubjectT, p.SubjectID)
	if err != nil {
		return err
	}
	for _, id := range list {
		if _, err := s.Reindex(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Register subscribes the indexer to its events (worker).
func (s *Service) Register(reg *outbox.Registry) {
	for _, ev := range CampaignEvents {
		reg.Subscribe(ConsumerIndex, ev, s.HandleCampaignEvent)
	}
	for _, ev := range RestrictionEvents {
		reg.Subscribe(ConsumerIndex, ev, s.HandleRestrictionEvent)
	}
}

// SweepResult reports a reconciliation pass.
type SweepResult struct {
	Checked, Reindexed, Failed int
}

// Reconcile walks every published campaign in id order (batches of 500), compares campaigns' eligibility with the
// projection and reindexes drift (missing rows, discoverable rows that are no longer eligible and the reverse), then
// retries dirty rows. Bounded per pass by maxCampaigns (0 = all).
func (s *Service) Reconcile(ctx context.Context, maxCampaigns int) (SweepResult, error) {
	var res SweepResult
	after := ""
	for {
		page, err := s.Campaigns.CampaignIDsPage(ctx, after, 500)
		if err != nil {
			return res, err
		}
		if len(page) == 0 {
			break
		}
		after = page[len(page)-1]
		disc, err := s.Campaigns.Discoverable(ctx, page, false)
		if err != nil {
			return res, err
		}
		rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text, discoverable FROM app.discovery_campaign_index WHERE campaign_id = ANY($1::uuid[])`, page)
		if err != nil {
			return res, err
		}
		have := map[string]bool{}
		known := map[string]bool{}
		for rows.Next() {
			var id string
			var d bool
			if err := rows.Scan(&id, &d); err != nil {
				rows.Close()
				return res, err
			}
			known[id], have[id] = true, d
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return res, err
		}
		for _, id := range page {
			res.Checked++
			if known[id] && have[id] == disc[id] {
				continue
			}
			if _, err := s.Reindex(ctx, id); err != nil {
				res.Failed++
				continue
			}
			res.Reindexed++
		}
		if maxCampaigns > 0 && res.Checked >= maxCampaigns {
			break
		}
	}
	// retry rows left dirty by failures
	rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text FROM app.discovery_campaign_index WHERE dirty_since IS NOT NULL ORDER BY dirty_since LIMIT 500`)
	if err != nil {
		return res, err
	}
	dirty, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, err
	}
	for _, id := range dirty {
		if _, err := s.Reindex(ctx, id); err != nil {
			res.Failed++
			continue
		}
		res.Reindexed++
	}
	return res, nil
}
