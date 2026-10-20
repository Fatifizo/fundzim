# Stage 16 — Categories

## 1. Taxonomy

The approved taxonomy is the existing `app.campaign_categories` (Stage 6 seed; staff edit names, descriptions, order and
the active flag with `campaign.category.manage`; risk tier, organisation requirement, moderation policy and the sensitive
flag change only by migration). Stage 16 invents no category and adds no metadata table: descriptions and the
`sensitive` flag already exist (`discovery_category_metadata` from the brief was not needed).

## 2. Pages

| Route | Behaviour |
|---|---|
| `/categories` | active categories with live counts (Stage 7) |
| `/categories/[slug]` | description, live count, respectful-sharing notice for sensitive categories, "Popular right now" in the category (first page only), discovery search with the category fixed, other filters, sorts, keyset pagination; canonical `/categories/<slug>`; `noindex, follow` for filtered/sorted/later pages; `BreadcrumbList` JSON-LD |
| `/campaigns/category/[slug]` | permanent redirect (308) to `/categories/[slug]` (one canonical URL per category) |

`GET /discovery/categories` returns each active category with its number of discoverable campaigns (projection counts).
The sitemap lists only categories with at least one discoverable campaign (empty-category pages are still reachable but
not advertised).

## 3. Empty and sensitive categories

An empty category shows "There are no published campaigns here yet. Every campaign is reviewed before it is published."
Sensitive categories (e.g. medical, funeral, children's welfare) show "Please be respectful" and their campaigns keep
the Stage 6/7 disclosure rules (beneficiary names only with consent, never for minors).
