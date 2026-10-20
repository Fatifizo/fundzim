# Stage 16 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S16-01 | WhatsApp (and other platforms') link-preview rendering is **not verified with real clients**: the development stack has no public HTTPS URL that WhatsApp's servers can fetch. Open Graph tags, the generated preview image and crawler access are asserted in automated tests only | roadmap acceptance criterion "Link previews render correctly in WhatsApp" not evidenced | OPEN (needs a public staging URL; Stage 18/20) |
| KI-S16-02 | External platforms cache previews for their own durations; a campaign suspended after being shared can still show an old preview until the platform refreshes it. FundZim stops serving the page and image at once but cannot purge external caches | stale previews outside FundZim | ACCEPTED (documented; manual refresh tools exist per platform) |
| KI-S16-03 | Trending and analytics can be inflated by many distinct addresses (botnets, carrier address pools); the visitor key is address + browser digest, there is no device fingerprinting (privacy review required) | bounded by log scaling, minimum sample, per-visitor caps, bot filter, rate limits; visible in abuse indicators | ACCEPTED (monitor; revisit with field data) |
| KI-S16-04 | No retention period for engagement aggregates, search counters, abuse counters, referral tokens or trending scores; nothing is purged except two-day de-duplication keys (a minimisation choice, not a legal period) | storage growth; legal confirmation needed | LEGAL_REVIEW_REQUIRED (LR-012, LR-035) |
| KI-S16-05 | Index reconciliation compares only the discoverable flag, not content; content drift (e.g. a missed `campaigns.approved` event) is repaired only by the next event for that campaign | search may match on the previous approved text until then; never exposes a non-public campaign | ACCEPTED (events are transactional outbox, at least once) |
| KI-S16-06 | Location list holds the 10 provinces and their capitals only; no districts, other towns, wards or causes outside Zimbabwe; codes are FundZim's own (ISO 3166-2 not verified) | coarse filtering | OPEN (content owner; LR-090) |
| KI-S16-07 | Referral attribution is blocked in production in code until LR-023 is answered; the copy in the share panel is engineering wording | production organisers would see no referral channels | EXTERNAL_BLOCKER (LR-023) |
| KI-S16-08 | The Stage 7 counters (`POST /public/events`, `app.public_event_counters`) remain for compatibility though the web app no longer uses them; the Stage 7 table `app.campaign_featured` and permission `campaign.feature` remain as history | dead endpoint and permission | OPEN (remove in Stage 18 clean-up) |
| KI-S16-09 | Search runs on PostgreSQL in the primary database; measured inside the 2 s timeout at 100 000 campaigns, with one scenario marginally over its 300 ms p95 budget ([mobile-performance.md](mobile-performance.md) §5, KI-S16-16) but no read replica or dedicated search store exists | load on the primary at scale | ACCEPTED (revisit with production traffic data) |
| KI-S16-10 | Analytics buffers live in each API process; a crash or forced restart loses at most one flush interval (2 s) of engagement events; multiple replicas each flush their own buffer | approximate analytics | ACCEPTED (by design) |
| KI-S16-11 | Category and location counts come from the projection and can include a campaign restricted since its last reindex until the consumer runs and are cached in process for 30 s (listings themselves re-check) | a count can be one or two higher for up to about a minute | ACCEPTED |
| KI-S16-12 | English only for discovery copy, reasons and share text; no Shona or Ndebele (as KI-S15-06) | language accessibility | OPEN (content owner) |
| KI-S16-13 | Manual screen-reader and keyboard audit of the new pages not performed; automated axe (WCAG 2.x A/AA serious/critical) runs on every new page on desktop and Pixel 7 and in the real stack | WCAG 2.2 AA not certified | OPEN (as Stage 7 condition) |
| KI-S16-14 | Field performance (real phones and networks) is not measured: lab Core Web Vitals only; no real-user monitoring exists | budgets verified in the lab only | OPEN (Stage 18/20) |
| KI-S16-15 | Stage 0–15 acceptance not recorded; Stage 13/14/15 external blockers remain (screening provider, messaging provider, SLA targets) | governance | EXTERNAL_BLOCKER (owner) |
| KI-S16-16 | At 53 952 discoverable campaigns, searches for very common words cost ~100–280 ms p95 and the mixed 20-query scenario 310 ms p95 (budget 300): the full-text match fetches every matching row before the bounded relevance window. Inside the 2 s timeout; not a correctness issue | slower search for common words at scale | OPEN (options: a newest-first partial index per category, a dedicated search store, a read replica; decide with production data) |

## Stage 15 items revisited

| ID | Status in Stage 16 |
|---|---|
| KI-S15-19 (unbounded TEST payout resubmission) | FIXED in the Stage 15 finalisation (verified in [prerequisite-assessment.md](prerequisite-assessment.md) §1); untouched by Stage 16 |
| KI-S15-04 (marketing disabled) | unchanged: discovery sends no message; sharing is user-initiated links |
| KI-S15-06 (English only) | carried as KI-S16-12 |
