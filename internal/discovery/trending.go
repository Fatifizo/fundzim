package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Trending (docs/stage-16/trending.md). A deterministic, explainable score from first-party, deduplicated, bot-filtered
// engagement only: unique visitors and unique sharers per day over a window, plus publication freshness. Money is never
// an input (no real donations exist; TEST donations must never rank anything). Signals are log-scaled so that
// inflating one of them yields sharply diminishing returns; a minimum number of unique visitors keeps tiny samples out.

// Weights is a ranking policy's content (closed key set, mirrored by ck_discovery_ranking_policies_weights).
type Weights struct {
	ViewWeight             int `json:"view_weight"`               // 0–100: weight of ln(1 + unique visitors)
	ShareWeight            int `json:"share_weight"`              // 0–100: weight of ln(1 + unique sharers)
	FreshnessHalfLifeHours int `json:"freshness_half_life_hours"` // 6–2160: age at which the freshness factor halves
	WindowDays             int `json:"window_days"`               // 1–30: engagement window
	MinUniqueVisitors      int `json:"min_unique_visitors"`       // 0–1000: below this a campaign is not ranked as trending
}

// DefaultWeights is the engineering default used while no ranking policy has been approved (shown as such everywhere).
var DefaultWeights = Weights{ViewWeight: 10, ShareWeight: 25, FreshnessHalfLifeHours: 168, WindowDays: 7, MinUniqueVisitors: 3}

// Validate checks the bounds.
func (w Weights) Validate() error {
	var det []errs.Detail
	check := func(f string, v, lo, hi int) {
		if v < lo || v > hi {
			det = append(det, errs.Detail{Field: f, Code: "OUT_OF_RANGE"})
		}
	}
	check("view_weight", w.ViewWeight, 0, 100)
	check("share_weight", w.ShareWeight, 0, 100)
	check("freshness_half_life_hours", w.FreshnessHalfLifeHours, 6, 2160)
	check("window_days", w.WindowDays, 1, 30)
	check("min_unique_visitors", w.MinUniqueVisitors, 0, 1000)
	if w.ViewWeight == 0 && w.ShareWeight == 0 {
		det = append(det, errs.Detail{Field: "view_weight", Code: "ALL_ZERO"})
	}
	if len(det) > 0 {
		return httpx.Validation(det...)
	}
	return nil
}

// Signals are one campaign's inputs.
type Signals struct {
	UniqueVisitors int64
	UniqueSharers  int64
	AgeHours       float64
}

// Components explain a score.
type Components struct {
	UniqueVisitors int64   `json:"unique_visitors"`
	UniqueSharers  int64   `json:"unique_sharers"`
	AgeHours       int64   `json:"age_hours"`
	Engagement     float64 `json:"engagement"`
	Freshness      float64 `json:"freshness"`
	BelowMinimum   bool    `json:"below_minimum"`
}

// Score is the trending formula (pure; rounded to 6 decimals so storage and recomputation agree):
//
//	engagement = view_weight·ln(1+V) + share_weight·ln(1+S)
//	freshness  = 0.5^(age_hours / half_life)
//	score      = engagement·(0.25 + 0.75·freshness)   (0 when V < min_unique_visitors)
func Score(sig Signals, w Weights) (float64, Components) {
	c := Components{UniqueVisitors: sig.UniqueVisitors, UniqueSharers: sig.UniqueSharers, AgeHours: int64(math.Max(0, sig.AgeHours))}
	age := math.Max(0, sig.AgeHours)
	c.Engagement = round6(float64(w.ViewWeight)*math.Log1p(float64(max(0, sig.UniqueVisitors))) +
		float64(w.ShareWeight)*math.Log1p(float64(max(0, sig.UniqueSharers))))
	c.Freshness = round6(math.Pow(0.5, age/float64(w.FreshnessHalfLifeHours)))
	if sig.UniqueVisitors < int64(w.MinUniqueVisitors) {
		c.BelowMinimum = true
		return 0, c
	}
	return round6(c.Engagement * (0.25 + 0.75*c.Freshness)), c
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// activeWeights returns the approved policy in force, or the engineering default (version nil).
func (s *Service) activeWeights(ctx context.Context) (Weights, *int, error) {
	var raw []byte
	var v int
	err := s.Pool.QueryRow(ctx, `SELECT weights, policy_version FROM app.discovery_ranking_policies WHERE status = 'ACTIVE'`).Scan(&raw, &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultWeights, nil, nil
	}
	if err != nil {
		return Weights{}, nil, err
	}
	var w Weights
	if err := json.Unmarshal(raw, &w); err != nil {
		return Weights{}, nil, err
	}
	return w, &v, w.Validate()
}

// TrendingResult reports a computation.
type TrendingResult struct {
	Scored, Ranked int
	PolicyVersion  *int
}

// ComputeTrending recomputes every discoverable campaign's score (worker job). rank_eligible additionally needs the
// campaign to be promotable now (re-checked again at serve time).
func (s *Service) ComputeTrending(ctx context.Context) (TrendingResult, error) {
	var res TrendingResult
	w, ver, err := s.activeWeights(ctx)
	if err != nil {
		return res, err
	}
	res.PolicyVersion = ver
	now := s.now()
	from := now.AddDate(0, 0, -w.WindowDays).Format("2006-01-02")
	rows, err := s.Pool.Query(ctx, `SELECT i.campaign_id::text, i.published_at,
		  coalesce(sum(d.count) FILTER (WHERE d.metric = 'VIEW_UNIQUE'), 0), coalesce(sum(d.count) FILTER (WHERE d.metric = 'SHARE_UNIQUE'), 0)
		FROM app.discovery_campaign_index i
		LEFT JOIN app.discovery_campaign_daily d ON d.campaign_id = i.campaign_id AND d.day >= $1::date AND d.metric IN ('VIEW_UNIQUE','SHARE_UNIQUE')
		WHERE i.discoverable GROUP BY i.campaign_id, i.published_at`, from)
	if err != nil {
		return res, err
	}
	type item struct {
		id   string
		sc   float64
		comp Components
	}
	var items []item
	for rows.Next() {
		var id string
		var pub time.Time
		var v, sh int64
		if err := rows.Scan(&id, &pub, &v, &sh); err != nil {
			rows.Close()
			return res, err
		}
		sc, comp := Score(Signals{UniqueVisitors: v, UniqueSharers: sh, AgeHours: now.Sub(pub).Hours()}, w)
		items = append(items, item{id, sc, comp})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	// promotable now, in batches
	prom := map[string]bool{}
	for i := 0; i < len(items); i += 500 {
		end := min(i+500, len(items))
		batch := make([]string, 0, end-i)
		for _, it := range items[i:end] {
			if it.sc > 0 {
				batch = append(batch, it.id)
			}
		}
		if len(batch) == 0 {
			continue
		}
		m, err := s.Campaigns.Discoverable(ctx, batch, true)
		if err != nil {
			return res, err
		}
		for k, v := range m {
			prom[k] = v
		}
	}
	idl, scores, comps, elig := make([]string, len(items)), make([]string, len(items)), make([][]byte, len(items)), make([]bool, len(items))
	for i, it := range items {
		idl[i], scores[i], elig[i] = it.id, strconv.FormatFloat(it.sc, 'f', 6, 64), it.sc > 0 && prom[it.id]
		comps[i], _ = json.Marshal(it.comp)
		if elig[i] {
			res.Ranked++
		}
	}
	res.Scored = len(items)
	_, err = s.Pool.Exec(ctx, `INSERT INTO app.discovery_trending_scores (campaign_id, score, components, policy_version, rank_eligible, computed_at)
		SELECT c::uuid, sc::numeric, co::jsonb, $5, e, $6 FROM unnest($1::text[], $2::text[], $3::text[], $4::bool[]) AS u(c, sc, co, e)
		ON CONFLICT (campaign_id) DO UPDATE SET score = EXCLUDED.score, components = EXCLUDED.components, policy_version = EXCLUDED.policy_version,
		  rank_eligible = EXCLUDED.rank_eligible, computed_at = EXCLUDED.computed_at`, idl, scores, bytesToStrings(comps), elig, ver, now)
	if err != nil {
		return res, err
	}
	// campaigns that left discovery keep their row but are no longer ranked
	_, err = s.Pool.Exec(ctx, `UPDATE app.discovery_trending_scores t SET rank_eligible = false, computed_at = $1
		WHERE t.rank_eligible AND NOT EXISTS (SELECT 1 FROM app.discovery_campaign_index i WHERE i.campaign_id = t.campaign_id AND i.discoverable)`, now)
	return res, err
}

func bytesToStrings(b [][]byte) []string {
	out := make([]string, len(b))
	for i, x := range b {
		out[i] = string(x)
	}
	return out
}

// Trending returns the top trending promotable campaigns with their explanation.
func (s *Service) Trending(ctx context.Context, category string, limit int) ([]Card, error) {
	if limit <= 0 || limit > 24 {
		limit = 12
	}
	args := []any{limit * 2}
	where := ""
	if category != "" {
		ref, err := s.reference(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, ref.categoryCode(category))
		where = ` AND i.category_code = $2`
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.campaign_id::text, t.components FROM app.discovery_trending_scores t
		JOIN app.discovery_campaign_index i ON i.campaign_id = t.campaign_id AND i.discoverable
		WHERE t.rank_eligible`+where+` ORDER BY t.score DESC, i.published_at DESC, t.campaign_id DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	var idl []string
	comps := map[string]Components{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var c Components
		_ = json.Unmarshal(raw, &c)
		idl, comps[id] = append(idl, id), c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cards, err := s.Campaigns.PublicCards(ctx, idl, true)
	if err != nil {
		return nil, err
	}
	out := []Card{}
	for _, c := range cards {
		if len(out) == limit {
			break
		}
		r := reason(ReasonTrending)
		out = append(out, Card{DiscoveryCard: c, Reason: &r})
		s.recordImpression(c.ID, "TRENDING")
	}
	return out, nil
}

// ---- ranking policies (maker-checker) ------------------------------------------------------------------------------

// RankingPolicy is a version of the trending weights.
type RankingPolicy struct {
	ID           string     `json:"id"`
	Version      int        `json:"policy_version"`
	Weights      Weights    `json:"weights"`
	SHA256       string     `json:"content_sha256"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason"`
	ProposedBy   string     `json:"proposed_by"`
	ProposedAt   time.Time  `json:"proposed_at"`
	DecidedBy    *string    `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote *string    `json:"decision_note"`
	RetiredAt    *time.Time `json:"retired_at"`
}

// RankingView is GET /admin/discovery/ranking.
type RankingView struct {
	InForce        Weights         `json:"in_force"`
	InForceVersion *int            `json:"in_force_version"` // null: engineering default
	InForceSource  string          `json:"in_force_source"`  // APPROVED_POLICY | ENGINEERING_DEFAULT
	Default        Weights         `json:"engineering_default"`
	Policies       []RankingPolicy `json:"policies"`
}

// Ranking lists the policies (newest first) and what is in force.
func (s *Service) Ranking(ctx context.Context) (RankingView, error) {
	w, v, err := s.activeWeights(ctx)
	if err != nil {
		return RankingView{}, err
	}
	out := RankingView{InForce: w, InForceVersion: v, InForceSource: "ENGINEERING_DEFAULT", Default: DefaultWeights, Policies: []RankingPolicy{}}
	if v != nil {
		out.InForceSource = "APPROVED_POLICY"
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text FROM app.discovery_ranking_policies ORDER BY policy_version DESC LIMIT 50`)
	if err != nil {
		return out, err
	}
	idl, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return out, err
	}
	for _, id := range idl {
		p, err := s.rankingPolicy(ctx, id)
		if err != nil {
			return out, err
		}
		out.Policies = append(out.Policies, p)
	}
	return out, nil
}

func (s *Service) rankingPolicy(ctx context.Context, id string) (RankingPolicy, error) {
	var p RankingPolicy
	var raw, sum []byte
	err := s.Pool.QueryRow(ctx, `SELECT id::text, policy_version, weights, content_sha256, status, reason, proposed_by::text, proposed_at, decided_by::text,
		decided_at, decision_note, retired_at FROM app.discovery_ranking_policies WHERE id = $1`, id).Scan(&p.ID, &p.Version, &raw, &sum, &p.Status,
		&p.Reason, &p.ProposedBy, &p.ProposedAt, &p.DecidedBy, &p.DecidedAt, &p.DecisionNote, &p.RetiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(raw, &p.Weights)
	p.SHA256 = hexString(sum)
	return p, nil
}

// ProposeRanking submits a new policy version (maker). Content is hashed and immutable.
func (s *Service) ProposeRanking(ctx context.Context, staffID string, w Weights, reason string) (RankingPolicy, error) {
	if err := w.Validate(); err != nil {
		return RankingPolicy{}, err
	}
	r, err := cleanReason(reason, 10, 1000)
	if err != nil {
		return RankingPolicy{}, err
	}
	canon, _ := json.Marshal(w) // fixed field order (struct): canonical
	sum := sha256.Sum256(canon)
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE app.discovery_ranking_policies IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.discovery_ranking_policies (id, policy_version, weights, content_sha256, status, reason, proposed_by, proposed_at)
			SELECT $1, coalesce(max(policy_version), 0) + 1, $2, $3, 'SUBMITTED', $4, $5, $6 FROM app.discovery_ranking_policies`,
			id, canon, sum[:], r, staffID, s.now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "discovery.ranking_policy_proposed", ActorType: "staff", ActorID: staffID,
			TargetType: "discovery_ranking_policy", TargetID: id, Reason: r, Metadata: map[string]any{"content_sha256": hexString(sum[:])}})
	})
	if err != nil {
		return RankingPolicy{}, err
	}
	return s.rankingPolicy(ctx, id)
}

// DecideRanking approves (activates; the previous ACTIVE version retires) or rejects a SUBMITTED version (checker: never
// the proposer or a linked account — Go and database), or withdraws it (the proposer only).
func (s *Service) DecideRanking(ctx context.Context, staffID, id, action, note string) (RankingPolicy, error) {
	if !ids.Valid(id) {
		return RankingPolicy{}, ErrNotFound
	}
	n, err := cleanReason(note, 3, 1000)
	if err != nil {
		return RankingPolicy{}, err
	}
	idents, err := s.identities(ctx, staffID)
	if err != nil {
		return RankingPolicy{}, err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status, proposer string
		if err := tx.QueryRow(ctx, `SELECT status, proposed_by::text FROM app.discovery_ranking_policies WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &proposer); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if status != "SUBMITTED" {
			return ErrState
		}
		now := s.now()
		switch action {
		case "withdraw":
			if proposer != staffID {
				return ErrNotFound
			}
			if _, err := tx.Exec(ctx, `UPDATE app.discovery_ranking_policies SET status = 'WITHDRAWN', version = version + 1 WHERE id = $1`, id); err != nil {
				return err
			}
		case "approve", "reject":
			if idents[proposer] {
				return ErrSelf
			}
			to := "REJECTED"
			if action == "approve" {
				to = "ACTIVE"
				if _, err := tx.Exec(ctx, `UPDATE app.discovery_ranking_policies SET status = 'RETIRED', retired_at = $1, version = version + 1
					WHERE status = 'ACTIVE'`, now); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE app.discovery_ranking_policies SET status = $2, decided_by = $3, decided_at = $4, decision_note = $5,
				version = version + 1 WHERE id = $1`, id, to, staffID, now, n); err != nil {
				return mapCheck(err)
			}
		default:
			return httpx.Validation(errs.Detail{Field: "action", Code: "INVALID_VALUE"})
		}
		return audit.Record(ctx, tx, audit.Event{Action: "discovery.ranking_policy_" + map[string]string{"approve": "approved", "reject": "rejected",
			"withdraw": "withdrawn"}[action], ActorType: "staff", ActorID: staffID, TargetType: "discovery_ranking_policy", TargetID: id, Reason: n})
	})
	if err != nil {
		return RankingPolicy{}, err
	}
	return s.rankingPolicy(ctx, id)
}
