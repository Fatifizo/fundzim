# Stage 7 — Public experience

What a visitor sees, page by page, and the rules behind it. Decisions: ADR-038. Code: `apps/web/src/app/**`,
`apps/web/src/components/{campaigns,layout,seo,analytics,pwa}`; developer notes in `apps/web/README.md`
"Public experience (Stage 7)".

## 1. Global rules

- **No money is shown or collected.** No totals, raised amounts, progress bars, donor counts or donor lists. The goal
  is shown from `amount_minor` + currency using integer/string arithmetic only. The campaign page has a disabled
  "Donations are not yet available." control; the home, how-it-works and safety pages say donations are not
  available yet.
- **Approved content only.** Public pages render the campaign's approved version (`approved_version_id`); a campaign
  that is not live and public is a 404 everywhere.
- **Escaped plain text.** Titles, summaries, stories, updates and report details are rendered as React text nodes:
  no HTML, no auto-linked URLs, never `dangerouslySetInnerHTML` for user content. The only
  `dangerouslySetInnerHTML` use is the JSON-LD script, whose content is built by our code and escaped by
  `serializeJsonLd` (see [seo.md](seo.md)).
- **Fresh data.** Server rendering fetches campaign data with `cache: 'no-store'`; the API answers `public, no-cache`
  with an ETag (ADR-038 §1). A suspended campaign disappears on the next request.
- **Per-request nonce CSP** (ADR-033) on every page; no third-party scripts, fonts or trackers.
- **Privacy.** A beneficiary's name is shown only when its disclosure is `DISPLAY_NAME`; otherwise the page says
  "Details kept private". No organiser contact details, ids, KYC or review information are exposed (enforced by the
  API allow-list test).

## 2. Pages

| Route | Content |
|---|---|
| `/` | Hero ("Fund anyone in Zimbabwe, from anywhere"), a note that donations are not available yet, featured campaigns (with the explanation that some are chosen by the FundZim team and being featured is not an endorsement or ranking; shown only when the list contains staff picks), categories with live counts, how it works (four steps, the last "Receive support when payments become available"), trust and safety (including "What checks cannot promise"), call to action. Organization JSON-LD |
| `/campaigns` | Search and browse: text query (≤ 100 characters), category (code or slug), sort (`published` or `created`), keyset "more results" cursor, total (shown as "1000+ campaigns" when capped). See [campaign-discovery.md](campaign-discovery.md) |
| `/campaigns/[slug]` | Approved title, summary, cover and gallery images, story, goal, category, organiser (organisation display name for organisation campaigns), beneficiary (or "Details kept private"), trust indicators with the disclaimer, a notice for PAUSED or COMPLETED campaigns, updates (visible ones only), share controls, Report button, disabled donate control. A non-canonical slug redirects permanently (`permanentRedirect`) to the canonical one. WebPage JSON-LD |
| `/c/[code]` | Short link: resolves the public code and redirects permanently to `/campaigns/<canonical slug>`; `noindex`. Only internal campaign paths are ever redirect targets |
| `/categories`, `/categories/[slug]` | Category list with live counts; one category's campaigns (same listing as `/campaigns`) |
| `/how-it-works`, `/safety`, `/about`, `/contact` | Static explanatory pages. Safety covers how review works, what verification means and does not guarantee, spotting suspicious requests, how to report, donations today |
| `/privacy`, `/terms` | "DRAFT — placeholder only": counsel will draft them before FundZim opens (**LEGAL_REVIEW_REQUIRED**, LR-022, LR-035). Kept out of the sitemap |
| `/explore` | Redirects to `/campaigns` (old Stage 3 link) |
| `/offline.html` | Static offline page served by the service worker (no scripts) |

## 3. Trust indicators (ADR-038 §5)

Computed live by the API and shown only when true now. Copy (`apps/web/src/lib/campaigns/trust.ts`):

| Indicator | Label | Explanation |
|---|---|---|
| `campaign_reviewed` | Reviewed before publishing | A FundZim reviewer checked this campaign against FundZim's rules before it was published. |
| `organiser_identity_verified` | Organiser's identity verified | The organiser completed FundZim's identity check. (Owner `IDENTITY_VERIFIED` or higher, profile `ACTIVE`; an expired level counts as unverified) |
| `organisation_verified` | Organisation verified | The organisation running this campaign completed FundZim's organisation check. (`ORG_KYB_VERIFIED`, profile `ACTIVE`) |
| `beneficiary_verified` | Beneficiary verified | The person or organisation the campaign is for completed FundZim's beneficiary check. (Beneficiary `APPROVED`; a SELF beneficiary counts when the organiser is identity verified) |

Disclaimer shown with them: "Verified identity does not guarantee the campaign's claims. Checks confirm who people
are, not that every statement in a story is true. Read carefully, and report anything that worries you." No other
badges exist.

## 4. Reporting a campaign

The Report button opens an accessible dialog: a reason (radio group: misleading information, impersonation,
suspicious activity, inappropriate content, privacy concern, other), details (required, at least 10 characters, for
"other"; at most 2 000), an optional contact email, and a visually hidden honeypot field (`website`, `tabIndex=-1`,
`aria-hidden`). A submission is never retried automatically. Accepted reports, same-day duplicates and honeypot hits
all get the same confirmation (202), so the response reveals nothing. Rate limits: 5 per hour per IP, 50 per hour per
campaign. Staff triage at `/admin/campaigns/reports` (permission `campaign.review`): dismiss or escalate with a
reason code and note; escalation opens a compliance case. The owner never sees reports; reporter contact emails are
never shown (only "has contact"). A report never changes a campaign's status.

## 5. Featured campaigns (ADR-038 §2)

Staff manage picks at `/admin/campaigns/featured` (permission `campaign.feature`, step-up). Only live, listed
(`PUBLIC`, not `COMPLETED`) campaigns can be picked; a pick whose campaign later leaves public view is skipped (staff
list shows `shown: false`). The public list fills the remainder with the most recently published live campaigns;
each item carries `selection: STAFF_PICK | RECENTLY_PUBLISHED`. No popularity, ranking or paid promotion.

## 6. First-party analytics (ADR-038 §4)

`TrackEvent` sends `CAMPAIGN_PAGE_VIEW`, `CAMPAIGN_SHARE_CLICK` (with channel), `CAMPAIGN_SEARCH` (only whether a
category filter was used — never the query text) and `CATEGORY_VIEW` to `POST /api/v1/public/events` with
`credentials: "omit"`. Nothing is sent when the browser signals Do Not Track or Global Privacy Control; failures are
ignored. The API stores daily aggregate counters only (no IP, no user id, no cookie). Counters are internal: no
public metric is displayed and no engagement figure is shown or fabricated.

## 7. PWA

`/manifest.webmanifest` (name FundZim, `display: standalone`, `lang: en-ZW`, SVG/192/512 and maskable icons). The
service worker (`public/sw.js`) caches only same-origin, query-less `/_next/static/` assets (cache-first, at most 150
entries) and the static offline page; it never intercepts or caches API responses, pages, media or cross-origin
requests, and never queues anything for replay. Online navigations always go to the network (fresh nonce). When the
browser reports it is offline, a navigation gets the offline page with its own restrictive CSP
(`default-src 'none'; style-src 'self'; img-src 'self'; …`) and status 503. `next.config.ts` serves `/sw.js` with
its own headers (see [security-review.md](security-review.md)).

## 8. Mobile-first

Layouts are designed at phone width first (Tailwind breakpoints upward), touch targets at least 44 px (`min-h-11`),
`enterKeyHint="search"` on the search field, a mobile navigation menu, and share controls that use the Web Share API
when the browser offers it. Mobile Chromium is one of the Playwright projects (mock and real-stack suites).
Performance on a throttled mobile profile (Lighthouse / Core Web Vitals) was **not measured** in Stage 7 (KI-S7-05).
