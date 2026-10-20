# Stage 16 prerequisite assessment

Date: 2026-10-19. Branch `stage-16/discovery`, created from `stage-15/notifications` at `558825d`. Inputs read:
`CLAUDE.md`, `README.md`, `docs/PRODUCT.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`, `docs/ROADMAP.md` (Stage 16),
[STAGE-15-TO-STAGE-16.md](../stage-handover/STAGE-15-TO-STAGE-16.md), Stage 15 known issues and testing, the acceptance
register, ADR-036/037/038/045/046/047, `internal/campaigns` (public read paths, staff picks, counters, restrictions,
holds), `internal/compliance/restrictions.go`, `internal/risk/holds.go`, the outbox and job platform, the web app's
public pages, sharing, SEO and analytics code, the mock and real-stack E2E suites, OpenAPI and CI.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**.

## 1. Stage 15 status (brief §3)

| Item | Status | Evidence |
|---|---|---|
| Stage 15 engineering complete | SATISFIED | `docs/stage-15/implementation.md`; head `558825d` |
| Stage 15 remote CI | SATISFIED (observed) | GitHub Actions `ci` on `558825d` (stage-15/notifications): **completed, success** at 2026-10-19T10:21:08Z (read from the public Actions API this session). Earlier runs on `04c024f`, `e0f1e49`, `c3d6559` failed and were fixed by later commits |
| Stage 15 acceptance | **NOT_VERIFIED** (not recorded) | register row 15 `NOT_RECORDED`; nothing is marked accepted in Stage 16 |
| KI-S15-19 (unbounded TEST payout resubmission) | SATISFIED — **remediated in the Stage 15 finalisation** | KI-S15-19 = KI-S11-20: `PAYOUTS_SUBMIT_RETRY_BUDGET`, jittered backoff, SEV2 `SUBMISSION_RETRIES_EXHAUSTED` exception halting dispatcher and attempts guard until an operator resolves it; tests `TestPayoutSubmitRetryBudgetHaltsAndResumes`, `TestPayoutHistoricalExcessiveAttemptsHaltedWithoutAnotherCall`; historical development payouts halted. Stage 16 does not touch payout state machines |
| Other Stage 15 open items | tracked | KI-S15-02 (no messaging provider), KI-S15-03/04 (consent wording, marketing off, LR-023), KI-S15-05 (retention, LR-012), KI-S15-12 (send-time consent fault path code-reviewed only), KI-S15-20 (communications report at scale) — carried into [known-issues.md](known-issues.md) where relevant |

## 2. Governance (brief §4)

| Item | Status | Evidence |
|---|---|---|
| Acceptance of Stages 0–15 | **NOT_VERIFIED** | every register row `NOT_RECORDED`; Stage 9 PARTIAL; 10–15 PASS WITH CONDITIONS recommended |
| Production financial operations | BLOCKED (unchanged) | `psp.LiveExecutionEnabled`, `payouts.LiveExecutionEnabled`, `ledger.ProductionPostingEnabled`, `fees.ProductionFeesEnabled` all `false`; production CHECKs unchanged |
| Live messaging, marketing | BLOCKED (unchanged) | `notifications.LiveMessagingEnabled = false`, `MarketingEnabled = false` |
| Production screening | BLOCKED (unchanged) | `risk.ScreeningProductionEnabled = false` |
| Referral attribution in production | BLOCKED (new) | `discovery.ProductionReferralsEnabled = false` (LR-023) |

## 3. Legal and privacy dependencies

| ID | Effect on Stage 16 |
|---|---|
| LR-023 (marketing, WhatsApp Business, "referral attribution and tracking consent") | Referral codes are anonymous (campaign + channel only) and attribution is aggregate, but the question is open: production issuance and counting are blocked in code; development keeps them behind the kill switch `discovery.referrals.enabled`. No WhatsApp Business integration and no automated message: sharing is user-initiated `wa.me` links. **LEGAL_REVIEW_REQUIRED** |
| LR-035 (lawful basis incl. analytics) | Engagement analytics are aggregate, first-party, cookie-less and honour Do Not Track / GPC; the basis is not determined. **LEGAL_REVIEW_REQUIRED** |
| LR-012 (retention) | Aggregates have no coded retention (nothing purged). Per-visitor de-duplication keys (keyed hashes rotating daily) are deleted after two days — a minimisation choice, not a legal retention period; to confirm. **LEGAL_REVIEW_REQUIRED** |
| LR-010 / LR-011 (controller registration, cross-border) | No third-party analytics, search, preview or messaging vendor is used; everything is first-party. Nothing new to transfer |
| LR-022 (terms, cookie notice) | No cookies are set by discovery; the cookie notice question is unchanged |
| LR-070 (minors) / new LR-090 (public location of a cause) | Location is coarse (province or provincial capital) and reviewed; whether some categories or minors require province-only is open. **LEGAL_REVIEW_REQUIRED** |

No Zimbabwean rule is assumed. Province names and capitals come from a cited source
(https://en.wikipedia.org/wiki/Provinces_of_Zimbabwe, accessed 2026-10-19).

## 4. Technical prerequisites (what exists and is reused — brief §2)

| Prerequisite | Status | Evidence / reuse |
|---|---|---|
| Campaign lifecycle, approved versions, publication rules | SATISFIED | `internal/campaigns` (ADR-036); discovery reads only through `campaigns.Service` |
| Public read API, canonical slugs, short links, ETag caching | SATISFIED | Stage 7 `/public/*` (ADR-038); kept; the listing and sitemap endpoints now filter restricted campaigns too |
| Public directory `/campaigns`, `/explore` redirect, `/categories/*` | SATISFIED_WITH_CONDITIONS | Existing pages are extended (no competing directory); `/campaigns/search` and `/campaigns/category/[slug]` are permanent redirects to them |
| Categories (taxonomy, descriptions, sensitive flag, staff edits) | SATISFIED | `app.campaign_categories` (11 seeded in Stage 6); used as is — no new categories invented. `discovery_category_metadata` was not needed |
| Campaign location | **missing → built** | no location field existed (handover §2); added as reviewed content with a reference list (`app.campaign_locations`) |
| Compliance restrictions | SATISFIED_WITH_CONDITIONS | `compliance.Service.Restrictions` (batched); suspension of affected campaigns is **asynchronous** — discovery and the public page now re-check at serve time |
| Risk hold registry | SATISFIED | `risk.Service.BlockingHolds` (batched); used for promotion eligibility |
| Search | SATISFIED_WITH_CONDITIONS | Stage 7 `search_tsv` on versions (title + summary, no ranking, no typo tolerance); Stage 16 adds a ranked projection with `pg_trgm` |
| Featured | SATISFIED_WITH_CONDITIONS | Stage 7 single-actor staff picks; replaced by maker-checker placements (ADR-048 amends ADR-038 §2) |
| Sharing | SATISFIED | `ShareControls` (WhatsApp, Facebook, X, e-mail, copy, Web Share); extended with LinkedIn and referral codes |
| SEO (metadata, sitemap, robots, JSON-LD) | SATISFIED | Stage 7; extended (noindex for listing variants, provinces in the sitemap, breadcrumbs, generated preview image) |
| Analytics | SATISFIED_WITH_CONDITIONS | Stage 7 daily counters by slug (no dedupe, no bot filtering); replaced for the web app by the discovery pipeline; `/public/events` kept for compatibility |
| Outbox, River queues, consumer queue routing | SATISFIED | ADR-025; `outbox.Relay.ConsumerQueues` (Stage 15) — discovery runs on its own `discovery` queue |
| Operations portal sources | SATISFIED | `opsview` snapshots (ADR-046) — discovery adds one |
| RBAC, step-up, idempotency, rate limiting, audit | SATISFIED | platform; seven `discovery.*` permissions, three rate-limit policies |
| Performance harness | SATISFIED | `tests/perf` (Stage 7) — extended with `TestDiscoveryPerf` on a 100 000-campaign scratch database |
| Web vitals harness | SATISFIED | `apps/web/scripts/cwv.mjs` (Stage 7) — Stage 16 pages added |
| CI | SATISFIED | `.github/workflows/ci.yml`; new tests run in the existing jobs; perf harness vetted |

## 5. Conclusion

Stage 16 can proceed as non-production engineering. Referral attribution and analytics are built privacy-first and
blocked or flagged where legal answers are missing; no financial path is touched.
