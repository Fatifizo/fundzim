# Stage 15 → Stage 16 handover (Discovery, Sharing & WhatsApp Optimisation)

Status of Stage 15: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 15
NOT_RECORDED). Stage 16 must not start until the owner asks for it. This document prepares it; it implements nothing.

## 1. What Stage 16 inherits

From Stage 7 (public experience, ADR-038):

| Area | Already built |
|---|---|
| Discovery | `/explore` (search and filters), `/categories` and `/categories/[slug]`, public campaign pages `/campaigns/[slug]`, short links `/c/[code]` |
| SEO | `sitemap.ts`, `robots.ts`, per-page metadata (`lib/seo/metadata.ts`) with Open Graph image when a campaign has a cover image; public data `public, no-cache` + ETag |
| Sharing | `ShareControls`: WhatsApp, Facebook, X and e-mail links, copy link, Web Share API |
| Analytics | first-party aggregate counters only (`CAMPAIGN_PAGE_VIEW`, `CAMPAIGN_SHARE_CLICK` with channel, `CAMPAIGN_SEARCH`, `CATEGORY_VIEW`); no per-person tracking |
| Abuse | public reports with a staff triage queue; rate-limited public endpoints |

From Stage 15 (this stage):

- A notification engine with **no WhatsApp provider**: `whatsapp_simulator` only, template-only outbound, no
  free-form messages, `APPROVED` unreachable without provider evidence ([whatsapp.md](../stage-15/whatsapp.md)).
- Consent records per channel and purpose; marketing disabled (`MarketingEnabled = false`). Referral or promotional
  messages are **marketing** and stay impossible until LR-023 is answered.
- `CAMPAIGN_*` notices to owners (approved, published, paused, …) in-app and e-mail (development senders only).
- All notification work on the River `notifications` queue (`outbox.Relay.ConsumerQueues`): new Stage 16 consumers that
  could fan out widely (e.g. share analytics) should follow the same pattern rather than share the default queue.
- Payout resubmission after "definitely not sent" outcomes is budgeted (KI-S11-20); Stage 16 does not touch payouts.

## 2. Scope to settle in Stage 16 (from the brief §88)

| Topic | Starting point / open question |
|---|---|
| Discovery and search | `/explore` exists; relevance ranking, typo tolerance and low-bandwidth result pages are open. Ranking signals must not be gameable (views/share counts are aggregate and can be inflated) |
| Recommendations, featured, trending | none exists; any "trending" needs abuse controls and a documented, explainable rule — no hidden promotion |
| Categories | exist; curation and empty-category handling |
| Location | campaigns have no location field today; adding one (e.g. province) needs a privacy decision so beneficiaries' precise locations are never exposed |
| Social sharing / WhatsApp share links | `wa.me` links exist; message text, link previews and short-link behaviour on low-end phones need testing. **Share links need no provider and no consent; sending WhatsApp messages does** |
| Open Graph metadata and previews | OG image only when a cover exists; generated preview images (title, progress without money claims, TEST labels) are open; WhatsApp preview rendering must be verified with real clients, not assumed |
| Share analytics and referral tracking | only aggregate counters today. Referral attribution must not reveal individual donor paths (roadmap acceptance) and its consent basis is LR-023 ("referral attribution and tracking consent") — LEGAL_REVIEW_REQUIRED |
| SEO | sitemap and metadata exist; structured data must not claim verification or tax status that is not established |
| Mobile performance and low bandwidth | measure first (Lighthouse/WebPageTest on throttled profiles); image sizes, font loading, JS budget |
| Privacy | no third-party trackers or pixels have been added; keep it that way unless a privacy review approves one |
| Abuse prevention | share-count and view inflation, scraping, link spam via short links |

## 3. Constraints carried forward

- Money remains TEST; no page may present TEST donations as real, and progress figures follow the money-state
  rules (no "raised" figure from unsettled funds).
- No WhatsApp Business integration without an ADR, provider selection and legal review (roadmap: optional,
  separate ADR). Stage 15's simulator is not a provider.
- No marketing or referral messages until LR-023 and a marketing consent design are approved.
- Public endpoints keep the Stage 7 caching and error rules (ADR-038).

## 4. Open blockers relevant to Stage 16

| ID | Item |
|---|---|
| LR-023 | consent for marketing, WhatsApp Business messaging, referral attribution and tracking |
| LR-010 / LR-011 | controller registration and cross-border transfer — any third-party preview, analytics or messaging vendor |
| PCR (WhatsApp) | business account, template policy, messaging limits — only if a WhatsApp Business integration is proposed |
| KI-S15-02 | no messaging provider selected |
