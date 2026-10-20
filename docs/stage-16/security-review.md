# Stage 16 — Security and privacy review

Scope: `internal/discovery`, the campaigns changes (location, eligibility, public-page restriction check, retired staff
picks), migrations `20261020090000` and `20261020090100`, the web discovery pages, share controls, preview route,
analytics client, staff discovery pages, OpenAPI. Method: threat-by-threat review against the code and the tests that
prove each control; findings fixed during the stage are listed in §4.

## 1. Threats and controls

| Threat | Control | Proof |
|---|---|---|
| Restricted, suspended, draft or unlisted campaign exposed through search, lists, sitemap, previews or recommendations | candidates from the projection, cards and eligibility from campaigns at serve time; projection CHECK clears removed rows; sitemap re-checked; preview route re-fetches the public view; public page hides SUSPENDED/OFFBOARDED restrictions before the async suspension | `TestDiscoveryEndToEnd` (restriction: absent from search, sitemap, legacy listing and recommendations immediately; suspension; deliberately stale row not exposed), E2E preview 404 |
| Stale index bypasses moderation | as above; reconciliation repairs drift | stale-row test |
| Eligibility registry outage → unfiltered results | fail closed: 503 `DISCOVERY_UNAVAILABLE`, `no-store`; reindex leaves the row dirty, never discoverable | `TestDiscoveryFailureInjectionAndAbuse` |
| SQL injection via search | terms are letters/digits only; tsquery built from terms; all values bound parameters; filters from closed sets or reference lookups; cursors validated (base64url, sort tag, timestamp, UUID, number formats) | `TestNormalizeQuery`, `TestTSQueryHasNoOperatorsFromInput`, `TestSearchPlanValidation`, `TestCursorRoundTripAndTampering`, integration injection query |
| XSS through campaign text, highlights or JSON-LD | React escapes all text; highlights are text segments that must rebuild the text exactly (else ignored); JSON-LD escapes `< > &` U+2028/2029; preview image renders text as text | vitest `Highlighted` markup test, JSON-LD `</script>` test |
| Open redirect | no redirector added; `/campaigns/search` and `/campaigns/category/*` redirect to fixed internal paths built from validated values; share links point at the canonical page | E2E alias test (`sort=bogus` dropped) |
| Referral token manipulation | opaque random codes; validated format; must exist, be unexpired, unrevoked and belong to the viewed campaign; invalid codes counted as abuse, never as referrals; no privileges | `TestReferralCodes`, end-to-end revocation and wrong-campaign behaviour |
| Artificial trending / share inflation | log scale, minimum sample, per-visitor daily cap, bot filter, rate limits, promotable-only ranking, staff-visible abuse indicators | `TestScoreIsBoundedDeterministicAndExplainable`, visitor-cap and concurrency tests |
| Scraping / query amplification | 120 req/min per address on public reads (generous for carrier NAT), 48-item pages, 2 s statement timeout, capped counts, keyset pagination, prefix only on the last term ≥ 3 characters, fuzzy fallback one page only | rate-limit test (429 + Retry-After) |
| Unauthorised promotion | maker-checker placements (permission split, step-up, idempotency, Go self-check, CHECK, linked-identity trigger), window ≤ 31 days, promotable-only, removal by a third permission, audit | integration (DB refuses `decided_by = requested_by`), E2E maker-checker |
| Unauthorised ranking change | maker-checker ranking policies, closed bounded weights, hashed immutable content, audit | `TestDiscoveryGovernanceAndRBAC` (immutability, bounds, self-approval, permissions) |
| Cross-tenant analytics access | owner analytics authorised by `campaigns.OwnerAccess` (owner or organisation member); others 404; staff views aggregate only | integration (other user 404, anonymous 401), E2E stranger 404 |
| Personal data in analytics | no IP, user id, user agent, cookie, referrer or query text stored; daily keyed hashes for dedupe deleted after 2 days; addresses held in memory ≤ one flush | integration "no identifying data in aggregates"; schema |
| Covert tracking / fingerprinting | none: first-party, cookie-less, DNT/GPC honoured, no device fingerprint, no third-party script | events unit tests (DNT/GPC) |
| Sensitive location disclosure | coarse, optional, reviewed; no visitor location; LR-090 | [location-discovery.md](location-discovery.md) |
| Crawler metadata leakage | metadata only from approved fields; non-public pages 404 to everyone; no secrets in previews | E2E metadata assertions |
| Admin area discovery | `/admin/discovery*` 404 for non-staff and staff without `discovery.view` | E2E 404 test |
| Browser storage of codes | none: referral codes and the traffic-source hand-off live in module memory (lint rule `no-restricted-globals` enforced) | lint |
| Queue starvation | indexing and jobs on the `discovery` River queue | worker wiring |

## 2. Database controls

New tables have no DELETE grant except the de-duplication keys; featured placements and ranking policies are guarded
state machines with immutable request/content columns, CHECKs for windows, reasons and decisions, and a maker-checker
trigger that consults `app.actor_identities`; the projection CHECK makes a removed row empty; referral tokens allow only
revocation columns to change; `app.campaign_locations` is read-only for runtime roles (reference list).

## 3. Residual risks

Distributed inflation from many addresses (KI-S16-03); external preview caches (KI-S16-02); unverified WhatsApp preview
rendering (KI-S16-01); legal basis for analytics and referral attribution (LR-035, LR-023); location granularity
(LR-090).

## 4. Defects found and fixed during the stage

1. Fuzzy search returned 500: the full-text condition was dropped but its parameter kept (untyped parameter). Fixed;
   covered by the integration typo test.
2. Concurrent reindexes timed out: the advisory-lock design held pooled connections while campaigns needed more.
   Replaced by snapshot-time ordering; covered by `TestDiscoveryConcurrency` (20 concurrent reindexes racing a
   suspension).
3. Screen readers would read highlighted titles without spaces ("HelpTariro"): plain segments are now bare text nodes;
   covered by a vitest accessible-name assertion.
4. Staff action confirmations vanished after a decision (the actions component unmounted with the record): actions now
   stay mounted and placements are one stable list; covered by E2E.
