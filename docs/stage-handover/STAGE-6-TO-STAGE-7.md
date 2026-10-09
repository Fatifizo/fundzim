# Stage 6 → Stage 7 Handover: Public Fundraising Experience

> Stage 6 (campaign engine, lifecycle, moderation, eligibility, publishing, and the Stage 5 remediation) is
> implemented on branch `stage-6/campaigns` and **awaits acceptance**. Do not start Stage 7 until the owner accepts
> Stage 6 and asks for Stage 7. Stage 1–5 acceptance is still unrecorded (KI-S6-23).

Stage 6 docs: [implementation](../stage-6/implementation.md) · [lifecycle](../stage-6/campaign-lifecycle.md) ·
[eligibility](../stage-6/campaign-eligibility.md) · [moderation](../stage-6/campaign-moderation.md) ·
[media](../stage-6/campaign-media.md) · [security](../stage-6/campaign-security.md) · [testing](../stage-6/testing.md) ·
[known issues](../stage-6/known-issues.md) · [contracts](../stage-6/interface-contracts.md). ADRs: 036, 037.

## 1. Public APIs available

| Endpoint | Returns | Notes |
|---|---|---|
| `GET /api/v1/public/campaigns?q=&category=&sort=published|created&cursor=&limit=` | `{items: [{slug, title, summary, category, goal, status, cover_media_id, published_at, created_at}], next_cursor}` | live (`ACTIVE`/`PAUSED`/`COMPLETED`) and `PUBLIC` only; approved snapshots; keyset cursor; `Cache-Control: public, max-age=30` |
| `GET /api/v1/public/campaigns/{slug}` | approved snapshot + `organiser {type, display_name}`, `beneficiary {disclosed, display_name?}`, `cover_media_id`, `media_ids`, `donations {available: false, message}` | 404 for anything not live (`UNLISTED` reachable by link) |
| `GET /api/v1/public/campaigns/{slug}/updates` | published updates, newest first, cursor | organiser name only |
| `GET /api/v1/public/campaigns/{slug}/media/{media_id}` | processed image bytes | `public, max-age=300`, ETag, nosniff, sandbox CSP |
| `GET /api/v1/campaign-categories`, `GET /api/v1/campaign-currencies` | active categories; usable currencies `{code, minor_units, display_symbol}` | cacheable |

Stage 7 must keep the rules: public pages render **approved snapshots only**; nothing private (owner/beneficiary/
reviewer ids, KYC, risk, compliance, notes, documents, payout details) is ever exposed; non-public slugs are
indistinguishable from unknown ones.

## 2. Search and categories

Search is PostgreSQL full-text (`simple` configuration) over approved title + summary with a GIN index on
`campaign_versions`; there is no stemming or language support (KI-S6-18). Ranking, recommendations and trending are
Stage 16. Categories are configuration (11 seeded, provisional tiers); only presentation fields change through the
admin API.

## 3. Campaign page architecture (as built)

`apps/web` `/campaigns` (search, filter, sort, pagination) and `/campaigns/[slug]` (cover, escaped story paragraphs,
category, goal formatted from minor units, organiser, beneficiary disclosure, paused/completed notices, updates,
gallery, share controls, disabled donation button). Images are plain `<img>` from same-origin API paths because the
strict nonce CSP blocks `next/image` inline styles (ADR-033). Open Graph absolute URLs are emitted only when
`PUBLIC_SITE_URL` is configured.

## 4. Inputs and decisions for Stage 7

| Topic | Input / decision needed |
|---|---|
| Sharing | Copy link + Web Share exist; no third-party scripts. Decide on share images (Open Graph image = approved cover) and short links (`public_code`) |
| SEO | `noindex` on dashboard/admin; public pages indexable. Decide sitemap generation (live `PUBLIC` campaigns only), canonical URLs (`PUBLIC_SITE_URL`), structured data (no monetary claims until donations exist) |
| Mobile-first and low bandwidth | Measure page weight on 3G; image sizes (derivatives are re-encoded originals — add resized variants?); caching headers are in place |
| Accessibility | axe checks run in E2E; alt text is required for media (3–250 chars); keep reading order and contrast |
| Privacy | Medical campaigns are sensitive (pre-moderated updates); minors' media refused and names never disclosed (LR-070, PD-39); beneficiary names shown only with declared consent and review; health detail redaction is a manual review check |
| Future donation experience | The disabled CTA and `donations.available = false` are the placeholder. Totals must come from the ledger per currency (MONEY §8, campaign-schema §5), never from campaign fields. No progress bars, totals or "raised" text until then |
| Payment integration boundaries | Licensed PSP collects and settles (ADR-013, Model A); no wallet or stored value; no FX; payment confirmation only from verified webhooks/status APIs. Campaign `ACTIVE` ≠ donation-eligible: a separate donation eligibility (currency rail available, restrictions, holds) is needed |
| Performance | Stage 6 sample (tiny dataset): search p50 1.9 ms / p95 5 ms; set budgets and test at realistic volume; consider a read model for listings |

## 5. Open items to schedule

KI-S6-02 (BASIC re-grant), KI-S6-08/09/10 (API consistency), KI-S6-11 (unhide updates), KI-S6-14 (end dates and
expiry), KI-S6-16 (policy admin API), KI-S6-20 (load-sensitive worker test), KI-S6-21 (review check results);
legal: LR-019, LR-021, LR-043, LR-046 – LR-048, LR-070.
