# Stage 7 — SEO

Code: `apps/web/src/lib/site/url.ts`, `lib/seo/{metadata,robots,json-ld}.ts`, `components/seo/json-ld.tsx`,
`app/{robots,sitemap,manifest}.ts`. Unit tests: `url.test.ts`, `robots.test.ts`, `json-ld.test.ts`.

## 1. Indexing is opt-in

FundZim is a development preview with no donations, so indexing is a deployment decision, never a side effect.

| Setting | Result |
|---|---|
| `PUBLIC_SITE_URL` unset or invalid | No canonical/OG/share/sitemap URLs; `robots.txt` = `User-agent: * / Disallow: /`; pages `noindex, nofollow, nocache`; sitemap empty |
| `PUBLIC_SITE_URL` valid, `SITE_INDEXING` ≠ `allow` | Absolute URLs and share links work; `robots.txt` still `Disallow: /`, without a sitemap line; pages `noindex`; `/sitemap.xml` lists URLs but is not referenced |
| `PUBLIC_SITE_URL` valid and `SITE_INDEXING=allow` | `robots.txt` allows `/`, disallows `/dashboard`, `/admin`, `/login`, `/register`, `/settings`, `/staff`, `/verify-email`, `/reset-password`, `/forgot-password`, `/api`, and names `/sitemap.xml`; public pages `index, follow` |

`PUBLIC_SITE_URL` must be an http(s) origin only (no path, query, fragment or credentials); anything else counts as
unset. Absolute URLs are never derived from `Host`/`X-Forwarded-*` headers. `absoluteUrl` refuses protocol-relative
(`//…`) paths. `robots.txt` and `sitemap.xml` are evaluated per request (`connection()`), so the environment is read at
run time rather than baked in at build.

Private pages (dashboard, settings, admin, staff, register, verification, organisations) carry their own
`noindex` metadata in addition to the robots rules. `/privacy` and `/terms` are draft placeholders: `noindex` and not
in the sitemap. `/c/[code]` short links are `noindex, follow`.

## 2. Per-page metadata (`pageMetadata`)

Title, description (truncated to 200 characters), `alternates.canonical`, Open Graph (title, description, type,
`siteName` FundZim, locale `en_ZW`, URL, image) and Twitter card. The image is the approved campaign cover with its
alt text, or the generic `/og/fundzim-og.png` (1200×630, alt "FundZim"). A missing or non-public campaign or category
gets "Page not found" with `noindex, nofollow`. Campaign titles get a suffix for paused or ended campaigns.

## 3. Canonical URLs

One canonical URL per campaign: `/campaigns/<canonical slug>`. Non-canonical slugs (wrong title part, right public
code) and `/c/<code>` short links redirect permanently to it (ADR-038 §6). `/explore` redirects to `/campaigns`.

## 4. Sitemap

`/sitemap.xml`: static public pages (`/`, `/campaigns`, `/categories`, `/how-it-works`, `/safety`, `/about`,
`/contact`), then active categories and live PUBLIC campaigns from `GET /public/sitemap` (deduplicated, `lastModified`
from `updated_at`). Unlisted, draft, suspended, archived and other non-live campaigns are never included. The API
caps campaigns at 50 000 (one sitemap file; a sitemap index is needed beyond that — KI-S7-12) and caches the data for
300 s, so a newly suspended campaign can stay listed for up to five minutes while its page already returns 404.

## 5. Structured data (JSON-LD)

Only what describes the page honestly:

- Home: `Organization` (name, URL, logo, description "A Zimbabwe-first donation crowdfunding platform under
  development.").
- Campaign page: `WebPage` (name, description, URL, primary image, dates, publisher).

Never emitted (enforced by `FORBIDDEN_JSON_LD_KEYS` and unit tests): `aggregateRating`, `review`, `award`,
`hasCredential`, `potentialAction`, `offers`, `amount`, `price`, `funder`, `sponsor` — no `DonateAction`, money,
ratings or certifications. JSON-LD needs an origin, so it is omitted without `PUBLIC_SITE_URL`. It is inlined with
the request's CSP nonce, and `serializeJsonLd` escapes `<`, `>`, `&`, U+2028 and U+2029 so campaign text cannot close
the script element.

## 6. Other

`<html lang="en-ZW">`; one `h1` per page; semantic headings; the web app manifest (`/manifest.webmanifest`). No
hreflang (English only; Shona and Ndebele content is not translated — KI-S7-08).

## 7. Not verified

Crawling and rendering by real search engines and validation in external rich-result testing tools were not done
(no public deployment exists) — KI-S7-06.
