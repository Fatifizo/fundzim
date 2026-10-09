# ADR-038: Public experience — caching, featured selection, reports, analytics, trust indicators, API consistency

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Technical lead (Stage 7)
- **Stage:** 7

## Context

Stage 7 turns the Stage 6 public read API into a public fundraising experience: home page, discovery, category
pages, sharing, SEO, PWA, reporting and analytics. Several choices affect safety: stale caches could keep a
suspended campaign visible; "featured" could imply endorsement or popularity; reports could be abused to take
campaigns down; analytics could collect personal data; trust badges could overstate what verification means.
Stage 6 also left API inconsistencies (KI-S6-08/09/10).

## Decision

1. **Caching (public data must disappear immediately when a campaign leaves public view).**
   - Campaign detail, campaign lists, featured lists, updates and sitemap data: `Cache-Control: public, no-cache`
     with a strong `ETag` (clients and shared caches must revalidate every time; a 304 is cheap). Public images:
     `public, no-cache` + ETag as well (served only while the campaign is public).
   - Categories and currencies: `public, max-age=300` (configuration, not campaign state).
   - The web app's server rendering fetches campaign data with `cache: 'no-store'`. There is no server-side
     shared cache of campaign state, so there is nothing to invalidate on suspension, restriction, disclosure or
     publication changes. This trades some latency for correctness; ETag revalidation keeps it cheap.
   - Private responses keep `private, no-store`.

2. **Featured campaigns.** No popularity, ranking or paid promotion. Selection is transparent and reported in the
   response: first **staff picks** (`app.campaign_featured`, ordered by position, added/removed by staff with
   `campaign.feature`, audited), then **recently published** live `PUBLIC` campaigns to fill the list. Each item says
   which (`selection: STAFF_PICK | RECENTLY_PUBLISHED`). A staff pick never overrides visibility: a pick whose
   campaign is not live and `PUBLIC` is skipped.

3. **Reports (`POST /public/campaigns/{slug}/reports`).** Anyone may report a public campaign (anonymous or signed
   in). Reports are stored (`app.campaign_reports`: reason code, plain-text details, optional contact email kept only
   as an HMAC blind index plus an encrypted value for follow-up — never shown to the owner), rate limited per IP and
   per campaign, deduplicated per reporter per campaign per day, and placed in a staff triage queue
   (`campaign.review`). Staff may dismiss or escalate (opens a compliance case through the outbox). **A report never
   changes a campaign's status by itself.**

4. **Analytics foundation.** First-party only, no third-party scripts, no cookies, no IP or user id stored.
   `POST /public/events` increments daily aggregate counters (`app.public_event_counters`: day, event type, subject
   slug or category, share channel). Search events record only that a search happened (and whether it had a
   category), never the query text. Counters are internal (no public metric is shown; no engagement is fabricated).

5. **Trust indicators** are computed live for the public view and shown only when true *now*:
   `organiser_identity_verified` (owner `IDENTITY_VERIFIED`, profile `ACTIVE`, not expired),
   `organisation_verified` (`ORG_KYB_VERIFIED`, `ACTIVE`), `beneficiary_verified` (beneficiary `APPROVED`; a SELF
   beneficiary counts when the organiser is identity verified), `campaign_reviewed` (an approved version exists).
   Copy explains what they mean and do **not** mean (no guarantee of claims). No other badges.

6. **Canonical URLs.** The slug is fixed at first publication. If a request's slug does not match but its trailing
   public code identifies a public campaign, the API returns the campaign with its canonical slug and the web app
   permanently redirects. `/c/{public_code}` is a short link that redirects to the canonical URL.

7. **API consistency (Stage 6 campaign family: campaigns, media, updates, featured, reports).**
   - Path parameter for a campaign is always `{campaign_id}`.
   - Optimistic concurrency: `If-Match` missing or unparsable → **428 `PRECONDITION_REQUIRED`**; stale →
     **412 `PRECONDITION_FAILED`**. State races not caused by `If-Match` stay 409.
   - Lists return `data: {items: [...], next_cursor: string|null}` (keyset cursors).
   - Validation is always 422 `VALIDATION_FAILED` with field details; gated actions 422 `NOT_ELIGIBLE`.
   - Public errors use the standard envelope; anything not public is 404 `CAMPAIGN_NOT_FOUND`.
   The Stage 5 verification and compliance APIs keep their behaviour (documented in their contracts) to avoid
   churn in reviewed workflows; aligning them is a recorded follow-up.

## Consequences

- Public pages cost one revalidation round trip per request; measured in Stage 7 performance docs.
- The web client and OpenAPI change with the consistency rules; compatibility notes are in the Stage 7 contracts.

## Related

ADR-033, ADR-036, ADR-037; docs/stage-7/interface-contracts.md.
