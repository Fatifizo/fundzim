# Stage 16 — Trending

Code: `internal/discovery/trending.go` (`Score`, `ComputeTrending`, `Trending`, ranking policies).

## 1. Formula (published on `/campaigns/trending` and in the API response)

```
V = unique visitors over the window (daily unique estimates summed: a returning visitor counts once per day)
S = unique sharers over the window (same)
engagement = view_weight · ln(1 + V) + share_weight · ln(1 + S)
freshness  = 0.5 ^ (hours since publication / freshness_half_life_hours)
score      = engagement · (0.25 + 0.75 · freshness)      (0 when V < min_unique_visitors)
```

Rounded to 6 decimals so storage and recomputation agree. Engineering default (no approved policy):
`view_weight 10, share_weight 25, freshness_half_life_hours 168, window_days 7, min_unique_visitors 3`.

## 2. Inputs that are never used

Money (amounts, donations, goals — there are no real donations and TEST money must never rank anything), referral
counts, click counts, staff choices, payment, any personal or risk data.

## 3. Manipulation controls

| Control | Where |
|---|---|
| Log scaling: 100× the visitors yields far less than 10× the score (unit test) | `Score` |
| Minimum sample (`min_unique_visitors`) | `Score` |
| Unique per visitor per day per campaign; raw events capped at 20 per visitor, campaign, kind and day (excess counted as `VISITOR_CAP`) | Recorder |
| Automated clients (bots, crawlers, link-preview fetchers, scripts, missing user agent) never count | `IsAutomated` |
| Rate limit on events (240/min per address); limited requests counted as `RATE_LIMITED` | `Ingest` |
| Events for non-discoverable campaigns ignored (`NOT_DISCOVERABLE`) | Recorder |
| Only promotable campaigns ranked (`rank_eligible`), re-checked at serve time | `ComputeTrending`, `PublicCards(promotable)` |
| Abuse indicators visible to staff | `/admin/discovery` |

Limits: the visitor key is an address + browser digest; many addresses (botnets, carrier pools) can still inflate V
(KI-S16-03). The log scale and the staff view bound and expose that; there is no device fingerprinting (privacy review
required, brief §22).

## 4. Ranking policies (maker-checker)

`SUBMITTED → ACTIVE | REJECTED | WITHDRAWN`, `ACTIVE → RETIRED` (`app.guard_transition('discovery_ranking_policy')`).
Closed key set and number types (CHECK), bounded integers (Go: weights 0–100, half-life 6–2160 h, window 1–30 days,
minimum 0–1000; both weights zero refused). Content hashed (SHA-256 over the canonical JSON) and immutable after
submission (trigger). Proposer `discovery.ranking.request` + step-up; approver `discovery.ranking.approve` + step-up,
never the proposer or a linked account (Go `ErrSelf`, CHECK, trigger). Approval retires the previous ACTIVE version. None
is seeded. Every decision is audited (`discovery.ranking_policy_*`).

## 5. Computation

Worker job `discovery.trending` every 15 minutes (queue `discovery`): scores every discoverable campaign, checks
promotability in batches of 500, upserts `discovery_trending_scores` (score, components, policy version,
`rank_eligible`) and unranks campaigns that left discovery.
