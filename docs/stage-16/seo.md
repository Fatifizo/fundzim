# Stage 16 — SEO

## 1. Indexing switch

Unchanged from Stage 7: crawlers are kept out entirely unless `PUBLIC_SITE_URL` is set **and** `SITE_INDEXING=allow`
(`indexingAllowed`). Private areas are disallowed in robots.txt and carry `noindex` themselves; robots.txt is never
relied on for access control (every private page is authorised server-side).

## 2. Canonical policy

| Page | Canonical | Robots |
|---|---|---|
| `/campaigns` (no query, filter, sort or cursor) | `/campaigns` | index, follow |
| `/campaigns?…` (any query, filter, sort or page) | `/campaigns` | **noindex, follow** |
| `/categories/<slug>` (unfiltered first page) | itself | index, follow |
| `/categories/<slug>?…` | `/categories/<slug>` | noindex, follow |
| `/campaigns/location/<province>` / `?…` | itself / the province page | index / noindex, follow |
| `/campaigns/trending`, `/campaigns/featured` | itself | index, follow |
| `/campaigns/<slug>` (also with `?ref=`) | `/campaigns/<canonical slug>` | index, follow |
| `/campaigns/<old-title>-<code>`, `/c/<code>`, `/campaigns/search`, `/campaigns/category/<slug>` | — | permanent redirect (308) |

Tracking parameters (`ref`) never change the canonical URL and are removed from the address bar after the visit.

## 3. Sitemap (`/sitemap.xml`, from `GET /discovery/sitemap`)

Static public pages (`/`, `/campaigns`, `/campaigns/trending`, `/campaigns/featured`, `/categories`, `/how-it-works`,
`/safety`, `/about`, `/contact`), then categories and provinces with at least one discoverable campaign, then up to
50 000 discoverable campaigns (re-checked in batches against current eligibility). Excluded: drafts, campaigns under
review, suspended, restricted, unlisted, cancelled and archived campaigns, private profiles, staff and account pages, every filtered or paginated
listing URL. Draft legal pages stay out until final (Stage 7).

## 4. Structured data

| Page | Types |
|---|---|
| Home | `Organization` |
| Campaign | `WebPage` (name, description, URL, primary image, publication date, publisher, `about` category), `BreadcrumbList` |
| Category | `BreadcrumbList` |

Never: `DonateAction`, `MonetaryAmount`, totals, ratings, reviews, awards, credentials, verification or tax-deductible
claims (unit test with a forbidden-key list). JSON-LD is serialised with `<`, `>`, `&`, U+2028/2029 escaped (unit test
with a `</script>` title).

## 5. Metadata

Titles and descriptions from approved text only (escaped by React/Next metadata), paused/ended markers in titles, Open
Graph and Twitter cards with absolute URLs from `PUBLIC_SITE_URL` (never request headers), image dimensions for the
generic and generated images. Clean slugs (`<title>-<public code>`; fixed at first publication; old slugs redirect by
public code; non-public campaigns are a 404, so slugs cannot be used to enumerate drafts).
