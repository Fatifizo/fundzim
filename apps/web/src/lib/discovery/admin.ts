/**
 * Staff discovery read models (GET /admin/discovery/*): aggregates only — discovery holds no personal data. Read
 * defensively: unknown shapes become empty values.
 */
type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, f = ""): string => (typeof v === "string" ? v : f);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const int = (v: unknown): number => (typeof v === "number" && Number.isInteger(v) && v >= 0 ? v : 0);
const counts = (v: unknown): Record<string, number> => {
  const o: Record<string, number> = {};
  if (isRec(v)) for (const [k, x] of Object.entries(v)) if (/^[A-Z_a-z]{1,40}$/.test(k)) o[k] = int(x);
  return o;
};

export interface Weights {
  view_weight: number;
  share_weight: number;
  freshness_half_life_hours: number;
  window_days: number;
  min_unique_visitors: number;
}

export const WEIGHT_FIELDS: ReadonlyArray<{ key: keyof Weights; label: string; min: number; max: number; hint: string }> = [
  { key: "view_weight", label: "Weight of unique visitors", min: 0, max: 100, hint: "Multiplies ln(1 + unique visitors)." },
  { key: "share_weight", label: "Weight of unique sharers", min: 0, max: 100, hint: "Multiplies ln(1 + unique sharers)." },
  { key: "freshness_half_life_hours", label: "Freshness half-life (hours)", min: 6, max: 2160, hint: "Age at which the freshness factor halves." },
  { key: "window_days", label: "Engagement window (days)", min: 1, max: 30, hint: "How many days of engagement count." },
  { key: "min_unique_visitors", label: "Minimum unique visitors", min: 0, max: 1000, hint: "Campaigns below this are not ranked." },
];

function readWeights(v: unknown): Weights {
  const w = isRec(v) ? v : {};
  return { view_weight: int(w.view_weight), share_weight: int(w.share_weight), freshness_half_life_hours: int(w.freshness_half_life_hours), window_days: int(w.window_days), min_unique_visitors: int(w.min_unique_visitors) };
}

export interface Overview {
  index: { discoverable: number; notDiscoverable: number; backlog: number; oldestBacklogAt: string | null; failing: number; lastIndexedAt: string | null; status: string; trendingAt: string | null; trendingRanked: number };
  search: Record<string, number>;
  searchSorts: Record<string, number>;
  engagement: Record<string, number>;
  referrals: Record<string, number>;
  shares: Record<string, number>;
  abuse: Record<string, number>;
  featured: { live: number; requested: number; scheduled: number };
  trending: Array<{ campaignId: string; slug: string; title: string; score: string; uniqueVisitors: number; uniqueSharers: number; ageHours: number }>;
  ranking: { source: string; version: number | null; pending: number; inForce: Weights };
  referralCodes: { enabled: boolean; active: number; revoked: number; expired: number };
  definitions: Record<string, string>;
}

export function readOverview(v: unknown): Overview {
  const d = isRec(v) ? v : {};
  const ix = isRec(d.index) ? d.index : {};
  const f = isRec(d.featured) ? d.featured : {};
  const rk = isRec(d.ranking) ? d.ranking : {};
  const rc = isRec(d.referral_codes) ? d.referral_codes : {};
  const defs: Record<string, string> = {};
  if (isRec(d.definitions)) for (const [k, x] of Object.entries(d.definitions)) if (typeof x === "string") defs[k] = x;
  return {
    index: { discoverable: int(ix.discoverable), notDiscoverable: int(ix.not_discoverable), backlog: int(ix.backlog), oldestBacklogAt: strOrNull(ix.oldest_backlog_at), failing: int(ix.failing), lastIndexedAt: strOrNull(ix.last_indexed_at), status: str(ix.status, "UNKNOWN"), trendingAt: strOrNull(ix.trending_computed_at), trendingRanked: int(ix.trending_ranked) },
    search: counts(d.search_7d_by_outcome),
    searchSorts: counts(d.search_7d_by_sort),
    engagement: counts(d.engagement_7d),
    referrals: counts(d.referral_visits_7d_by_channel),
    shares: counts(d.shares_7d_by_channel),
    abuse: counts(d.abuse_7d_by_kind),
    featured: { live: int(f.live), requested: int(f.pending_requests), scheduled: int(f.scheduled) },
    trending: Array.isArray(d.trending_top)
      ? d.trending_top.filter(isRec).map((t) => {
          const c = isRec(t.components) ? t.components : {};
          return { campaignId: str(t.campaign_id), slug: str(t.slug), title: str(t.title), score: str(t.score), uniqueVisitors: int(c.unique_visitors), uniqueSharers: int(c.unique_sharers), ageHours: int(c.age_hours) };
        })
      : [],
    ranking: { source: str(rk.source), version: typeof rk.version === "number" ? rk.version : null, pending: int(rk.pending), inForce: readWeights(rk.in_force) },
    referralCodes: { enabled: rc.enabled === true, active: int(rc.active), revoked: int(rc.revoked), expired: int(rc.expired) },
    definitions: defs,
  };
}

export interface Placement {
  id: string;
  campaignId: string;
  campaignSlug: string;
  campaignTitle: string;
  position: number;
  reason: string;
  startsAt: string;
  endsAt: string;
  status: string;
  requestedBy: string;
  requestedAt: string;
  decidedBy: string | null;
  decidedAt: string | null;
  decisionNote: string | null;
  removedAt: string | null;
  removalReason: string | null;
  live: boolean;
  promotable: boolean | null;
}

export function readPlacements(v: unknown): Placement[] {
  const items = isRec(v) && Array.isArray(v.items) ? v.items : Array.isArray(v) ? v : [];
  return items.filter(isRec).filter((p) => typeof p.id === "string").map((p) => ({
    id: p.id as string,
    campaignId: str(p.campaign_id),
    campaignSlug: str(p.campaign_slug),
    campaignTitle: str(p.campaign_title),
    position: int(p.position),
    reason: str(p.reason),
    startsAt: str(p.starts_at),
    endsAt: str(p.ends_at),
    status: str(p.status),
    requestedBy: str(p.requested_by),
    requestedAt: str(p.requested_at),
    decidedBy: strOrNull(p.decided_by),
    decidedAt: strOrNull(p.decided_at),
    decisionNote: strOrNull(p.decision_note),
    removedAt: strOrNull(p.removed_at),
    removalReason: strOrNull(p.removal_reason),
    live: p.live === true,
    promotable: typeof p.promotable === "boolean" ? p.promotable : null,
  }));
}

export interface RankingPolicy {
  id: string;
  version: number;
  weights: Weights;
  sha256: string;
  status: string;
  reason: string;
  proposedBy: string;
  proposedAt: string;
  decidedBy: string | null;
  decisionNote: string | null;
}

export interface RankingView {
  inForce: Weights;
  inForceVersion: number | null;
  inForceSource: string;
  engineeringDefault: Weights;
  policies: RankingPolicy[];
}

export function readRanking(v: unknown): RankingView {
  const d = isRec(v) ? v : {};
  return {
    inForce: readWeights(d.in_force),
    inForceVersion: typeof d.in_force_version === "number" ? d.in_force_version : null,
    inForceSource: str(d.in_force_source),
    engineeringDefault: readWeights(d.engineering_default),
    policies: Array.isArray(d.policies)
      ? d.policies.filter(isRec).filter((p) => typeof p.id === "string").map((p) => ({
          id: p.id as string,
          version: int(p.policy_version),
          weights: readWeights(p.weights),
          sha256: str(p.content_sha256),
          status: str(p.status),
          reason: str(p.reason),
          proposedBy: str(p.proposed_by),
          proposedAt: str(p.proposed_at),
          decidedBy: strOrNull(p.decided_by),
          decisionNote: strOrNull(p.decision_note),
        }))
      : [],
  };
}
