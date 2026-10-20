# Stage 16 — Analytics

Code: `internal/discovery/events.go` (Recorder), `analytics.go` (owner and staff read models); web
`lib/analytics/events.ts`, `components/analytics/track-event.tsx`.

## 1. Pipeline

1. Browser → `POST /discovery/events` (1–20 events: `CAMPAIGN_VIEW` with source and optional ref, `SHARE_CLICK` with
   channel, `RESULT_CLICK` with surface). Fire-and-forget, `credentials: omit`, `keepalive`, no retries (a retry could
   double-count; a lost event is acceptable). Nothing is sent under Do Not Track or Global Privacy Control.
2. Handler: validates (closed value sets), rate limits (240/min per address), classifies the client (automated or not),
   enqueues in memory (bounded: 5 000; overflow counted as `INGEST_DROPPED`) and answers `204 No Content`. **No database
   write per request.** (A JSON body on the `keepalive` response kept the request open in Chromium; found by the CWV run.)
3. Recorder flush (every 2 s or at 500 queued): resolves slugs and eligibility in one query each, validates referral
   codes in one query, upserts per-visitor de-duplication keys in one statement (`RETURNING` the new hit counts), then
   upserts aggregates in one batch.
4. Search and listing impressions are counted by the API when it returns results (no client involvement).
5. Worker: trending every 15 min; maintenance deletes de-duplication keys older than 2 days.

A crash loses at most one flush interval of analytics and nothing else. Several API replicas each keep their own buffer.

## 2. Metric definitions (also returned with every analytics response)

| Metric | Definition |
|---|---|
| views | page views by browsers that did not identify as automated; at most 20 per visitor, campaign and day |
| unique visitors | daily estimate: a visitor is a keyed hash of network address and browser that changes every day and cannot be linked across days or campaigns; summed over days, a returning visitor counts once per day |
| bot views excluded | views from crawlers, link-preview fetchers and scripts; never engagement |
| shares | clicks on a share option; not proof anything was sent |
| referral visits | views arriving with a valid referral code, by channel |
| traffic sources | DIRECT, SEARCH, CATEGORY, FEATURED, TRENDING, RECOMMENDED, INTERNAL, UNKNOWN |
| impressions | times the card was returned by FundZim search or a discovery list (server-side) |
| clicks | clicks on the card from a discovery surface |
| click-through | clicks per impression in basis points (integer arithmetic); null without impressions |
| donations | not available: views, shares and clicks are never donations |

Every response is labelled `MEASURED_NON_PRODUCTION` (figures come from development and test use of the environment).

## 3. Accuracy

Raw views and shares are capped counts; unique visitors are estimates (shared addresses under-count, changing addresses
over-count); bot filtering is a user-agent rule (misses bots that pretend to be browsers); referral attribution is
partial ([referrals.md](referrals.md) §2). No figure is extrapolated, smoothed or fabricated; zero is shown as zero and
"not measured" as such.

## 4. Who sees what

| Viewer | Route | Scope |
|---|---|---|
| Campaign owner, organisation members | `GET /campaigns/{id}/analytics`, `/dashboard/campaigns/[id]/analytics` | their campaign only; anyone else 404 (no existence oracle) |
| Staff with `discovery.view` | `/admin/discovery/overview`, `/admin/discovery` | aggregates across campaigns, search counters, abuse indicators, top trending |
| Public | — | none (no view or share counts are shown publicly; no social proof is fabricated) |

## 5. Stage 7 counters

`POST /public/events` and `app.public_event_counters` remain for compatibility (no longer called by the web app); new
work uses the discovery pipeline. Removal is a Stage 18 clean-up (KI-S16-08).
